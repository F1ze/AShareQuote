// Package frontend supervises the independent Vite development server.
package frontend

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Config 配置独立的 Vite 开发进程；Go API 不嵌入或托管 React 静态资源。
type Config struct {
	Directory string    // 前端工程目录；空值时依次从工作目录和可执行文件附近寻找 package.json。
	Host      string    // Vite 监听地址，由主进程 --host 传入。
	Port      int       // Vite 监听端口，启用 strictPort；占用时失败而不自动换端口。
	APIURL    string    // Echo 地址，注入 QFLUX_API_TARGET 供 Vite 代理 /api。
	Stdout    io.Writer // 依赖安装与 Vite 的标准输出接收器，nil 时丢弃。
	Stderr    io.Writer // 依赖安装与 Vite 的错误输出接收器，nil 时丢弃。
}

// Runner 监管一个前端子进程；只有 Wait 协程写入最终 err 并关闭 done。
type Runner struct {
	cmd  *exec.Cmd     // 正在监管的 Node/Vite 命令；Close 必要时终止其进程。
	done chan struct{} // Wait 返回后关闭的广播通道；关闭仅代表退出，不代表成功启动或正常退出。
	mu   sync.RWMutex  // 保护 err 的并发读写；进程退出观察者通过 Err 取得结果。
	err  error         // Wait 的最终错误，运行中或正常退出时可能为 nil，需结合 done 判断是否结束。
}

// Start 的流程是：解析前端目录 → 查找 Node → 依赖缺失时安装 → 启动 Vite → 启动 Wait 监管。
// 返回 Runner 只说明子进程已创建，不保证端口已经就绪；运行错误需结合 Done/Err 观察。
func Start(ctx context.Context, config Config) (*Runner, error) {
	if config.Stdout == nil {
		config.Stdout = io.Discard
	}
	if config.Stderr == nil {
		config.Stderr = io.Discard
	}
	directory, err := resolveDirectory(config.Directory)
	if err != nil {
		return nil, err
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return nil, fmt.Errorf("未找到 Node.js；前端运行需要 Node.js 20 或更高版本")
	}
	vite := filepath.Join(directory, "node_modules", "vite", "bin", "vite.js")
	if _, err := os.Stat(vite); err != nil {
		if err := install(ctx, directory, config.Stdout, config.Stderr); err != nil {
			return nil, err
		}
	}
	if _, err := os.Stat(vite); err != nil {
		return nil, fmt.Errorf("前端依赖安装后仍未找到 Vite：%w", err)
	}
	// 直接调用 node+Vite 脚本，避免额外 npm/shell 常驻层；父上下文取消会终止本进程。
	command := exec.CommandContext(ctx, node, vite, "--host", config.Host, "--port", strconv.Itoa(config.Port), "--strictPort")
	command.Dir = directory
	command.Env = append(os.Environ(), "QFLUX_API_TARGET="+config.APIURL)
	command.Stdout = config.Stdout
	command.Stderr = config.Stderr
	command.WaitDelay = 5 * time.Second
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("启动前端：%w", err)
	}
	runner := &Runner{cmd: command, done: make(chan struct{})}
	// 唯一 Wait 协程先发布退出错误，再关闭 done，观察者收到 done 后即可读取最终结果。
	go func() {
		err := command.Wait()
		runner.mu.Lock()
		runner.err = err
		runner.mu.Unlock()
		close(runner.done)
	}()
	return runner, nil
}

func install(ctx context.Context, directory string, stdout, stderr io.Writer) error {
	npm, err := exec.LookPath("npm")
	if err != nil {
		return fmt.Errorf("首次运行需要安装前端依赖，但未找到 npm")
	}
	commandName := "install"
	if _, err := os.Stat(filepath.Join(directory, "package-lock.json")); err == nil {
		commandName = "ci"
	}
	fmt.Fprintln(stdout, "首次运行：正在安装前端依赖…")
	command := exec.CommandContext(ctx, npm, commandName)
	command.Dir = directory
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("安装前端依赖：%w", err)
	}
	return nil
}

func resolveDirectory(configured string) (string, error) {
	candidates := make([]string, 0, 3)
	if configured != "" {
		candidates = append(candidates, configured)
	} else {
		candidates = append(candidates, "frontend")
		if executable, err := os.Executable(); err == nil {
			directory := filepath.Dir(executable)
			candidates = append(candidates, filepath.Join(directory, "frontend"), filepath.Join(directory, "..", "frontend"))
		}
	}
	seen := make(map[string]bool)
	for _, candidate := range candidates {
		absolute, err := filepath.Abs(candidate)
		if err != nil || seen[absolute] {
			continue
		}
		seen[absolute] = true
		if info, err := os.Stat(filepath.Join(absolute, "package.json")); err == nil && !info.IsDir() {
			return absolute, nil
		}
	}
	return "", fmt.Errorf("未找到前端工程；请在项目根目录运行，或通过 --frontend-dir 指定 frontend 目录")
}

func (r *Runner) Done() <-chan struct{} { return r.done }

func (r *Runner) Err() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.err
}

// Close 已退出时立即返回；否则终止受管进程并最多等 6 秒回收，不无限阻塞应用退出。
func (r *Runner) Close() {
	select {
	case <-r.done:
		return
	default:
	}
	if r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
	}
	select {
	case <-r.done:
	case <-time.After(6 * time.Second):
	}
}
