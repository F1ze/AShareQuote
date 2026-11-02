GO ?= go
PYTHON ?= python
HOST ?= 127.0.0.1
PORT ?= 7100
OUTPUT ?= downloads

BIN := bin/quotes
ifeq ($(OS),Windows_NT)
BIN := bin/quotes.exe
# Prefer the standard installation when go.exe has not been added to PATH.
ifneq ($(wildcard C:/Program\ Files/Go/bin/go.exe),)
GO := C:/Program Files/Go/bin/go.exe
endif
endif

.PHONY: help build run dev test cover vet fmt check smoke tidy clean

help: ## 显示所有可用命令
	@echo "QFLUX 行情下载台"
	@echo ""
	@echo "  make build   编译程序到 $(BIN)"
	@echo "  make run     编译并运行程序"
	@echo "  make dev     使用 go run 启动开发服务"
	@echo "  make test    运行全部单元与集成测试"
	@echo "  make cover   运行测试并显示覆盖率"
	@echo "  make vet     执行 Go 静态检查"
	@echo "  make fmt     格式化全部 Go 代码"
	@echo "  make check   依次执行 test、vet、build"
	@echo "  make smoke   检查 HTTP 接口并生成桌面/移动截图"
	@echo "  make tidy    整理 Go 模块依赖"
	@echo "  make clean   清理编译与测试产物"
	@echo ""
	@echo "可覆盖参数：HOST=$(HOST) PORT=$(PORT) OUTPUT=$(OUTPUT)"
	@echo "示例：make run PORT=8080 OUTPUT=D:\\quotes-data"
	@echo "线路配置已内置，无需 CONFIG 或 connect.cfg"

build: ## 编译可执行文件
ifeq ($(OS),Windows_NT)
	@if not exist bin mkdir bin
else
	@mkdir -p bin
endif
	"$(GO)" build -o "$(BIN)" ./cmd/qflux

run: build ## 编译并运行
	"$(BIN)" --host "$(HOST)" --port "$(PORT)" --output "$(OUTPUT)"

dev: ## 不生成二进制，直接开发运行
	"$(GO)" run ./cmd/qflux --host "$(HOST)" --port "$(PORT)" --output "$(OUTPUT)"

test: ## 运行测试
	"$(GO)" test -v ./...

cover: ## 测试覆盖率
	"$(GO)" test -coverprofile=coverage.out ./...
	"$(GO)" tool cover -func=coverage.out

vet: ## 静态检查
	"$(GO)" vet ./...

fmt: ## 格式化代码
	"$(GO)" fmt ./...

check: test vet build ## 完整检查

smoke: build ## 页面与 HTTP 冒烟测试
	"$(PYTHON)" scripts/smoke_preview.py

tidy: ## 整理依赖
	"$(GO)" mod tidy

clean: ## 清理生成文件，不删除 downloads
ifeq ($(OS),Windows_NT)
	@if exist bin rmdir /s /q bin
	@if exist artifacts rmdir /s /q artifacts
	@if exist coverage.out del /q coverage.out
else
	rm -rf bin artifacts coverage.out
endif
