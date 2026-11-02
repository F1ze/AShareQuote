# TDX 工具链使用手册

本手册覆盖本仓库里我们自己写的全部脚本：常量含义、函数契约、命令行、数据格式、组合流程与排错。目标是让后来的人（或半年后的自己）不用重读代码就能正确使用、安全修改。

配套文档：TDX_接口逆向分析报告.md（协议与文件格式的实证、证据级别、章节 0–19）。

## 0. 读法

先看第 1 章的分层与调用关系，再看你要用的那个脚本那一章，最后看第 9 章的流程与第 11 章的排错。

命令都在仓库根执行。示例里用两个变量指代路径，粘贴前先按自己的机器设好：

| 变量 | 含义 |
|---|---|
| $tdx | 客户端安装目录，自填，例如 D:\neptune |
| $root | 数据根目录，本文示例用 data\tdx（相对仓库根） |

相对路径在脚本里一律先展开成当前目录下的绝对路径，报错与日志打印的都是绝对路径，便于核对。

文档里的层级标注：

- **只读**：不修改目标目录，可随时重跑。
- **写入**：会创建或覆盖文件，目录要事先确认。
- **联网**：访问 7709 主站，受连通性与频率影响。

## 1. 分层与调用关系

~~~
tdx_local.py      本地资产提取 + connect.cfg 解析/生成/校验       只读(仅输出)
tdx_client.py     7709 协议客户端 + 本地 .day/.lc1 读取 + 复权入口  联网+只读
tdx_adjust.py     gbbq 解密/加密、因子计算、复权应用               只读
tdx_factors.py    复权事件库(SQLite)，线上同步与 gbbq 回写         写入+联网
tdx_bootstrap.py  从零造目录树、填数、同步权息、校验               写入+联网
~~~

依赖方向（箭头表示 import）：tdx_bootstrap → tdx_local、tdx_client、tdx_adjust；tdx_factors → tdx_adjust、tdx_client；tdx_client → tdx_adjust（仅在 --adjust 时才 import，避免默认依赖）。

全部脚本只依赖标准库：argparse、base64、collections、configparser、csv、json、os、re、socket、sqlite3、struct、sys、threading、time、zlib、shutil、glob。没有 pandas、没有第三方包。

Python 解释器用环境变量里的 python（3.x，标准库足够，无需第三方包）。控制台输出是 UTF-8；connect.cfg 与本地 .cfg/.dat 是 GBK。

## 2. tdx_local.py —— 本地资产提取与 sitecfg

**只读**。默认根目录常量 ROOT 由脚本位置推导（仓库旁的 neptune），可用 TDX_ROOT 环境变量或 --root 覆盖。除 sitecfg-build 与 sitecfg-defaults 会写文件外，其余命令只打印或写 --out 指定的 CSV/JSON。

### 2.1 结构常量

| 名称 | 值 | 含义 |
|---|---|---|
| ROOT | 仓库旁的 neptune | 默认安装根，TDX_ROOT 或 --root 可覆盖 |
| hq(root) | <root>\T0002\hq_cache | 主表与缓存目录，base.dbf、gbbq、hqrule.dat 都在这里 |
| blk(root) | <root>\T0002\blocknew | 自定义板块 .blk |
| vip(root) | <root>\vipdoc | 行情文件根 |
| BLOCK_SYNTAX | 9 对 (语法, 文件名) | 板块文件的行格式表：hash_csv、hash_line7、hash_line、comma、csi |
| BASE_FIELDS | 40 个 (名, 类型, 长度, 小数位) | base.dbf 字段布局，顺序与长度照抄官方表 |
| SEED_HQ / SEED_TRY / SEED_INFO / SEED_WT / SEED_DS | (ip, port) 元组 | 内置站点种子，裸生成不依赖参照安装 |
| SEED_USERHOST | IPAddress、Port 7715 | 登录口默认值 |
| OPT_COLS | 12 列名 | 期权链字段顺序：id、code_full、name、underlying、strike_code、month、strike、qty、strike2、start、end、days |
| SITECFG_ORDER | 25 分区名（解压自 _CFG_B64） | 生成时的分区输出顺序 |
| SITECFG_DEFAULTS | 每分区的默认键值 | 缺省键补全来源，取自同版本参照安装 |
| HOST_SECTIONS | HQHOST、TRYHQHOST、INFOHOST、WTHOST、DSHOST | 5 个“编号站点”段，唯一允许带 NNN 后缀的段 |
| SINGLE_HOSTS | CHATHOST、ICQHOST | 单站点段，只有 Port 与 IPAddress |
| HOST_FIELDS | HostName、IPAddress、Port、Weight、Areas、HostType | 站点字段全集，HostType 仅 INFOHOST 使用 |
| OTHERHOST_KEYS | L2PrimaryHost、SSDSPrimaryHost、SSDS2/3、VipHQ/VipHQ2 | OTHERHOST 段的索引键，-1 表示无，不是地址 |

_CFG_B64 是压缩后 base64 的 JSON（4.6 KB，分片存放，单行不要手工改），解压后形如 {"order": [...], "sections": {...}}，在模块导入时就解析成上面两个常量。

### 2.2 字段解析约定

- to_f(s)：空或非数字返回 None，绝不抛异常。
- to_i(s)：先 to_f 再取整，同样返回 None。
- gbk(b)：GBK 解码，错误替换为 U+FFFD，不抛。
- read_dbf(path)：dBASE III 读取器，返回 (字段名列表, 行迭代器)；数值字段按 dec 位缩放，* 开头的删除行跳过。

### 2.3 命令

| 命令 | 关键参数 | 输出 | 退出码 |
|---|---|---|---|
| master | --out、--limit | base.dbf 全字段 CSV | 0 |
| industry | 同上 | tdxhy.cfg 行业/地域映射 | 0 |
| blocks | --out、--custom | 板块成分 JSON（--custom 走 blocknew/*.blk） | 0 |
| index | --out | tdxzs.cfg 板块指数 | 0 |
| futures | --out | code2name.ini 期货规格 | 0 |
| options | --out | ggqqcode/szqqcode 期权链 | 0 |
| snapshot | --out | tdxzsbase.cfg 横截面，raw 列折成 cols 计数 | 0 |
| rules | --out | hqrule.dat 键值对（加 --out 写文件） | 0 |
| files | --out | 只列 lday 文件名与大小 | 0 |
| bars | --market(sh/sz/bj/ds)、--code、--kind(lday/minline/fzline)、--out | 行情 CSV | 缺文件返回 2 |
| base | --out、--markets(默认 0,1)、--limit、--cfg | 生成 base.dbf | 0 |
| sitecfg-parse | --path、--hosts-out | 解析结果与站点表 | 缺文件 2 |
| sitecfg-check | --path | 错误/警告/结论 | 有错误 1，缺文件 2，通过 0 |
| sitecfg-build | --spec（必填）、--out | 生成并自检 | 0 |
| sitecfg-defaults | --ref、--out | 起始 spec JSON | 0 |

所有相对路径都按当前目录解析（含 --path、--out、--spec），因此在仓库根执行、写相对路径即可；安装目录本身由 ROOT 从仓库位置推导，不再写死盘符。

### 2.4 sitecfg 三个函数

- parse_sitecfg(path)：返回 {"sections": [...], "hosts": [...], "errors": [...], "warns": [...]}。hosts 每项含 section、index、name、ip、port、weight、areas、host_type。错误是阻断级，警告可自愈。
- build_sitecfg(spec)：由 spec 生成全文并立刻回读自检，出错抛异常且不落盘；强制编号连续、HostNum 与组数相等、段内不重复、空段清空编号。
- default_spec(ref)：以参照文件为底，产出一份可编辑的 spec（身份键 + 4 主站 + 5 试连 + 数据站 + 资讯/交易站），hosts 数组元素形如 {"ip": "...", "port": 7709, "weight": 90}。

base.dbf 三件套：BASE_FIELDS 定义布局，write_dbf 写 dBASE III（头 32 字节 + 每字段 32 字节描述 + 0x0D + 记录 + 0x1A，数值右对齐、字符左对齐），gen_base 只填 SC 与 GPDM、其余留 0 与空。seed_spec() 返回内置站点的最小可用 spec。

spec 顶层键：identity、hosts、try_hosts、info_hosts、wt_hosts、ds_hosts、single、otherhost、primary、sections。

## 3. tdx_client.py —— 7709 客户端

**联网 + 只读**（local 子命令完全离线）。默认读安装目录的 connect.cfg，只对 HQHOST/TRYHQHOST 段生效。

### 3.1 常量

| 名称 | 值 | 含义 / 单位 | 改动影响 |
|---|---|---|---|
| BASE_DIR | 脚本所在目录 | 相对路径基准 | — |
| CFG_DEFAULT | 默认根 + connect.cfg | 默认配置，TDX_CFG 可覆盖 | 指向自举树时会读那棵树 |
| VIPDOC_DEFAULT | 默认根 + vipdoc | 默认本地行情根 | 同上 |
| MARKET_DIR | {0: sz, 1: sh, 2: bj} | 市场号 → vipdoc 子目录 | 与 PREFIX 必须一致 |
| PREFIX | {0: sz, 1: sh, 2: bj} | 市场号 → 文件名前缀 | sz000001.day 这种命名 |
| CATEGORY | 0..12 → 5m/15m/30m/60m/day/week/month/1m/1m/day/quarter/year/xrday | K 线类别码 | 9 与 4 都是日线，9 是常用取值 |
| CT_BARS / CT_MINUTE / CT_TICK | 0x052D / 0x051D / 0x0FC5 | commtype | 服务端按它分派 |
| CAT_BARS / CAT_MINUTE / CAT_TICK | 0x01 / 0x1B / 0x17 | 帧头类别字节 | 与 commtype 成对，不能单改 |
| VER_BARS / VER_MINUTE / VER_TICK | 0x0164 / 0x0100 / 0x0101 | 帧头版本 | 同上 |
| MAX_INFLIGHT | 5 | 并发信号量 | 掉包就调小 |
| LOGIN_CT | 0x0004 | 登录命令号 | 服务端暂无该分支 |
| LOGIN_CAT / LOGIN_VER / LOGIN_HI | 0x02 / 0x0093 / 0x18 | 登录帧头三要素 | 与上一条成组 |
| 登录默认口 | 7615 | 客户端内部默认值 | 配置里的 7715 会覆盖；两个口本机实测都 TCP 可连、HTTP 打过去回 0 字节 |

### 3.2 编解码函数

- get_price(buf, pos) → (int, new_pos)：可变长带符号整数。bit6 是符号位，bit7 是续位，低 6 位起算、每次左移 7 位。返回的是**千分之一元**的整数，除 1000 得价格。
- get_volume(raw) → float：4 字节对数浮点（首字节是指数），用于成交量与成交额。不是普通 int。
- unpack_date(category, buf, pos) → ((年, 月, 日, 时, 分), new_pos)：日线类是 u32 的 YYYYMMDD，分钟类拆成打包日 + 分钟数。
- parse_quotes(body)：快照五档解析，仅 security_quotes 使用。
- login_probe(conn=None, ports=(7715,), cfg_path=None)：探测登录链路，返回 hosts / main / login_ports 三段。只读，不改配置；超时与断连都记。静态已知默认口是 7615、主机表按 {"Addr","Port"} JSON 序列化（0x9718A0 case 16），所以探测只判可达，不判协议通进 error，不当作成功。
- 实例属性 host/port：connect() 成功后记录实际连上的地址，供日志与探针使用。

### 3.3 TdxConnection

构造：TdxConnection(cfg_path=CFG_DEFAULT, timeout=8.0, verbose=False)。cfg_path=None 时 configparser.read(None) 会炸，调用方要给值。

| 方法 | 作用 | 返回 | 备注 |
|---|---|---|---|
| load_hosts() | 读 HQHOST/TRYHQHOST 全部 IP | [(ip, port)] 按权重降序 | 权重缺失记 0；无主机抛 TdxError |
| connect() | 逐台尝试连接并握手 | (host, port) | 全失败抛 TdxError；TCP_NODELAY 已开 |
| _close() | 关 socket 置 None | — | 幂等 |
| _request(payload, cat, ver, commtype, hi=0x08, flag5=0x01) | 组帧 + 三条重试 | 应答体 | 失败自动重连；commtype 不符最多丢 8 条推送 |
| _read_message() | 读 16 字节头 + 体 | 解压后的体 | zipsize≠unzipsize 时 zlib 解压；>2 MB 抛错 |
| _handshake() | Setup1/2/3 | — | 前两个必须读干净应答 |
| heartbeat() | 心跳帧 | — | 长连接保活 |
| security_count(market) | 证券总数 | int | cat 0x0C ver 0x016C commtype 0x044E |
| security_list(market, start, count=1000) | 码表分页 | [(market, code, name)] | 每记录 29 字节，名 GBK |
| xdxr_info(market, code) | 除权除息明细 | [事件字典] | commtype 0x000F；类别 1 才有 dividend/rights_price/bonus/rights |
| security_bars(category, market, code, start, count) | K 线 | [{dt, open, high, low, close, vol, amount}] | 单次 ≤800；价格 ÷1000；收盘是基准递推 |
| minute_time_data(market, code) | 当日分时 | [{price, vol}] | 记录自偏移 13 起，价格累加 |
| transaction_data(market, code, start, count) | 逐笔 | [{time, price, vol, num, buyorsell}] | buyorsell 0 买 1 卖 2 中性 |

重试语义：_request 内 3 次，每次失败先 _close() 再重连——实测该服务端每连接只服务一次业务请求，不重连就必然拿不到第二条。上层不需要再包重试。

### 3.4 本地读取与复权入口

- load_gbbq(path) → {(market, code): [事件]}：**未解密**的粗略读取，只用于计数展示；要真值用 tdx_adjust.load_events。
- read_day(path) / read_lc1(path)：32 字节记录，返回带 date/dt 与 OHLC 的字典；价格 ÷100。
- vipdoc_path(root, market, code, kind)：拼 <root>/<市场目录>/<kind>/<前缀><code>.<扩展名>。
- main()：CLI，见下表。

### 3.5 命令

| 命令 | 关键参数 | 说明 |
|---|---|---|
| smoke | --market --code | 证券数 + 前 5 笔逐笔，验证链路 |
| count | --market | 只要证券数 |
| codes | --market --start --count | 码表分页 |
| bars | --category --market --code --start --count --out | K 线，可叠加 --adjust |
| minute | --market --code | 当日分时 |
| tick | --market --code --start --count | 逐笔 |
| xdxr | --market --code | 除权除息明细 |
| local | --vipdoc --market --code --out | 读本地 .day，可叠加 --adjust |
| login | --cfg、--out | 探测 7709 的 0x0004 与 UserHost 登录口 TCP 可达性，写 JSON |

复权参数：--adjust none|hfq|qfq（默认 none），--gbbq 可指定文件，缺省按 <vipdoc 的上级>/T0002/hq_cache/gbbq 找。因子缺失时打印提示并按不复权输出，不静默。--adjust 只有 bars 与 local 生效。

输出：默认打印前 10 条 + 总数；--out 写 CSV（列含 date、open、high、low、close、vol、amount、mode）。

bars 与 local 加 --out 会额外写一份 CSV（列 date,open,high,low,close,amount,volume），便于核对复权。

### 3.6 注意

1. 单连接只能问一次，批量务必串行或复用 security_list。
2. 除权价单位是千分之一元，写库前必须 ÷1000，别直接当分。
3. 该主站对 0x053E 快照只收不回（疑 L2 门控），批量截面改走 BDHQ。
4. --count 超过 800 直接抛 ValueError。

## 4. tdx_adjust.py —— gbbq 解密与复权

**只读**。整个模块是纯函数 + 一个 CLI，没有全局状态。

### 4.1 常量

| 名称 | 值 | 含义 |
|---|---|---|
| CATEGORIES | 1..15 → 中文名 | 事件类别字典。只有 1 除权除息带比例 |
| PRICE_CATEGORY | 1 | 除权除息主类别，公式主体 |
| SHRINK_CATEGORIES | (11, 12) | 缩股/扩缩股，f3 是份额拆分比，价格除以它 |
| WARRANT_CATEGORIES | (13, 14) | 送认购/认沽权证，按内在价值折价（推断） |
| KEY_HEX | 4176 字节十六进制（分 14 段） | Feistel 密钥：0x00 起 k0、0x44 起 k44、0x48/0x448/0x848/0xC48 四张 256 项轮表、0x04..0x40 是 16 个轮常量 |

布局要点：每条记录 29 字节，前 24 字节加密（3 个 8 字节块），后 5 字节明文是日期 u32 + 类别 u8。头部 u32 条数，必须满足 4 + 条数×29 == 文件长度。

### 4.2 字段单位（务必记牢）

四个浮点都是**每 10 股**：

- f1 派息：10 派 5 元记 5.0
- f2 配股价：元/股，本身不再缩放
- f3 送转：10 送 4 记 4.0；类别 11/12 时 f3 是份额拆分比（2 表示 1 拆 2）
- f4 配股：10 配 3 记 3.0

类别 1 用 f1..f4 全部；11/12 只用 f3；13/14 用 f1 当行权价、f3 当每 10 股份数。类别 5 的浮点是绝对股本（如 19726.0156），拿来当送转会把因子算爆。字段位置是对 417 条 11/12 与 24 条 13/14 实测判出来的，别凭印象改。

### 4.3 函数

| 签名 | 作用 | 返回 | 备注 |
|---|---|---|---|
| _key() | 密钥字节 | bytes | 每次调用重建，勿缓存进全局 |
| iter_events(path) | 逐条解密 | (市场, 代码, 日期, 类别, f1..f4) | 长度自洽先验，不符抛 ValueError |
| _encrypt_record(plain, key) | 明文 29 字节 → 密文 | bytes | iter_events 的严格逆运算 |
| encode_event(market, code, date, cat, f1..f4) | 组一条密文记录 | 29 字节 | 尾部明文原样拼接 |
| write_gbbq(events, path) | 写整表 | 条数 | 按市场/代码/日期升序，头部写条数 |
| load_events(path, market=None, code=None) | 带过滤的列表版 | [事件字典] | 字典键：market/code/date/category/dividend/rights_price/bonus/rights |
| is_price_event(category) | 是否参与折算 | bool | 1 与 11/12/13/14 |
| ratio(event, prev_close) | 折算系数 | float 或 None | 1 除权除息、11/12 取 1/f3、13/14 按内在价值折价；前收无效返回 None |
| build_curve(events, closes, mode) | 复权曲线 | {日期: 复权价} | mode 为 qfq/hfq，其他原值返回 |
| adjust_records(recs, events, mode, date_key="dt") | 批量复权 | 新记录列表 | date_key 自动退到 date；价格保留 4 位 |
| check(path) | 结构自检 | 统计字典 | 问题超过 20 条截断 |

ratio 的公式与分母保护：

~~~
ratio = (前收 - f1/10 + f2*f4/10) / (前收 * (1 + f3/10 + f4/10))
~~~

分母 ≤0 或结果 ≤0 返回 None，调用方按“该事件不折算”处理，绝不拿 1.0 顶替。

### 4.4 命令

| 命令 | 参数 | 输出 |
|---|---|---|
| check | --gbbq | 条数/证券数/区间/类别分布/问题/结论 |
| dump | --gbbq --out --category --since | CSV：market,code,date,category,name,dividend_per10,rights_price,bonus_per10,rights_per10 |
| factors | --gbbq --market --code --closes-csv --mode | 逐事件前收与系数，有前收时再打印整段曲线 |
| apply | --gbbq --csv --market --code --mode --out | 复权后 CSV，多一列 mode |

退出码：check 有问题返回 1，其余 0。

### 4.5 验证过的事实

- 往返：原文件前 200 条编码后与原始字节 200/200 相同。
- 缩股折算实测：588810 于 20260909 有 11 类事件 f3=2，原始跳空 50.25%，后复权后最大跳空降到 14.40%；与因子库同日系数逐值一致。
- 方向覆盖：417 条 11/12 事件里 f3 最小 0.0013、最大 441.64，缩股与扩股都走过。
- check 在现有 gbbq 上通过：193719 条、6331 只、1990-03-01 至 2026-10-28、0 问题。
- 001207 复权后最大单日跳空由 27.06% 降到 8.91%，除权日前后连续。

### 4.6 注意

1. 除权日是除权除息日，前收要取**前一交易日**，不是登记日。
2. 11/12 与 13/14 已参与折算；13/14 的折价公式是推断（无历史行情可对照），别当权威。
3. gbbq 只含已发生事件，不能当未来除权日历。
4. 复权价不可与原始价混用于市值、成交额、每股指标。

## 5. tdx_factors.py —— 复权事件库

**写入 + 联网**。库文件是普通 SQLite，任意路径。

### 5.1 表结构

~~~
events(market, code, date, category, dividend, rights_price, bonus, rights, shrink, warrant_price, warrant_ratio, src,
       PRIMARY KEY(market, code, date, category))
factor(market, code, date, cum_qfq, cum_hfq, basis, PRIMARY KEY(market, code, date))
meta(key, value)
~~~

- events 只增不删，同键 ON CONFLICT 覆盖；src 记来源（server 或 gbbq）。shrink 存 11/12 的 f3，warrant_price/warrant_ratio 存 13/14 的 f1 与 f3。
- done(market, code, at, events) 记断点：每只成功即写一行，sync 默认跳过。
- factor 每次 build 全量删了重建。
- basis 是**逐行**标注：ratio 表示用了真实前收，share 表示只有股本比例（派息部分缺失，复权会偏）。看统计时的 ratio_basis 字段就知道有多少行可信。

### 5.2 函数

| 签名 | 作用 |
|---|---|
| open_db(path) | 建库建表，新库写 created 时间 |
| upsert(db, events, src) | 批量写入并提交，返回条数 |
| import_gbbq(db, path, market=None) | 解密整表导入，src=gbbq |
| _closes(vipdoc, market, code) | 从 .day 取 {日期: 收盘}，文件缺失返回空表 |
| build_factors(db, vipdoc=None) | 重算全部因子；有前收走 ratio，否则退化 share |
| stats(db) | events/priced/codes/factors/ratio_basis/first/last |
| export_csv(db, out) | 事件导出，带中文类别名 |
| to_gbbq(db, out) | 由库回写合法 gbbq |
| done_set(db, market) | 已完成代码集合 |
| sync(db, market, codes, cfg, limit, redo=False) | 逐只抓 xdxr 入库，跳过已完成，返回本轮成功只数 |

build_factors 的前收查找是“日期严格小于事件日、且在库中最近的收盘”，一处找不到就整条退化，不会跨股票借价。

### 5.3 命令

| 命令 | 参数 | 说明 |
|---|---|---|
| sync | --db --market --codes/--limit --cfg、--redo | 不给 --codes 就整市场遍历码表；--redo 忽略断点 |
| import | --db --gbbq --market | 吃现成 gbbq |
| build | --db --vipdoc --togbbq | 重算因子，可选回写 |
| stats | --db | 看规模与 ratio_basis |
| export | --db --out | 事件 CSV |
| gbbq | --db --out | 只回写 gbbq |

### 5.4 注意

1. build 会清空 factor，别在有下游的情况下并发跑。
2. 没有 vipdoc 时空有 factor 行但全是 share，复权会偏——先有行情再建因子。
3. 主键会吃掉完全重复记录（好事），导入条数可能比源少几条。
4. sync 是串行逐只，6331 只约十几分钟，中途失败只计数不中断。

## 6. tdx_bootstrap.py —— 从零造树

**写入 + 联网**。init 拒绝非空目录，避免覆盖既有库。

### 6.1 常量

| 名称 | 内容 | 作用 |
|---|---|---|
| T0002_DIRS | 51 个目录名 | T0002 下的固定骨架（连 vipdoc 的 25 个共 76 个目录）（缓存、cloud_*、wt_*、User、log 等） |
| MARKETS | bj、sh、sz、ds、ot、cw | vipdoc 一级市场目录 |
| SUB_DIRS | lday、minline、fzline、eday | 每市场的周期子目录 |
| REF_GLOBS | exe/dll/dax/daq/dai/xmb/ini/dat/dbf/cfg/json/txt/html/css/chm/bmp/jpg/ql/up | 从 --ref 复制的根文件白名单 |
| REF_SKIP | connect.cfg、base.dbf | 不复制配置与主表，必须由生成器现造 |
| REF_DIRS | chrome、files、funcs、GNPlugins、jspages、QHData、SEPlugins、TCloudCalc、TCPlugins、webs、xiadan、ZDPlugins | 递归复制的程序目录，chrome 是 CEF 运行时，缺了不建窗 |

### 6.2 函数

| 签名 | 作用 | 返回 |
|---|---|---|
| _dirs(root) | 待建目录全表 | 绝对路径列表 |
| init(root, spec_path, ref) | 建目录、生成 connect.cfg、写空 gbbq、复制参照件、写 bootstrap.json | 清单字典 |
| gen_base(root, markets, limit, cfg) | 抓码表生成 base.dbf | (条数, 路径) |
| probe_login(root, cfg) | 探测登录链路写 login.json | (结果, 路径) |
| regen(root, spec_path, ref) | 重造 connect.cfg 并刷新清单哈希 | 配置路径 |
| fill(root, market, codes, days, cfg) | 抓日线写成 .day | [(code, 条数)] |
| sync_gbbq(root, market, codes, limit, cfg, redo) | 抓权息写 gbbq，断点续传、按主键去重 | {records, added, symbols, failed, codes, skipped} |
| verify(root) | 五项校验 | (问题列表, 统计字典) |

verify 检查：目录齐全、connect.cfg 可解析且有主站、gbbq 长度与条数自洽、bootstrap.json 存在、清单里的配置哈希与现文件相符。任一不过返回问题列表，CLI 退出码 1。

bootstrap.json 记 build_at、spec 与 cfg 的 sha256、目录数、复制清单；它同时是 verify 的比对基准，手改 connect.cfg 会让哈希校验失败。

fill 与 sync_gbbq 都从目标树自己的 connect.cfg 取站点（--cfg 可覆盖），所以树必须先 init。

### 6.3 命令

| 命令 | 参数 | 退出码 |
|---|---|---|
| plan | root | 0 |
| init | root --spec --ref | 0 |
| fill | root --market --codes --days --cfg、--no-merge | 0 |
| sync | root --market --codes --limit --cfg、--redo | 0 |
| base | root --markets --limit --cfg | 0 |
| login | root --cfg | 0 |
| regen | root --spec --ref | 0 |
| verify | root | 通过 0，有问题 1 |

fill 默认合并写：按日期并进已有 .day，同日以新抓为准，旧历史保留，用来一天天攒长历史；只有 --no-merge 才整文件覆盖。

### 6.4 实测规模

init 产 76 目录、142 静态件、合计约 3836 文件（chrome 占 70）；不带 --ref 的裸 init 也能过（76 目录、4 站点、0 复制），只是没 exe。

### 6.5 注意

1. 判据看**窗口**不看进程存活：缺 chrome 时进程活着、日志只有版本号、永不建窗（18.3 有完整排除过程）。
2. --ref 里 T0002 只复制 2 MB 以内的状态文件，大 zip 与子目录内容不复制。
3. verify 对空 gbbq 只查长度自洽，不要求有内容。
4. 程序层（exe/DLL/chrome）无法自造，仍依赖一份安装来源。

## 7. 小工具

全部收在 re-tools\ 下，与建库主链隔离，说明见 re-tools\README.md。

| 文件 | 作用 |
|---|---|
| re-tools\ida\ida_shim.py | 打开样本、等自动分析完，注册 seginfo/dis1/decompile 等 RPC，监听 13337 |
| re-tools\ida\shim2.py | 同上但端口与样本从参数取，多注册 decompile_va |
| re-tools\ida\q.py | 打 13337：str 搜字符串、disr 连续反汇编 |
| re-tools\ida\q2.py | 打 13338：raw 通用调用、imp 搜导入、fs 查字符串、d 反编译、dis 反汇编、x 交叉引用、f 所在函数 |
| re-tools\ida\rpc.py | 一次性 JSON-RPC 调用，默认 get_metadata |
| re-tools\ida\mkall.py | 触发 0x401000–0xcf1000 全段函数创建 |

这些只在静态分析阶段用，与建库无关。另有 re-tools\probes\ 的 30 个一次性探针、re-tools\dumps\strings.txt 全量字符串、re-tools\blobs\ 探针参数与结果。

## 8. 数据格式附录

### 8.0 base.dbf（40 字段，481 字节记录）

dBASE III。头 32 字节 = 版本 3 + 日期 3 + 记录数 u32 + 头长 u16 + 记录长 u16 + 20 字节保留；随后每字段 32 字节描述；0x0D 结束字段区；记录首字节为删除标志；0x1A 收尾。键是 SC(市场 1 字节) + GPDM(6 位代码)，其余 38 字段为股本/财务/股东数，自造时留 0 与空。

### 8.1 .day（32 字节，小端）

| 偏移 | 类型 | 含义 |
|---|---|---|
| 0 | u32 | 日期 YYYYMMDD |
| 4 | u32 | 开盘 ×100 |
| 8 | u32 | 最高 ×100 |
| 12 | u32 | 最低 ×100 |
| 16 | u32 | 收盘 ×100 |
| 20 | f32 | 成交额（元） |
| 24 | u32 | 成交量（股） |
| 28 | u32 | 保留 |

路径 vipdoc\<sh|sz|bj>\lday\<前缀><代码>.day，前缀与市场一致。

### 8.2 gbbq（29 字节）

头部 u32 条数，随后定长记录：加密 24 字节（市场 1 + 代码 7 + f1..f4 各 4）+ 明文 5 字节（日期 u32 + 类别 u8）。长度必须 4 + 条数×29。

### 8.3 events CSV（dump/export）

market、code、date、category、name、dividend_per10、rights_price、bonus_per10、rights_per10（export 多 src 列）。

### 8.4 apply 输入 CSV

必须有 date、open、high、low、close，可选 vol、amount。

### 8.5 spec.json

identity（身份与品牌键）、hosts、try_hosts、info_hosts、wt_hosts、ds_hosts（数组，元素含 ip/port/weight，name 可留空）、single（CHATHOST/ICQHOST 各一）、otherhost（六个索引，-1 表示无）、primary（各区 PrimaryHost 下标）、sections（任意分区键值覆盖）。

### 8.6 bootstrap.json

build_at、spec_sha256、cfg_sha256、dirs、hosts、copied。verify 以 cfg_sha256 判定配置是否被手改。

站点 name 留空时，build_sitecfg 会按分区自动命名（行情主站1、候选站1、数据站1、资讯站1、交易站1 各自的序号），spec 里写死 name 则原样采用。

## 9. 组合流程

### 9.1 建库（数据面）

~~~
$root='data\tdxlib'
$tdx='<安装目录>'
python tdx_bootstrap.py init  $root --ref $tdx --spec spec.json
python tdx_bootstrap.py sync  $root --market 0 --limit 3000
python tdx_bootstrap.py sync  $root --market 1 --limit 3000
python tdx_bootstrap.py fill  $root --market 1 --codes 600000,600519 --days 800
python tdx_bootstrap.py verify $root
~~~

先 sync 权息再 fill 行情，因子才有前收可算。这一步是速查版，从零分步、含期望输出与排错的完整教程见第 13 章；只想要纯脚本自举（不拷 exe）时按第 13 章去掉 --ref 即可。

### 9.2 因子仓库

~~~
python tdx_factors.py sync  --db factors.db --market 1 --limit 3000
python tdx_factors.py build --db factors.db --vipdoc $root\vipdoc
python tdx_factors.py stats --db factors.db
python tdx_factors.py gbbq  --db factors.db --out $root\T0002\hq_cache\gbbq
~~~

### 9.3 单只复权核对

~~~
python tdx_client.py bars --market 1 --code 600000 --count 200 --adjust hfq --vipdoc $root\vipdoc --out 600000.hfq.csv
python tdx_adjust.py check --gbbq $root\T0002\hq_cache\gbbq
~~~

### 9.4 配置单测

~~~
python tdx_local.py sitecfg-check --path $root\connect.cfg
~~~

## 10. 术语

| 词 | 含义 |
|---|---|
| commtype | 业务命令号，帧体前 2 字节 |
| cat / ver / hi / flag5 | 帧头四要素，成对出现，单改会失配 |
| zipsize / unzipsize | 应答体压缩前后长度，不等即 zlib |
| f1..f4 | gbbq 四浮点，见 4.2，单位每 10 股 |
| ratio | 除权折算系数，<1 表示摊薄 |
| hfq / qfq | 后复权 / 前复权 |
| basis | 因子可信度标记：ratio 用真实前收，share 只有股本比例 |
| 前收 | 事件日前一交易日收盘，折算分母起点 |
| 空表 gbbq | 4 字节、0 条的占位文件 |
| done 表 | factors 库里的完成记录，断点续传用 |
| .xdxr_state.json | 自举树里的权息进度文件，按市场记已完成代码 |
| 份额拆分比 | 类别 11/12 的 f3，2 表示 1 拆 2，价格除以它 |

## 11. 排错

| 症状 | 原因 | 处置 |
|---|---|---|
| configparser.read(None) TypeError | cfg_path 传了 None | 传路径，或让调用方兜底 |
| 全部主机连接失败 | connect.cfg 站点不可达或为空 | sitecfg-check，换 spec 里的地址 |
| 报“缺文件”并退 2 | 输入路径写错或文件没生成 | 按打印的绝对路径核对，先补上游步骤 |
| 报“缺参照目录”并退 2 | --ref 指向的不是安装目录 | 指向含 TdxW.exe 的目录 |
| 应答 commtype 不匹配 | 发帧的 cat/ver/commtype 不成对 | 对照 3.1 的表 |
| 只拿到 1 条就断 | 服务端每连接只服务一次 | 让 _request 重连，别自己复用 socket |
| KeyError 'dt' | 本地记录用 date 键 | adjust_records 传 date_key="date" 或依赖自动探测 |
| 因子全为 share | vipdoc 无该股历史 | 先 fill 行情再 build |
| 复权后价格翻倍 | 把类别 5 的股本当比例 | 只认类别 1 |
| 进程活着但不建窗 | 缺 chrome/ 等程序目录 | init 时给 --ref |
| verify 哈希不符 | 手改了 connect.cfg | 重跑 init 或更新清单 |
| gbbq 条数与长度不符 | 写入被打断 | 重跑 sync（有断点，不会重抓已完成）|
| 生成配置 UserHost.IPAddress 为空 | 用了旧 spec 或旧树 | 重跑 sitecfg-defaults；老树用 regen 重造 |
| 客户端不认 base.dbf | 头里记录数或记录长写错 | 用 gen_base 重生成，别手改头 |
| sync 反复重抓同一批 | 断点表被清或用了 --redo | 查 done 表 / .xdxr_state.json |

## 12. 变更须知

源码里的中文注释是给 review 用的：改了帧字段、密钥表偏移、记录布局，注释要跟着改，别留过期说明。

1. 改 CATEGORY、CT_*/CAT_*/VER_* 必须整组一起改，并重跑 smoke。
2. 改 KEY_HEX 等于换密钥，旧 gbbq 全部读不出；改完先跑 200 条往返。
3. 改 ratio 公式后必须重跑 001207 全段比对（最大跳空应回到 8.91%）。
4. 改 T0002_DIRS 或 REF_DIRS 后重跑 init 并实测起窗，别只看 verify。
5. 改 _CFG_B64 前先解压成 JSON 校对，分片不可手工拼接。
6. 新增字段先加进 SCHEMA 的建表语句，再改 upsert，最后改 export，顺序反了会静默丢列。

## 13. 从零自举教程（不依赖 exe）

自举指的是数据链路只靠本仓库的 Python：内置种子造配置，配置连服务端，服务端给数据，落到磁盘。TdxW.exe 只在还想开图形界面时才需要，属于可选的第 8 步。

### 13.1 约定

| 项 | 值 |
|---|---|
| 解释器 | python（3.x，需在 PATH 里；只用标准库） |
| 演示根目录 | data\tdx（相对仓库根，先 $root='data\tdx'） |
| 生产根目录 | 换目录即可，命令里 $root 跟着换 |
| spec | spec.json（仓库根），由种子命令生成，可手改站点与品牌 |
| 前提 | 目标目录为空；能出网访问 7709 |

命令都在仓库根执行，路径写相对仓库根即可；--out、--path、--spec 的相对值按当前目录解析，不会落到安装目录。

### 13.2 第 1 步 造 spec

~~~
$tdx='<安装目录>'
$root='data\tdx'
cd .   # 仓库根
python tdx_local.py sitecfg-defaults --ref nope.cfg --out spec.json
~~~

--ref 指向不存在的文件是有意的：这样纯用内置种子，不掺参照安装的品牌与站点。期望输出“已写出起始 spec”。

要换成自己的主站，编辑 spec.json 的 hosts、try_hosts、ds_hosts、info_hosts、wt_hosts 数组，每项是 ip 与 port。

### 13.3 第 2 步 造树

~~~
python tdx_bootstrap.py init $root --spec spec.json
~~~

期望“目录 76，站点 6，复制 0 个静态文件”。复制 0 个正是纯脚本自举的标志；带上 --ref $tdx 才会拷 exe 与 chrome。

init 见到非空目录直接退出，重来先换 $root 或删掉旧树。

### 13.4 第 3 步 造主表

~~~
python tdx_bootstrap.py base $root --markets 0,1
~~~

清单来自服务端 0x0450，自造 base.dbf，实测约 5 万条、一分钟内。只想要少量样本加 --limit。

### 13.5 第 4 步 抓权息

~~~
python tdx_bootstrap.py sync $root --market 1
python tdx_bootstrap.py sync $root --market 0
~~~

每只抓 0x000F 写进 gbbq，写完即落盘，进度记在 T0002\hq_cache\.xdxr_state.json。中断直接重跑，会自动跳过已完成的；要重来加 --redo。

先权息后行情，因子才有前收可算。全市场十几分钟，用 --limit 可分段跑。

### 13.6 第 5 步 抓行情

~~~
python tdx_bootstrap.py fill $root --market 1 --codes 600000,600519
~~~

期望“合计 2 只 / 500 根”。--days 可调，上限 800。写入按日期合并，重跑只加厚历史不覆盖。

全市场用 --codes 分批，或按 base.dbf 的代码表切段循环。

### 13.7 第 6 步 校验与回读

~~~
python tdx_bootstrap.py verify $root
python tdx_local.py --root $root bars --market sh --code 600000 --kind lday
~~~

verify 期望“通过”；bars 打出首几根 K 线，证明数据真在盘上。日志里有“同步 0 只”属正常，那是有权息记录的证券数，不是请求数。

### 13.8 第 7 步（可选）加图形界面

~~~
robocopy $tdx $root /E /XF connect.cfg base.dbf *.id0 *.id1 *.id2 *.nam *.til
~~~

或者 init 时直接带 --ref $tdx 一步到位。判据只看窗口标题是否出现“海王星金融终端”，别看进程存活；缺 chrome\ 会静默不建窗。

### 13.9 常见错误

| 现象 | 原因 | 处理 |
|---|---|---|
| init 报“目标目录非空” | 目录里有东西 | 换 $root 或清空 |
| 生成的配置字段异常 | 在别的目录下跑了相对路径 | 回到仓库根，或把 --out 与 --path 写全 |
| verify 报 UserHost.IPAddress 为空 | 手写 spec 把该键留空且旧代码未兜底 | 现版本会补种子；老树跑 regen |
| Start-Process 找不到 TdxW.exe | 没做第 7 步 | 补拷程序层，或确认不需要 GUI |
| 进程在但不建窗 | 缺 chrome\ | 补拷 chrome 目录 |
| 读配置乱码 | connect.cfg 是 GBK | 解析时指定 gbk |