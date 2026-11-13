# QFLUX 项目协作指南

## 1. 项目目标

QFLUX 是一个高性能通达信行情下载工具，使用 Go 协程池并发拉取 K 线，并通过网页完成配置、监控和行情预览。

核心数据原则：

- 原始 OHLCV 行情始终保存为未复权 CSV。
- 复权因子单独保存为 `*_gbbq.csv`。
- 前复权、后复权结果在预览时实时计算，不改写原始行情文件。
- 内置线路配置自动完成生成、回读校验和 TCP 连通性检测（仅建连后关闭，不发送行情握手或业务请求），不依赖用户提供的 `connect.cfg`。
- 下载界面以开始日期、结束日期表达数据范围（北京时间，包含起止当天），不要要求普通用户填写条数或偏移。
- `startDate` / `endDate` 模式自动分页，每次最多请求 800 条，按时间去重、排序后写入一个 CSV。旧的 `start` / `count` API 保留并支持分页。
- 通达信指定代码和实时看盘支持 A 股、ETF 和可转债；按市场采集仍默认只选 A 股。
- 历史偏移是 16 位无符号数，不得溢出回绕；达到边界、行情源耗尽或重复分页时必须提示，并展示文件实际覆盖日期，不能承诺无限历史。
- 统一按北京时间解释行情时间；CSV 成交量按每 100 个证券单位归档（允许小数），DuckDB/Parquet 的 `volume_shares` 保存股、份或张。不得根据数值大小猜单位。
- DuckDB 入库和文件归档分属不同事务边界；先保存原始归档，再幂等入库，最后写持久断点。不得将部分成功称为恰好一次。
- BaoStock 因子与通达信 gbbq 事件独立存储，不修改原始价，不混用不同来源的复权语义。
- 历史完整性、历史证券全体和 point-in-time 状态不能由当前名单或已存条数推断。参考资料不足时保持 unverified。

## 2. 技术栈与端口

### 后端

- Go 1.24+，GORM + 原生 DuckDB Go 驱动（CGO + C/C++ 编译器）
- Echo v4
- 标准库 `net/http` 服务生命周期管理
- REST + SSE

### 前端

- Node.js 20+
- React + TypeScript
- Vite
- Tailwind CSS
- shadcn/ui、Lucide、Recharts

### 默认地址

- React 开发服务器：`http://127.0.0.1:7100`
- Echo API：`http://127.0.0.1:7101/api`
- Vite 将 `/api` 代理到 Echo；独立部署时可通过 `QFLUX_API_TARGET` 修改代理目标。

## 3. 目录职责

```text
cmd/qflux/              依赖组装根、命令行和应用生命周期
internal/handler/       HTTP 解析、响应序列化、状态码与 SSE
internal/service/       任务、恢复、研究查询、预览等用例及消费端口
internal/domain/        业务实体、约束、日期/代码/单位规则与纯复权算法
internal/repository/    CSV、因子、JSONL 任务日志、DuckDB 及查询实现
internal/platform/      协议、SDK、进程、系统等技术适配器（不是业务用例）
internal/architecture_test.go  自动检查真实的分层依赖方向
frontend/               独立 React 工程
frontend/src/lib/       API 客户端与通用函数
frontend/src/hooks/     React 状态订阅逻辑
frontend/src/types/     与后端响应对应的 TypeScript 类型
frontend/src/components/业务组件
scripts/                构建辅助和浏览器冒烟脚本
original/               原始 Python 参考资料，非 Go 运行依赖
```

## 4. 常用命令

在 `src` 目录执行：

```bash
# 首次配置项目本地原生编译器（或复用现有 GCC/Clang）
npm run setup:native

# 自动启动 Echo 和 React，前端支持 HMR
npm run dev

# 可选 BaoStock SDK 依赖；DuckDB 测试直接运行 Go 内嵌引擎
npm run setup:data
npm run test:data

# 等价的一键 Make 入口
make run

# 构建前端 dist 和 Go 可执行文件
npm run build
make build

# 仅启动 Echo API
node scripts/go.mjs run --api-only
make api

# 单独启动 Vite
npm run frontend:dev
make frontend-dev

# Go 测试与静态检查
npm test
node scripts/go.mjs vet ./...

# 前端检查与构建
npm run frontend:lint
npm run frontend:build

# 完整浏览器冒烟测试
make smoke
```

可覆盖参数：

```bash
node scripts/go.mjs run \
  --host 127.0.0.1 \
  --port 7100 \
  --api-port 7101 \
  --output downloads
```

首次启动缺少前端依赖时会自动执行 `npm ci`（存在锁文件）或 `npm install`。不要为了启动页面重新引入 `embedFS`。

## 5. 后端约束

- 保持依赖方向向内：`domain` 不依赖协议、文件系统、数据库或 HTTP 层。
- Handler 只处理传输；Service 协调用例；Domain 定义实体与约束；Repository 实现持久化/查询；Platform 对接协议与系统。
- Service 的外部端口在消费端定义，由 cmd 组装注入；禁止 Service 导入 repository/platform、直接读写文件、编码 JSON 或拼 SQL。
- 数据库访问统一经 GORM；ORM 标签与持久化模型只放在 Repository，不污染 Domain/Service。
- DuckDB 使用官方 Go 原生驱动，不得通过 Python 子进程操作；批量 CSV 加载、修订合并及 Parquet 导出可通过 GORM 执行专用 SQL。
- 数据库结构使用显式版本迁移，通过 GORM Migrator 根据模型创建缺失对象，不以 AutoMigrate 自动改写历史库的 Decimal 精度或复合主键；仓库操作须保留整批事务与 context 取消。
- Repository 不决定证券采集范围或任务成败，Handler 不读取行情 CSV、不组织复权、不拼 Python 工作进程命令。
- 按职责在现有包内分文件，禁止每增加一个用例就增加一个 internal 顶层目录。
- 通过 RegisterSource(SourceSpec) 注册新的历史来源，避免向用例添加供应商名称分支；本地格式通过解码器注册表扩展。
- `handler` 只负责传输语义，不实现通达信协议或下载业务。
- 所有出站连接（含裸 TCP 与出站 HTTP）统一经 `internal/platform/netdial` 的全局拨号器：协议客户端通过 SetDialer 注入，HTTP 侧使用其 HTTPTransport（`Proxy` 保持 nil，避免双重代理）。禁止新的 `net.Dialer` 直连、`http.DefaultClient` 或散落的自建 Transport。
- 出站代理设置持久化在数据目录 `settings.json`，经 `/api/proxy` 读写；初始值按 `ALL_PROXY` → `HTTPS_PROXY` → `HTTP_PROXY` 环境变量约定读取一次，不得引入私有命名的代理变量。子进程只注入标准代理变量，不与 Go 侧当前状态分叉。
- 下载失败必须关闭受影响的连接并换线重试，不能复用可能错位的报文流。
- CSV 使用临时文件写入，成功后再原子替换目标文件。
- 无行情、任意分页失败或取消时不得用空文件或半成品覆盖已有 CSV。
- JSON 市场列表使用 `[]int`，不要使用会被 Go 编码为 Base64 字符串的 `[]uint8`。
- 预览接口只接受输出目录内的合法 CSV 文件名，拒绝路径穿越和 `*_gbbq.csv`。
- 全量预览使用 CSV 的 `all=true` 或仓库的排他 `before` 时间游标；保留旧 limit 查询兼容。复权必须先处理完整序列，再选择可视范围，不能因日期或框选改变基准。
- 图表允许概览采样，但明细必须保留所有记录并分页；起止日期包含结束日全部分钟记录，关闭预览应取消正在加载的请求。
- 网页路径选择只能调用 `/api/fs/*`，不得调用 PowerShell、Windows Forms 或其他系统原生选择框。
- 不恢复 `go:embed`、`embed.FS` 或由 Echo 托管 React 静态资源的单体方式。

## 6. 前端约束

- 所有 API 请求集中在 `frontend/src/lib/api.ts`，不要在组件中散落裸 `fetch`。
- 与后端共享语义的响应类型放在 `frontend/src/types/`。
- 页面必须实现加载、空数据、失败和禁用状态，不能留下无响应按钮。
- 动态面板支持键盘操作和清晰焦点；不依赖 hover 才能完成操作。
- 保持中文可读性、等宽数字、紧凑数据表格和移动端无横向页面滚动。
- 不将 Node.js 文件能力直接暴露给浏览器；本地文件操作必须经过受约束的 Echo API。
- 生产构建输出为 `frontend/dist/`，由独立静态服务器或反向代理托管。

## 7. 修改后的验证要求

后端修改：

```bash
gofmt -w <修改的 Go 文件>
npm test
node scripts/go.mjs vet ./...
```

数据适配器、数据库结构、因子或 Python 进程修改还需要执行 `npm run test:data`。

前端修改：

```bash
npm run frontend:lint
npm run frontend:build
```

页面、进程启动或 API 联调修改：

```bash
npm run backend:build
python scripts/smoke_preview.py
```

需要验证真实下载和行情预览时：

```bash
QFLUX_SMOKE_DOWNLOAD=1 python scripts/smoke_preview.py
```

浏览器检查至少覆盖：

- React 页面正常渲染。
- 目录选择、范围切换和表单校验可用。
- 控制台无错误，HTTP 请求无 404。
- 桌面与手机排版正常，手机页面无横向溢出。
- 验证结束后关闭 Go、Vite 和 Chrome，不遗留后台进程。

`artifacts/`、`bin/`、`frontend/dist/`、`downloads/`、`data/`、`.tools/` 和 `.venv/` 都是本地产物，不应提交。

## 8. Git 约定

- Git 仓库根目录是 `src`，不要在上级目录重复初始化仓库。
- 不要自动 `git add`、`git commit` 或 `git push`；仅在用户当前明确要求提交时执行，不沿用先前任务的提交授权。
- 提交前运行 `git diff --check`，并确认构建产物和下载数据未进入暂存区。
- 用户要求“仅本次提交”时，不形成后续自动提交习惯。
