package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"quotes/internal/domain"
	"quotes/internal/handler"
	"quotes/internal/platform/baostock"
	"quotes/internal/platform/filesys"
	webfront "quotes/internal/platform/frontend"
	"quotes/internal/platform/historyfile"
	"quotes/internal/platform/lines"
	"quotes/internal/platform/netdial"
	"quotes/internal/platform/processlock"
	"quotes/internal/platform/tdx"
	"quotes/internal/repository"
	"quotes/internal/service"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run 是依赖组装根：注入来源和存储后，让网页服务与无界面批处理复用同一个 Manager。
// 用返回错误代替中途 os.Exit，保证文件锁、工作进程和任务的 defer 清理都能执行。
func run() error {
	// 第 1 步：解析命令行/环境变量，建立进程退出上下文，并锁定数据目录的单写者资格。
	host := flag.String("host", envOr("HOST", "127.0.0.1"), "前后端监听地址")
	port := flag.Int("port", envInt("PORT", 7100), "前端监听端口")
	apiPort := flag.Int("api-port", envInt("API_PORT", 7101), "Echo API 监听端口")
	apiOnly := flag.Bool("api-only", false, "仅启动 Echo API，不启动前端")
	frontendDir := flag.String("frontend-dir", envOr("QFLUX_FRONTEND_DIR", ""), "前端工程目录")
	outputDir := flag.String("output", envOr("QUOTES_OUTPUT", "downloads"), "默认行情输出目录")
	dataDir := flag.String("data-dir", envOr("QFLUX_DATA_DIR", "data"), "DuckDB 与持久任务日志目录")
	syncMode := flag.Bool("sync", false, "执行一次全 A 股增量入库后退出，适用于计划任务")
	resumeID := flag.String("resume", "", "恢复指定任务后退出")
	syncCodes := flag.String("codes", "", "批处理证券代码；留空为沪深北全 A 股")
	syncCategory := flag.Int("category", 9, "批处理行情周期，9=日线，7=1 分钟，0=5 分钟")
	syncFrom := flag.String("from", "1990-12-19", "批处理历史起点")
	syncTo := flag.String("to", time.Now().In(domain.ExchangeTime).AddDate(0, 0, -1).Format("2006-01-02"), "批处理结束日，默认北京时间昨日")
	fullSync := flag.Bool("full", false, "批处理重新扫描整个请求区间，用于历史修订和补洞")
	syncSource := flag.String("source", "tdx", "批处理数据源：tdx 或 baostock（仅沪深，含源提供的退市证券）")
	collectFactors := flag.Bool("factors", true, "BaoStock 采集时同步独立复权因子表")
	proxyURL := flag.String("proxy", "", "出站代理（socks5/socks5h/http/https 地址）；留空使用已保存设置或环境变量")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dataPath := absoluteOr(*dataDir, "data")
	if err := os.MkdirAll(dataPath, 0700); err != nil {
		return err
	}
	// 同一数据目录只允许一个应用写者；锁句柄由操作系统管理，崩溃后也能自动释放。
	lock, err := processlock.Acquire(filepath.Join(dataPath, "qflux.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()

	// 第 2 步：装配统一出站代理与拨号器；设置持久化在数据目录，空配置直连。
	// 拨号器在每次新连接时读取当前快照，网页修改后无需重启即可生效。
	dialer, err := netdial.NewManager(domain.ProxySettings{})
	if err != nil {
		return err
	}
	var historySource *baostock.Source
	proxySettings, err := service.NewProxyService(hotProxyStore{
		inner: repository.NewSettingsStore(filepath.Join(dataPath, "settings.json")),
		apply: func(updated domain.ProxySettings) error {
			if err := dialer.Update(updated); err != nil {
				return err
			}
			// 数据会话进程只在启动时读取代理环境；结束旧会话让下次调用按新代理重建。
			if historySource != nil {
				historySource.Close()
			}
			return nil
		},
	})
	if err != nil {
		return err
	}
	// 环境变量按通用约定只提供初始值；界面已保存的设置与 --proxy 始终优先。
	if env := envProxyValue(); env != "" {
		if seeded, err := domain.ParseProxyURL(env); err == nil {
			proxySettings.Seed(seeded)
		} else {
			log.Printf("忽略无效的代理环境变量：%v", err)
		}
	}
	if err := dialer.Update(proxySettings.Current()); err != nil {
		return fmt.Errorf("装配出站代理：%w", err)
	}
	if *proxyURL != "" {
		if _, err := proxySettings.Update(*proxyURL); err != nil {
			return fmt.Errorf("解析代理地址：%w", err)
		}
	}

	// 第 3 步：组装通达信线路/客户端、原始归档、DuckDB 和可扩展来源，恢复已有任务日志。
	// 线路探测客户端仅验证一个端点；任务客户端接收可用线路列表，失败时可自行轮换。
	provider := lines.NewBuiltin(func(host domain.Host, timeout time.Duration) lines.SmokeClient {
		client := tdx.NewClient([]domain.Host{host}, timeout, 0)
		client.SetDialer(dialer.DialContext)
		return client
	})
	factory := func(hosts []domain.Host, timeout time.Duration, offset int) service.Client {
		client := tdx.NewClient(hosts, timeout, offset)
		client.SetDialer(dialer.DialContext)
		return client
	}
	project, err := os.Getwd()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(project, "internal", "platform", "baostock", "source_worker.py")); err != nil {
		if executable, e := os.Executable(); e == nil {
			project = filepath.Dir(filepath.Dir(executable))
		}
	}
	warehouse := repository.NewDuckDB(filepath.Join(absoluteOr(*dataDir, "data"), "quotes.duckdb"))
	defer warehouse.Close()
	jobs, err := repository.NewJobJournal(filepath.Join(dataPath, "jobs"))
	if err != nil {
		return err
	}
	manager, err := service.NewJobService(service.JobDependencies{
		DefaultOutput: absoluteOr(*outputDir, "downloads"), Lines: provider, NewClient: factory,
		Archives: func(output string) service.Archive { return &repository.CSVArchive{Directory: output} },
		Factors:  repository.FileFactors{}, Jobs: jobs, Warehouse: warehouse, Local: historyfile.New(),
	})
	if err != nil {
		return err
	}
	if err = manager.Restore(); err != nil {
		return fmt.Errorf("恢复任务日志：%w", err)
	}
	historySource = baostock.New(project)
	defer historySource.Close()
	// 每次启动数据会话时求值；代理变更后由热应用回调结束旧会话并按新值重建。
	historySource.EnvFunc = func() []string {
		// 用解析后的统一值改写标准代理变量，避免子进程继承的环境与 Go 侧当前状态分叉。
		current := proxySettings.Current().URL
		return []string{
			"ALL_PROXY=" + current, "all_proxy=" + current,
			"HTTPS_PROXY=", "https_proxy=", "HTTP_PROXY=", "http_proxy=",
		}
	}
	if err := manager.RegisterSource(historySource.Registration()); err != nil {
		return err
	}
	// defer 按后进先出执行：先取消并等待任务，再关闭 SDK 和仓库进程，最后释放目录锁。
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		manager.Shutdown(shutdown)
	}()
	// 第 3 步：无界面模式直接执行同一任务用例并返回，跳过 HTTP 和 Vite；错误由 main 转退出码。
	if *syncMode || *resumeID != "" {
		markets := []int{0, 1, 2}
		if *syncSource == "baostock" {
			markets = []int{0, 1}
		}
		return runSync(ctx, manager, domain.DownloadOptions{Markets: markets, Codes: *syncCodes, Category: *syncCategory, StartDate: *syncFrom, EndDate: *syncTo, ASharesOnly: true, Incremental: !*fullSync, Storage: "duckdb", Source: *syncSource, CollectFactors: *collectFactors && *syncSource == "baostock", OutputDir: manager.DefaultOutput()}, *resumeID)
	}
	// 第 4 步：网页模式先实际绑定 API 端口，再启动前端，避免端口冲突时留下无用 Vite 进程。
	frontendURL := localURL(*host, *port)
	apiURL := localURL(*host, *apiPort)
	apiHandler := handler.NewServer(handler.Dependencies{
		Jobs: manager, Workspace: service.NewWorkspaceService(filesys.NewBrowser(manager.DefaultOutput()), provider),
		Preview:   service.NewPreviewService(manager.OutputDir, func(output string) service.PreviewRepository { return &repository.CSVArchive{Directory: output} }),
		Warehouse: service.NewWarehouseService(warehouse),
		Proxy:     proxySettings,
	}).Handler(frontendURL)
	server := &http.Server{
		Addr:              net.JoinHostPort(*host, strconv.Itoa(*apiPort)),
		Handler:           apiHandler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return fmt.Errorf("监听 Echo API：%w", err)
	}
	defer listener.Close()

	go shutdownOnContext(ctx, server)

	var frontend *webfront.Runner
	if !*apiOnly {
		frontend, err = webfront.Start(ctx, webfront.Config{
			Directory: *frontendDir,
			Host:      *host,
			Port:      *port,
			APIURL:    apiURL,
			Stdout:    os.Stdout,
			Stderr:    os.Stderr,
		})
		if err != nil {
			return err
		}
		defer frontend.Close()
		// 第 5 步：监管前端退出；意外退出触发全局取消，使 API、下载和工作进程按同一生命周期收尾。
		go func() {
			<-frontend.Done()
			if ctx.Err() == nil {
				log.Printf("前端进程已退出：%v", frontend.Err())
				stop()
			}
		}()
	}

	fmt.Printf("QFLUX 前端：%s\n", frontendURL)
	fmt.Printf("Echo API：%s/api\n", apiURL)
	fmt.Printf("线路配置：内置模板，自动回读与业务自检\n")
	if current := proxySettings.Current().URL; current != "" {
		fmt.Printf("出站代理：%s\n", current)
	} else {
		fmt.Printf("出站代理：未设置（直连）\n")
	}
	fmt.Printf("默认输出目录：%s\n", manager.DefaultOutput())
	if *apiOnly {
		fmt.Printf("当前为 API-only 模式，请单独启动 frontend。\n")
	}
	// 第 6 步：阻塞服务直到退出信号或错误；正常 Shutdown 不当作故障，返回后统一执行 defer 清理。
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err != nil || value < 1 || value > 65535 {
		return fallback
	}
	return value
}

func absoluteOr(path, fallback string) string {
	if path == "" {
		path = fallback
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return absolute
}

func localURL(host string, port int) string {
	if host == "0.0.0.0" || host == "::" || host == "" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port))
}

// shutdownOnContext 在退出信号到达后停止接收新请求，用独立超时上下文等待现有请求收尾。
// 不能复用已经取消的 ctx，否则 Shutdown 会立即放弃等待。
func shutdownOnContext(ctx context.Context, server *http.Server) {
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
}

// hotProxyStore 在持久化成功后同步执行组装根提供的热应用回调。
// 属于组装层的组合件：让“保存代理设置”的用例顺带更新拨号器并结束旧数据会话。
type hotProxyStore struct {
	inner service.ProxyStore               // 实际落盘的仓库实现。
	apply func(domain.ProxySettings) error // 落盘成功后的应用回调；返回错误视为保存失败。
}

func (s hotProxyStore) Load() (domain.ProxySettings, error) { return s.inner.Load() }

func (s hotProxyStore) Save(settings domain.ProxySettings) error {
	if err := s.inner.Save(settings); err != nil {
		return err
	}
	return s.apply(settings)
}

// envProxyValue 按 curl 等工具的通用约定读取出站代理初始值：
// ALL_PROXY → HTTPS_PROXY → HTTP_PROXY，各变量同时接受小写形式。
func envProxyValue() string {
	for _, key := range []string{"ALL_PROXY", "all_proxy", "HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}
