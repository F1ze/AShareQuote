# QFLUX · A 股研究数据平台

从沪深北行情采集、历史补录到 DuckDB 研究仓库。原始价长期保留，增量数据按主键合并，任务可以中断恢复，历史修订和来源有记录。

## 开始使用

需要 Go 1.24+、Node.js 20+ 和 C/C++ 编译器。DuckDB 由 Go 通过 GORM 操作，底层使用官方 `database/sql` 驱动，编译时启用 CGO；只有使用 BaoStock 数据源时才需要 Python 3.13（已验证版本）。在本目录执行：

```powershell
npm run setup:native
npm run frontend:install
# 可选：使用 BaoStock 时安装 SDK；通达信、本地导入和 DuckDB 均无需 Python
npm run setup:data
npm run dev
```

打开 <http://127.0.0.1:7100>。默认 CSV 在 `downloads/`，数据库在 `data/quotes.duckdb`，任务日志在 `data/jobs/`。`setup:data` 仅安装到项目 `.venv`，不修改系统 Python。

`setup:native` 优先复用已有编译器；Windows x64 缺少 GCC 时下载固定版本并核验 SHA-256，安装在忽略提交的 `.tools/`。Linux 安装 `build-essential`，macOS 安装 Xcode Command Line Tools。`scripts/go.mjs` 只为当前构建启用 CGO 并选择编译器，不修改系统 PATH 或全局 Go 设置。Windows 产物静态链接 DuckDB 和 GCC 运行库，运行时无需编译器、Python DuckDB 包或额外 DuckDB DLL。

驱动固定为 `github.com/duckdb/duckdb-go/v2 v2.5.4`，内嵌 DuckDB 1.4.3，与既有数据库保持一致。GORM 固定为 `v1.31.2`，负责模型映射、批量 upsert、查询构建和整批事务；SQL、迁移和 Parquet 导出全部位于 Go Repository。DuckDB 方言使用 GORM 原生回调，保留复合主键及冲突更新语义，不额外引入另一套数据库驱动。

建议依次建立数据：

1. 选择 **BaoStock、沪深、日线、历史建库、DuckDB**，采集来源提供的历史证券（包括退市证券），同时同步上市/退市资料、交易日历和独立复权因子。
2. 选择 **通达信、北京、日线** 补充北交所。通达信还支持 1 分钟；历史保留长度由行情服务器决定。
3. 此后使用 **增量维护**。系统保留旧历史，并回看最近 7 个自然日，更新近期记录。
4. 在 **数据仓库** 查看实际覆盖范围、查询数据集、补录历史文件或导出 Parquet 研究快照。

上述步骤是数据构建流程，不代表免费源已证明覆盖所有历史 A 股。退市遗漏、代码迁移、停牌信息和分钟历史边界仍需要核验；详见 [数据约定与边界](docs/data-contract.md)。

## 主要能力

| 能力 | 行为 |
| --- | --- |
| A 股范围 | 按交易所筛选 A 股代码，排除指数、ETF、债券、B 股及北交所 889 申购代码 |
| 多数据源 | 通达信并发采集；BaoStock 官方 SDK 顺序采集；本地 `.day/.lc1/.lc5/CSV` 补录 |
| 日期范围 | 包含起止当天，按北京时间解释；通达信自动分页、排序、去重 |
| 增量更新 | 合并旧历史，重叠记录以新数据为准，失败不写空文件 |
| DuckDB | 主键幂等入库，价格/成交额使用 Decimal，成交量统一为股 |
| 审计 | 来源、请求范围、文件哈希、入库回执、旧行情修订和 schema 版本 |
| 恢复 | 持久证券计划和逐文件断点；验证原始文件及因子附件哈希后复用 |
| 参考数据 | 上市/退市名录、交易日历、独立因子表；缺口只作为候选提示 |
| 研究导出 | 同一事务中的行情、名录、日历、因子、回执、修订 Parquet 快照及 manifest |

## 无界面运行与计划任务

```powershell
npm run backend:build

# 沪深历史建库，含来源提供的退市证券、日历、独立因子
.\bin\quotes.exe --sync --source baostock --full --from 1990-12-19

# 默认沪深北通达信增量日线；结束日默认北京时间昨日
.\bin\quotes.exe --sync

# 单只证券与周期
.\bin\quotes.exe --sync --source baostock --codes sh600000 --from 2020-01-01
.\bin\quotes.exe --sync --category 7 --codes sh600000 --from 2026-10-01

# 恢复任务原有计划，已经验证的文件不重复下载
.\bin\quotes.exe --resume <任务编号>
```

Linux/macOS 可以通过 `node scripts/go.mjs run --sync ...` 使用相同参数。批处理可交给系统计划任务调用；GUI 服务持有同一数据目录时，使用其 REST API 启动任务，或先关闭 GUI 服务。操作系统锁会拒绝第二个写者，并在进程退出或崩溃后自动释放。

`--data-dir` / `QFLUX_DATA_DIR` 设置数据库和日志目录；`--output` / `QUOTES_OUTPUT` 设置归档目录；`QFLUX_PYTHON` 仅为 BaoStock SDK 指定现有 Python 环境。完整参数见 `bin/quotes.exe --help`。

## 在自己的研究代码中读取

DuckDB 数据文件由应用进程单写。应用运行时推荐导出 Parquet 快照，在研究进程中读取；应用关闭后可直接只读打开数据库。

Python 研究示例为可选客户端，单独安装其依赖；它不参与后端入库。也可在自己的 Go 程序中使用相同 DuckDB 驱动。

```powershell
.venv\Scripts\python.exe -m pip install duckdb==1.4.3
.venv\Scripts\python.exe examples/research.py --database data/quotes.duckdb --symbol sh600000
.venv\Scripts\python.exe examples/research.py --snapshot data/exports/snapshot-... --symbol sh600000
```

完整示例在 [examples/research.py](examples/research.py)。Parquet 快照也便于后续迁移 ClickHouse；本版本的数据库实现是 DuckDB，存储端口已独立，详见 [架构说明](docs/architecture.md)。

## 后端代码导航

后端按 `handler → service → domain` 组织，存储实现集中在 `repository`，协议、SDK 和进程能力集中在 `platform`。依赖由 `cmd/qflux` 组装，Service 不导入具体存储或平台实现。目录、调用流程及每类修改的阅读入口见 [后端结构](docs/architecture.md)，分层边界由自动测试约束。

## 验证

```powershell
npm test
node scripts/go.mjs vet ./...
npm run test:data
npm run frontend:lint
npm run build
python scripts/smoke_preview.py

# 额外验证真实通达信采集、DuckDB 入库与预览
$env:QFLUX_SMOKE_DOWNLOAD = '1'
python scripts/smoke_preview.py
```

`npm test` 包含真实 DuckDB 引擎测试，无需 Python DuckDB；`test:data` 额外运行 BaoStock SDK 保护测试。

CI 执行离线回归和生产构建，真实公网检查单独运行，避免外部源波动阻断每次提交。数据库、下载数据、虚拟环境和测试截图均不提交 Git。
