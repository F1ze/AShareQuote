NPM ?= npm
PYTHON ?= python
NODE ?= node
HOST ?= 127.0.0.1
PORT ?= 7100
API_PORT ?= 7101
OUTPUT ?= downloads

BIN := bin/quotes
ifeq ($(OS),Windows_NT)
BIN := bin/quotes.exe
endif

.PHONY: help setup setup-native setup-data test-data sync build backend-build run dev api frontend-install frontend-dev frontend-build frontend-lint test cover vet fmt check smoke tidy clean

help: ## 显示所有可用命令
	@echo "QFLUX 行情下载台（Echo + React）"
	@echo ""
	@echo "  make setup            安装原生编译器、前端和可选 SDK 依赖"
	@echo "  make test-data        运行 DuckDB 事务和 SDK 保护测试"
	@echo "  make sync             执行一次全 A 股增量入库"
	@echo "  make run              编译 Go，并自动启动前后端"
	@echo "  make dev              go run 启动前后端（前端支持 HMR）"
	@echo "  make build            构建前端 dist 和 Go 可执行文件"
	@echo "  make api              仅启动 Echo API"
	@echo "  make frontend-dev     单独启动 Vite 前端"
	@echo "  make frontend-build   构建前端生产资源"
	@echo "  make frontend-lint    检查前端代码"
	@echo "  make test             运行全部 Go 测试"
	@echo "  make cover            运行测试并显示覆盖率"
	@echo "  make vet              执行 Go 静态检查"
	@echo "  make fmt              格式化全部 Go 代码"
	@echo "  make check            依次执行 test、vet、lint、build"
	@echo "  make smoke            检查接口并生成页面截图"
	@echo "  make tidy             整理 Go 模块依赖"
	@echo "  make clean            清理编译与测试产物"
	@echo ""
	@echo "端口：前端 $(PORT)，Echo API $(API_PORT)"
	@echo "示例：make run PORT=8080 API_PORT=8081 OUTPUT=D:\\quotes-data"
	@echo "前端运行需要 Node.js 20+；首次启动会自动安装缺失依赖。"

setup: setup-native frontend-install setup-data ## 安装前端与隔离数据依赖

setup-native:
	"$(NPM)" run setup:native

setup-data:
	"$(NPM)" run setup:data

test-data:
	"$(NPM)" run test:data

sync: backend-build
	"$(BIN)" --sync --output "$(OUTPUT)"

frontend-install:
	cd frontend && "$(NPM)" ci

frontend-dev:
	"$(NPM)" --prefix frontend run dev

frontend-build:
	"$(NPM)" --prefix frontend run build

frontend-lint:
	"$(NPM)" --prefix frontend run lint

backend-build: ## 仅编译 Go 可执行文件
ifeq ($(OS),Windows_NT)
	@if not exist bin mkdir bin
else
	@mkdir -p bin
endif
	"$(NODE)" scripts/go.mjs build -o "$(BIN)" ./cmd/qflux

build: frontend-build backend-build ## 构建前端和 Go 可执行文件

run: backend-build ## 编译并自动启动前后端
	"$(BIN)" --host "$(HOST)" --port "$(PORT)" --api-port "$(API_PORT)" --output "$(OUTPUT)"

dev: ## 使用 go run 自动启动前后端
	"$(NODE)" scripts/go.mjs run --host "$(HOST)" --port "$(PORT)" --api-port "$(API_PORT)" --output "$(OUTPUT)"

api: ## 仅启动 Echo API
	"$(NODE)" scripts/go.mjs run --host "$(HOST)" --api-port "$(API_PORT)" --output "$(OUTPUT)" --api-only

test: ## 运行 Go 测试
	"$(NODE)" scripts/go.mjs test -v ./...

cover: ## 测试覆盖率
	"$(NODE)" scripts/go.mjs test -coverprofile=coverage.out ./...
	"$(NODE)" scripts/go.mjs tool cover -func=coverage.out

vet: ## 静态检查
	"$(NODE)" scripts/go.mjs vet ./...

fmt: ## 格式化代码
	"$(NODE)" scripts/go.mjs fmt ./...

check: test test-data vet frontend-lint build ## 完整检查

smoke: build ## 页面与 HTTP 冒烟测试
	"$(PYTHON)" scripts/smoke_preview.py

tidy: ## 整理依赖
	"$(NODE)" scripts/go.mjs mod tidy

clean: ## 清理生成文件，不删除 downloads 和前端依赖
ifeq ($(OS),Windows_NT)
	@if exist bin rmdir /s /q bin
	@if exist artifacts rmdir /s /q artifacts
	@if exist frontend\dist rmdir /s /q frontend\dist
	@if exist coverage.out del /q coverage.out
else
	rm -rf bin artifacts frontend/dist coverage.out
endif
