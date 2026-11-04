# 数据约定与覆盖边界

## 时间、价格与单位

| 层 | 价格 | 成交量 | 成交额 | 时间 |
| --- | --- | --- | --- | --- |
| 标准 CSV | 未复权元 | 手，1 手 = 100 股，可含小数 | 元 | 北京时间；日线为日期，分钟线含时分 |
| DuckDB / Parquet | `DECIMAL(18,6)` 未复权元 | `BIGINT volume_shares` 股 | `DECIMAL(24,4) amount_cny` 元 | `ts TIMESTAMP` 表示北京时间，不随宿主时区变化 |
| 采集与审计时刻 | — | — | — | `TIMESTAMPTZ`，按 UTC 保存 |

通达信网络股票 K 线使用手；本地股票 `.day/.lc1/.lc5` 使用股；BaoStock 的 `volume` 使用股。适配器先转到标准归档单位，入库时统一为股。外部 CSV 补录必须明确选择单位，系统不会猜测。

协议和本地二进制的量额精度受上游编码限制，Decimal 不会恢复源数据已经丢失的精度。原始行情不通过零填充或插值制造缺失记录。

## 表

- `bars`：主键 `(exchange, code, period, ts)`；原始 OHLC、股数、成交额、采集来源、入库时刻。
- `daily_bars`：日线研究视图，包含 `trade_date`。
- `instruments`：名称、上市/退市日期、状态、参考来源、首次与最近观察时刻。来源未提供的日期保持 NULL。
- `trading_calendar`：市场、日期、开市标记及参考来源。
- `adjustment_events`：通达信 gbbq 事件，不与其他供应商的比例因子混用。
- `provider_factors`：BaoStock 原始前/后复权和事件因子，独立于价格，保存来源与获取/入库时刻。
- `ingestion_receipts`：文件哈希、请求及实际范围、警告、来源和任务编号。
- `bar_revisions`：被新值替换的旧行情及替换时刻、来源和任务编号。
- `schema_migrations`：数据库结构版本。

API 的预览是有限窗口，不代表文件只保存了这些条数。批量研究请读取 DuckDB 或一致性 Parquet 快照。

## 来源与支持范围

| 来源 | 适用场景 | 重要边界 |
| --- | --- | --- |
| 通达信在线 | 沪深北当前码表，日线和分钟线，含 1 分钟 | 单次最多 800 条；可分页但历史保留有限；当前码表不是历史全体证券 |
| BaoStock 官方 SDK | 沪深历史证券名录、来源提供的退市证券、日历、日/周/月及 5/15/30/60 分钟 | 当前适配器不提供北交所或 1 分钟；公网服务可能断连；来源可得不等于完整已证实 |
| 本地历史文件 | `.day/.lc1/.lc5` 或规范 CSV | 只证明文件包含的记录；来源、覆盖和单位仍需确认 |

退市样本以源提供的证券名录为准。仅按“目前上市”过滤会产生幸存者偏差。历史代码更名、代码复用、北交所旧码到 920 的公司身份映射，需要额外可信参考数据；不要仅凭相似代码自动拼成同一家公司。

## 质量和完整性的区别

通过 OHLC、数值、主键和时间校验，只说明已存记录满足基本质量约束。

“完整历史”还需要核验历史证券全体、上市/退市边界、完整交易日历、停复牌记录，以及供应商自身保留和修订情况。因此本版本整体完整性保持 `unverified`。

候选缺口检查依赖已导入的上市日期和交易日历。某个开市日没有 K 线也可能是停牌，不应自动补零或当作系统丢数。没有参考资料时查不到缺口，不代表完整。

增量模式回看 7 个自然日，覆盖近期常见修订；更早的修订和历史空洞需要定期运行“历史建库”重新扫描对应范围。增量更新不会自动证明过去没有缺失。

本系统记录“本次什么时候观察/导入了数据”，不等同于完整历史 point-in-time 数据库。当前名称、ST 状态、财务公告、成分股、流通股本和公司身份变迁的历史快照，不由现有行情表推断。因子也不能作为当时已经公开的事件时间来使用。

## 补录 CSV

```csv
date,open,high,low,close,volume,amount
2024-01-02,10,11,9,10.5,100,105000
```

文件名例如 `sh600000_day.csv`、`sz000001_5m.csv`、`bj920001_1m.csv`。UTF-8/UTF-8 BOM 均支持；分钟时间格式为 `YYYY-MM-DD HH:MM`。目录可递归扫描，符号链接和非匹配文件跳过。

证券参考模板为 `exchange,code,name,listing_date,delisting_date,status`，交易日历模板为 `exchange,date,is_open`。参考导入必须填写来源，单批失败整批回滚。页面可以下载模板。

## 研究快照与备份

Parquet 导出包含全部研究与审计表，附带 `manifest.json`，记录 schema、时间、单位和行数。所有表在同一数据库事务中导出，临时目录完成后再发布。

DuckDB 文件是单进程写入模型。应用运行时，用导出快照进行多进程研究；关闭应用后可以直接只读打开数据库。备份建议包含 `data/` 与归档目录；不要只保存 `bars.parquet` 而遗漏名录、因子、修订和任务日志。

## 一手资料

- [BaoStock 官方基本资料接口](https://www.baostock.com/mainContent?file=stockBasic.md) 与 [官方 SDK 发行](https://pypi.org/project/baostock/)。
- [pytdx 的 K 线参数](https://rainx.gitbooks.io/pytdx/content/pytdx_hq.html)、[日线读取实现](https://github.com/rainx/pytdx/blob/master/pytdx/reader/daily_bar_reader.py)、[分钟线读取实现](https://github.com/rainx/pytdx/blob/master/pytdx/reader/lc_min_bar_reader.py)。
- [DuckDB 并发模型](https://duckdb.org/docs/stable/connect/concurrency)。

下载数据的授权范围以相应来源为准；本仓库只提交程序与测试，不分发采集数据。
