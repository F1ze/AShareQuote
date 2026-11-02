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
	"syscall"
	"time"

	"quotes/internal/core"
	"quotes/internal/download"
	"quotes/internal/httpapi"
	"quotes/internal/lines"
	"quotes/internal/tdx"
	"quotes/web"
)

func main() {
	host := flag.String("host", envOr("HOST", "127.0.0.1"), "网页监听地址")
	port := flag.Int("port", envInt("PORT", 7100), "网页监听端口")
	outputDir := flag.String("output", envOr("QUOTES_OUTPUT", "downloads"), "默认行情输出目录")
	flag.Parse()

	provider := lines.NewBuiltin(func(host core.Host, timeout time.Duration) lines.SmokeClient {
		return tdx.NewClient([]core.Host{host}, timeout, 0)
	})
	factory := func(hosts []core.Host, timeout time.Duration, offset int) download.Client {
		return tdx.NewClient(hosts, timeout, offset)
	}
	manager := download.NewManager(provider, absoluteOr(*outputDir, "downloads"), factory)
	server := &http.Server{
		Addr:              net.JoinHostPort(*host, strconv.Itoa(*port)),
		Handler:           httpapi.NewServer(manager, provider, web.Assets()).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go shutdownOnContext(ctx, server)

	fmt.Printf("QFLUX 行情下载台：http://%s\n", server.Addr)
	fmt.Printf("线路配置：内置模板，自动回读与业务自检\n")
	fmt.Printf("默认输出目录：%s\n", manager.DefaultOutput())
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
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

func shutdownOnContext(ctx context.Context, server *http.Server) {
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
}
