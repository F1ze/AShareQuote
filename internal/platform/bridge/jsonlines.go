// Package bridge 封装可取消的 JSON Lines 工作进程，用于隔离 Python SDK 运行时。
package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// Process 复用一个长驻子进程；mu 覆盖完整请求/应答周期及进程生命周期。
// 协议没有请求编号，必须串行收发，否则不同调用可能读到彼此的应答。
type Process struct {
	mu             sync.Mutex      // 保护进程启动/停止及完整收发周期；同一进程只允许一个未完成请求。
	Python, Script string          // Python 为解释器路径/名称；Script 为工作脚本路径，二者不是可执行 shell 命令串。
	Args           []string        // 工作脚本参数，例如 SDK 会话参数，通过 exec 参数传递而非 shell 拼接。
	EnvFunc        func() []string // 每次启动子进程时求值的附加环境变量（KEY=VALUE）；nil 表示无附加。
	cmd            *exec.Cmd       // 当前长驻子进程；nil 表示尚未启动或故障后已回收。
	input          io.WriteCloser  // 子进程 stdin；每次写一个 UTF-8 JSON 对象及换行。
	output         *bufio.Reader   // 子进程 stdout；每次读取完整的一行 JSON 应答，日志必须写 stderr。
}

// New 只确定解释器和脚本位置，不立即启动进程；未使用的来源不会产生后台进程。
// 解释器优先级为显式环境变量、项目虚拟环境、系统 Python。
func New(project, script string, args ...string) *Process {
	python := os.Getenv("QFLUX_PYTHON")
	if python == "" {
		for _, candidate := range []string{filepath.Join(project, ".venv", "Scripts", "python.exe"), filepath.Join(project, ".venv", "bin", "python")} {
			if _, err := os.Stat(candidate); err == nil {
				python = candidate
				break
			}
		}
	}
	if python == "" {
		python = "python"
	}
	return &Process{Python: python, Script: filepath.Join(project, script), Args: args}
}

func (d *Process) start() error {
	if d.cmd != nil {
		return nil
	}
	// -u 禁用 Python 输出缓冲，让一行应答及时到达，避免请求一直等待缓冲区刷新。
	cmd := exec.Command(d.Python, append([]string{"-u", d.Script}, d.Args...)...)
	// 环境在每次启动时求值：代理等运行期设置变更后，重启的会话立即使用新值。
	cmd.Env = os.Environ()
	if d.EnvFunc != nil {
		cmd.Env = append(cmd.Env, d.EnvFunc()...)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		return err
	}
	// stdout 专用于 JSON 协议，诊断信息走 stderr，避免普通日志被误读为应答。
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		in.Close()
		return err
	}
	d.cmd, d.input, d.output = cmd, in, bufio.NewReader(out)
	return nil
}

// stop 由持锁调用方执行；Kill 后 Wait 回收进程，并清空句柄供下次调用重新启动。
func (d *Process) stop() {
	if d.cmd != nil {
		_ = d.input.Close()
		_ = d.cmd.Process.Kill()
		_ = d.cmd.Wait()
	}
	d.cmd, d.input, d.output = nil, nil, nil
}

func (d *Process) Close() { d.mu.Lock(); defer d.mu.Unlock(); d.stop() }

// Call 在单次调用内完成“一行请求、一行应答”；取消时销毁进程而不是留下未知协议状态。
// 若取消前数据库可能已经提交，调用方必须依靠幂等重放恢复，不能推断一定未写入。
func (d *Process) Call(ctx context.Context, request any) (json.RawMessage, error) {
	// 第 1 步：独占整个请求周期，检查取消后按需启动进程，再编码请求。
	// 等待互斥锁不会打断当前持有者；轮到本调用后才检查自己的 ctx。
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := d.start(); err != nil {
		return nil, fmt.Errorf("启动数据进程：%w；请运行 npm run setup:data", err)
	}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	type response struct {
		Data  json.RawMessage `json:"data"`  // 成功返回的 JSON 载荷，保留原字节供具体适配器解码。
		Error string          `json:"error"` // 工作进程的业务错误；有完整应答时无需销毁会话。
	}
	type outcome struct {
		value response // 一轮收发取得的协议应答，err 非 nil 时不能信任其内容。
		err   error    // 管道写入、读取或 JSON 解码错误，区别于 response.Error 中的业务错误。
	}
	// 第 2 步：把阻塞管道读写放入一个协程，主协程才能同时观察取消信号。
	// 单槽缓冲容纳唯一结果，协议侧一次只允许一个在途请求。
	result := make(chan outcome, 1)
	// 捕获本次管道，后台协程不再读取可能被 stop 清空的 Process 字段。
	input, output := d.input, d.output
	go func() {
		_, err := input.Write(append(data, '\n'))
		var value response
		if err == nil {
			var line []byte
			line, err = output.ReadBytes('\n')
			if err == nil {
				err = json.Unmarshal(line, &value)
			}
		}
		result <- outcome{value, err}
	}()
	// 第 3 步：二选一收尾。取消或传输损坏会废弃进程；完整业务错误只返回错误并保留会话。
	// 废弃进程后，下次调用会新建管道；正常应答或完整业务错误则保留已对齐的会话。
	select {
	case <-ctx.Done():
		d.stop()
		// 关闭进程会解除管道阻塞；等收发协程退出后才释放锁，防止旧应答混入后续请求。
		<-result
		return nil, ctx.Err()
	case out := <-result:
		if out.err != nil {
			d.stop()
			return nil, fmt.Errorf("数据进程连接失败，请运行 npm run setup:data，并确认数据服务可用：%w", out.err)
		}
		// 业务错误仍是一条完整应答，不必重启会话；只有传输/解码故障才废弃进程。
		if out.value.Error != "" {
			return nil, fmt.Errorf("数据进程：%s", out.value.Error)
		}
		return out.value.Data, nil
	}
}
