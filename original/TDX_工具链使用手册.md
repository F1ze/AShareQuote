# TDX 工具链使用手册

本手册覆盖本仓库里我们自己写的全部脚本：常量含义、函数契约、命令行、数据格式、组合流程与排错。目标是让后来的人（或半年后的自己）不用重读代码就能正确使用、安全修改。

配套文档：TDX_接口逆向分析报告.md（协议与文件格式的实证、证据级别、章节 0–19）。

## 0. 读法

只想快点跑起来：直接抄第 14 章的样例，报错查第 15 章对照表。

要读懂再改：先看第 1 章的分层与调用关系，再看你要用的那个脚本那一章，流程看第 9 章，排错看第 11 章。

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
tdx_client.py     7709 协议层：帧/握手常量、各记录编解码、价格精度、
                  码表、本地读写、复权入口                        联网+只读
tdx_adjust.py     gbbq 解密/加密、因子计算、复权应用               只读
tdx_factors.py    复权事件库(SQLite)，线上同步与 gbbq 回写         写入+联网
tdx_bootstrap.py  从零造目录树、填数、同步权息、校验               写入+联网
tdx_realtime.py   采集调度：连接池、差分、增量落盘与回放、看板取数     联网
tdx_dash.py       本地看板服务，把快照与逐笔喂给 dash.html          联网
~~~

职责边界：帧结构、握手、记录编解码、价格分母这些协议知识只在 tdx_client；tdx_realtime 只管连谁、什么时候发、失败了怎么办、数据落到哪。新增记录类型时先改 tdx_client，再在采集层调用。

依赖方向（箭头表示 import）：tdx_bootstrap → tdx_local、tdx_client、tdx_adjust；tdx_factors → tdx_adjust、tdx_client；tdx_client → tdx_adjust（仅在 --adjust 时才 import，避免默认依赖）；tdx_realtime → tdx_client；tdx_dash → tdx_realtime。

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
| snapshot | --out、--top(默认 20，0 为全部) | tdxzsbase.cfg 板块横截面，按总市值降序，字段已命名 | 0 |
| rules | --out | hqrule.dat 键值对（加 --out 写文件） | 0 |
| files | --out | 只列 lday 文件名与大小 | 0 |
| bars | --market(sh/sz/bj/ds)、--code、--kind(lday/minline/fzline)、--out | 行情 CSV | 缺文件返回 2 |
| base | --out、--markets(默认 0,1)、--limit、--cfg | 生成 base.dbf | 0 |
| sitecfg-parse | --path、--hosts-out | 解析结果与站点表 | 缺文件 2 |
| sitecfg-check | --path | 错误/警告/结论 | 有错误 1，缺文件 2，通过 0 |
| sitecfg-build | --spec（必填）、--out | 生成并自检 | 0 |
| sitecfg-defaults | --ref、--out | 起始 spec JSON | 0 |
| hosts-update | --path、--from 或 --url、--dry | 合并新站表并刷新主机段 | 0，缺文件或缺来源 2 |
| autoup-probe | --base、候选名… | 试候选包名，打印状态码与字节数 | 0 |
| autoup-list | --url | 取清单并列出条目 | 0，取清单失败 2 |
| autoup-get | --url、--dest、--only、--version、--dry | 按清单下文件或整包 | 0，失败 2 |

所有相对路径都按当前目录解析（含 --path、--out、--spec），因此在仓库根执行、写相对路径即可；安装目录本身由 ROOT 从仓库位置推导，不再写死盘符。

### 2.4 sitecfg 三个函数

- parse_sitecfg(path)：返回 {"sections": [...], "hosts": [...], "errors": [...], "warns": [...]}。hosts 每项含 section、index、name、ip、port、weight、areas、host_type。错误是阻断级，警告可自愈。
- build_sitecfg(spec)：由 spec 生成全文并立刻回读自检，出错抛异常且不落盘；强制编号连续、HostNum 与组数相等、段内不重复、空段清空编号。
- default_spec(ref)：以参照文件为底，产出一份可编辑的 spec（身份键 + 4 主站 + 5 试连 + 数据站 + 资讯/交易站），hosts 数组元素形如 {"ip": "...", "port": 7709, "weight": 90}。

base.dbf 三件套：BASE_FIELDS 定义布局，write_dbf 写 dBASE III（头 32 字节 + 每字段 32 字节描述 + 0x0D + 记录 + 0x1A，数值右对齐、字符左对齐），gen_base 只填 SC 与 GPDM、其余留 0 与空。seed_spec() 返回内置站点的最小可用 spec。

spec 顶层键：identity、hosts、try_hosts、info_hosts、wt_hosts、ds_hosts、single、otherhost、primary、sections。

### 2.5 站点地址自更新（hosts-update）

官方客户端换行情地址走的是更新器：它把 newhost.lst 写进 T0002，再合并进 connect.cfg（第 21 章有证据）。本命令照这个格式刷自己的配置。

- 来源优先级：--from 本地文件 > --url 远端 > 环境变量 TDX_HOSTS_URL > 客户端自己维护的 <根>/T0002/newhost.lst 或 <根>/newhost.lst。
- 只重写 HQHOST、TRYHQHOST、INFOHOST、DSHOST 四段，编号从 1 连续重排，HostNum 同步；其它分区一字节不动。
- 合并结果先过 sitecfg-check 那套自检，有错误就放弃写入；写盘前留 connect.cfg.hosts.bak。
- 只比 IP:端口，改名单独计；--dry 只打印不落盘。

~~~powershell
python tdx_local.py hosts-update --path $root\connect.cfg --dry
python tdx_local.py hosts-update --path $root\connect.cfg
python tdx_local.py hosts-update --path $root\connect.cfg --url http://<主机>/update/<通道>/newhost.lst
~~~

远端地址目前仍要外部给：服务端包名换过，本机配置里的旧名全 404。挑战已经能解（见 2.6），所以给对地址就能一条命令取回。客户端侧 load_hosts 只把 newhost.lst 当兜底（配置里一个站点都没有时才读），不会盖掉你手选的站。

### 2.6 直接取升级包（autoup-*）

不跑 AutoUpEx 也能取包：按它的契约走，三条命令分工是“找名、看清单、下文件”（第 22 章有全部证据）。

- http_fetch 遇到 EdgeOne 挑战页会自己算 cookie 重取一次，算不出来就原样返回挑战页，不会把 HTML 当 zip 用。
- autoup-list 把地址末 3 个字符换成 txt 当清单，打印版本、文件数与每条的大小、路径。
- autoup-get 按清单落盘；--only 只取关心的条目（如只取 newhost.lst），--version 传本地版本，清单不比它新就不动，--dry 只列不写。清单第二行是 0 时按整包处理，取同目录的 update/<通道>.zip。
- autoup-probe 在包名被换掉后用来重新发现：--base 给目录，后面跟候选名，200 才算命中。
- 改了解挑战、清单解析或取文件逻辑，跑 re-tools\probes\_autoup_selftest.py，它会起本地假服务把四条路径全验一遍。

~~~powershell
python tdx_local.py autoup-probe --base https://data.tdx.com.cn/autoup tdxw3_yhzqnewjy_tcpip1168.zip
python tdx_local.py autoup-list --url https://data.tdx.com.cn/autoup/update/<通道>.zip
python tdx_local.py autoup-get --url https://data.tdx.com.cn/autoup/update/<通道>.zip --dest build\au --only newhost.lst
python tdx_local.py hosts-update --path connect.cfg --from build\au\newhost.lst
~~~

现网没有可猜的默认名，地址只能外部给。最省事的办法是让客户端自己走一次：在 connect.cfg 的 [Other] 段手工加 ManualAutoupUrl=<完整包地址>（这一条优先级最高），点一次自动升级，然后把安装根 tmp\needautoup.dat 里那串地址抄下来喂给 autoup-get。

另有“从行情站取更新”分支：[Version] 的 AutoupFromHQHost 为 1 时客户端把地址改写成相对的 update/<通道>.zip，并把当前主站 IP/端口写进 [Autoup] 的 HQIPStr/HQPort（缺省 7709），由升级器接成完整 URL。实测 12 台主站的 7709 与 7711 对 HTTP 都是零字节断开，这条分支在现网不通，别指望它。

安装包（自制 MFC 自解压器，整树内嵌、装时不联网）里没有地址，别去里面找。

拿到地址的四条来路：安装根 tmp\needautoup.dat（网页下发时写的，一次 2173 字节）、connect.cfg 的 [Other] ManualAutoupUrl（优先，填了就直接用）、[Version] 的 AutoupURLDir 加 [Other] 的 AutoUpZip 拼、以及自己在网页源码里看。

函数契约：

- http_fetch(url, timeout=20, cookie=None) -> (状态码, body)：GET 一次，命中挑战页则解 cookie 重取。HTTP 错误也返回响应体，不抛栈。
- challenge_cookie(page) -> str：从挑战页解 __tst_status 与 EO_Bot_Ssid；格式不符返回空串，调用方按未放行处理。
- manifest_url(url) -> str：末 3 字符换 txt。地址短于 4 字符抛 ValueError。
- parse_manifest(text) -> (版本, 文件数, [(路径, 字节数)])：头两行不是数字抛 ValueError。
- autoup_get(url, dest, only=None, version=None, dry=False) -> [落盘路径]：only 是文件名或相对路径集合；version 有值时先比版本；清单文件数为 0 改取整包。
- autoup_probe(base, names) -> [(名称, 状态码, 字节数, 头 4 字节)]：逐个试，只打印不判断。

### 2.7 直接调升级器（GUI 按钮不可用时的正路）

本渠道的“自动升级”按钮本身是坏的：它把地址交给升级器时那一格是空的，命令行永远是 autoupex &tdxw&11.68&0_，升级器只回一句“升级地址错误！”。原因是 conf\ff_cfg.ini 与 loading.exe 两件东西都没装，而地址取自一个从未被赋值的变量（详见报告 23.4）。所以要么补这两件，要么直接按契约调升级器。

契约（AutoUpEx 0x549268 一带）：参数是一整串，用 & 分段，形如 autoupex <完整地址含扩展名>&tdxw&<本机版本>&<标志>_<说明>。段数含 exe 必须 3 或 4，否则直接退；末段之前最后三字符会被换成 txt 当清单地址；工作目录必须是安装根；SEPlugins\TEncrypt.dll 与 res_autoup.dll 缺一不可。

已实测通过的走法（本地假站，整包模式）：

~~~
cd C:\neptune
.\AutoUpEx.exe "http://127.0.0.1:8899/tdxw3_yhzqnewjy_tcpip1168.zip&tdxw&11.68&0_"
~~~

清单文件内容是 1169 与 0 两行，服务器日志先后收到清单与包的 GET 并均回 200，包落到 Update\tdx_update\，进程退出码 0。若只想拿包不想装，用 2.6 的 autoup-get 更省事。

如果确实要让按钮自己跑起来，可以伪造运行期数据：T0002\tmp\needautoup.dat 是 2173 字节，偏移 0 是标志字节（非 0 表示有更新），偏移 125 起是 2046 字节地址槽，末尾 2171 起两字节，读入后 123 与 2171 会被强制置零。注意每次点击都会先用零字节覆盖这个文件再读，所以造好之后必须设成只读（attrib +R），否则白造；生效的表现是提示变成“升级地址错误！”。但即便如此地址槽仍会被空地址顶掉，所以这条路只解决“走不走进升级分支”，不解决“地址从哪来”。

#### snapshot 的列

26 列全部命名返回（market、code、free_cap、total_cap、float_cap、chg、amount 等），空串给 None。列 4 总市值、列 5 流通市值、列 19 成交额已用成分求和核实；列 2 自由流通市值、列 12 振幅等是强推断，全部口径见报告 24.2 与 24.3。某行的市值列整组为空表示该板块当天没重算（全文件有 5 行残值）。

~~~
python tdx_local.py snapshot --top 20
python tdx_local.py snapshot --out zs.csv --top 0
~~~

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
| request_raw(commtype, payload=b"", catword=1, ver=0, flag=0) | 裸帧请求 | 按 commtype 对齐的应答体 | 命令字在头 10..12、body 原样、两段长度相等即不压缩；最多丢 8 条夹带推送，3 次重连 |
| info_hello() | 资讯口问候（0x1F7E） | {raw,time,build,path,f10,table} | 打 INFOHOST，必须先 handshake=False 与 sections=(INFOHOST,DSHOST) |
| call(commtype, payload, catword, ver, flag, extra=0) | 组帧并走完整重试 | 应答体 | 全工程唯一的组帧入口；extra 抬高声明长度（逐笔族 +2），失败换机 |
| list_page(market, start) | 取一页码表 | 应答体 | 握三条要逐条读应答，声明长度含 commtype |
| snapshot(stocks) | 五档快照 | [记录字典] | 体形 = 前缀 + 数量 + 证券表 + 0C 01，尾部位置不能动 |
| ticks(market, code, start, count) / orders(同) | 逐笔分钟版 / 带秒版 | [{t, price, vol, trades, side}] | 两者同一条流，只是后者多一个秒字节 |
| probe() | 健康探测 | 应答体 | 打一条日线，能回就算活 |

**组帧只在这里**：0x0C 头、双长度、commtype、握手序列、价格分母都在本模块；tdx_realtime 与 tdx_bootstrap 只传参、不碰字节。

重试语义：_request 与 call 内各 3 次，每次失败先 _close() 再换一台主机——实测该服务端每连接只服务一次业务请求，不重连就必然拿不到第二条，而换机是因为有一批机器连上即关。上层不需要再包重试。

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
| info-hello | --cfg | 发资讯口问候帧，打印服务时间/版本/路径/F10 与表格能力位，有版本串返回 0 |

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
| WARRANT_CATEGORIES | (13, 14) | 送认购/认沽权证，默认不参与折算（报告 25 章实测） |
| WARRANT_IN_PRICE | False | 置真则按权证内在价值折价，属可选强推断；命令行用 --warrant 打开 |
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
| factors | --gbbq --market --code --closes-csv --mode、--warrant | 逐事件前收与系数，有前收时再打印整段曲线 |
| apply | --gbbq --csv --market --code --mode --out、--warrant | 复权后 CSV，多一列 mode |

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
4. sync 默认 64 条并发连接，中途失败只计数不中断，收尾按 rounds=3 自动补抓失败项（失败不写 done，所以要补）。8 并发实测 120 只要 10.5-14.4 秒，64 并发 1.2 秒，128 反而更慢且失败翻倍；整市场仍以十分钟计，瓶颈是服务端对单 IP 的接纳，不是本地线程。

## 6. tdx_bootstrap.py —— 从零造树

**写入 + 联网**。init 拒绝非空目录，避免覆盖既有库。

### 6.1 常量

| 名称 | 内容 | 作用 |
|---|---|---|
| T0002_DIRS | 51 个目录名 | T0002 下的固定骨架（缓存、cloud_*、wt_*、User、log 等），连 vipdoc 的 21 个共 72 个目录 |
| MARKETS | bj、sh、sz、ds、ot、cw | vipdoc 一级市场目录，含义见下方 |
| MARKET_SUBS | 每个市场各自的周期子目录 | 沪、深、京四类（含 eday），ds 三类（无 eday），ot、cw 无 |
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
| sync_gbbq(root, market, codes, limit, cfg, redo, jobs=64, rounds=3) | 抓权息写 gbbq，断点续传、按主键去重；jobs 是并发，rounds 是失败补抓轮数连接数 | {records, added, symbols, failed, codes, skipped} |
| verify(root) | 五项校验 | (问题列表, 统计字典) |

verify 检查：目录齐全、connect.cfg 可解析且有主站、gbbq 长度与条数自洽、bootstrap.json 存在、清单里的配置哈希与现文件相符。任一不过返回问题列表，CLI 退出码 1。

bootstrap.json 记 build_at、spec 与 cfg 的 sha256、目录数、复制清单；它同时是 verify 的比对基准，手改 connect.cfg 会让哈希校验失败。

fill 与 sync_gbbq 都从目标树自己的 connect.cfg 取站点，树必须先 init；--cfg 可直接指别处的配置。

目标树没有 connect.cfg 时 fill 不再直接报错：依次回退到仓库根的 connect.cfg、connect.gen.cfg，最后回退安装目录的默认配置，并在 stderr 打一行提示说明改用了哪份。回退只解决连得上，树本身还是空的——T0002 骨架与 base.dbf 仍要 init 才有。只给一个空目录就 fill，报的是 connect.cfg 与 newhost.lst 都没有可用主机，就是这一条：树没造过。

### 6.3 命令

| 命令 | 参数 | 退出码 |
|---|---|---|
| plan | root | 0 |
| init | root --spec --ref | 0 |
| fill | root --market --codes --days --cfg、--kind、--from、--no-merge | 0 |
| delisted | src --markets --out | 0 |
| archive | src --out --day | 0 |
| sync | root --market --codes --limit --cfg、--redo、--jobs(默认 64) | 0 |
| base | root --markets --limit --cfg | 0 |
| login | root --cfg | 0 |
| regen | root --spec --ref | 0 |
| verify | root | 通过 0，有问题 1 |

fill 默认合并写：按时间键并进已有文件，同一时间点以新抓为准，旧历史保留，用来一天天攒长历史；只有 --no-merge 才整文件覆盖。--kind 选周期（day/min/5min/15min/30min/60min），--from 从别的树拷而不是走网络，退市股与超出保留深度的历史只能这样搬。

delisted 扫一份现成 vipdoc，列出“本地有、主站码表没有”的代码，就是该保种的退市清单。archive 把 base.dbf 按日存档到 db/base，供日后回算 PB 一类估值。

### 6.4 实测规模

init 产 72 目录、148 静态件、合计约 3836 文件（chrome 占 70）；不带 --ref 的裸 init 也能过（72 目录、4 站点、0 复制），只是没 exe。

### 6.5 注意

1. 判据看**窗口**不看进程存活：缺 chrome 时进程活着、日志只有版本号、永不建窗（18.3 有完整排除过程）。
2. --ref 里 T0002 只复制 2 MB 以内的状态文件，大 zip 与子目录内容不复制。
3. verify 对空 gbbq 只查长度自洽，不要求有内容。
4. 程序层（exe/DLL/chrome）无法自造，仍依赖一份安装来源。

## 7. 小工具

抓包与探针收在 re-tools\ 下，与建库主链隔离，说明见 re-tools\README.md；行情侧的常驻脚本是 tdx_realtime.py 与 tdx_dash.py，用法见第 14 章与 11.13-11.24。

探针允许自己拼原始帧——它们就是拿去摸协议的，故意不走 tdx_client 的封装；主链脚本则一律用 tdx_client 的 call/snapshot/ticks/orders/list_page，模块里不再出现 0x0C 头。

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

### 8.7 六个市场目录各是什么

MARKETS 就是 vipdoc 下面那六个一级目录，客户端从 0x96a6c0 起按这串登记（ds\minline、bj\lday、sh\fzline、sz\eday 都在里面）：

| 目录 | 含义 | 本机实况 |
|---|---|---|
| sh | 上海证券交易所 | 4837 个 day 文件，如 sh000001.day（上证指数） |
| sz | 深圳证券交易所 | 4347 个 day 文件，如 sz000001.day |
| bj | 北京证券交易所 | 353 个 day 文件，如 bj899050.day、bj920000.day |
| ds | 扩展行情位，文件名带市场号前缀（模板 vipdoc\ds\lday\%d#%s.day），放期货连续、美股、港股这类非沪深京标的（静态串里就有 IFL0/AAPL/00001 三个例子） | 空目录，只有三个子目录、没有 eday |
| ot | 杂项预留位，客户端字符串里查不到它，安装器照样建 | 空目录，连子目录都没有 |
| cw | 财务位，放 gpcw<年月>.dat、gpsh999999.dat 与 gpcw.txt、gpszsh.txt 清单 | 只有两份 txt 清单（见 35 章） |

只有前三个是真市场，行情文件按 市场前缀加六位代码 命名；ds 是另一套带 # 的命名；ot 与 cw 不走 %s\lday 那套。造树时按 MARKET_SUBS 分派子目录，别给 ds 建 eday、也别给 ot 和 cw 建子目录——实测这样造出来的树与安装目录逐目录一致，客户端能正常起窗。

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
| 抓 7715 时行情一起断 | 登录与行情共用一个主站 IP，端口不同 | 用 build\\pw201p.py 的分流转发器，按命令字小于 0x0100 走 7715、其余走 7709，别只改端口 |
| 分流后两边都没回包 | 单口转发器的上游端口写死 7715，行情命令被打到交易口 | 换 build\\pw249.py 双端口（17709 转 7709、17715 转 7715）加 build\\pw250.py |
| 转发器端口被占 | 起过两个监听同一端口 | 先 Get-Process py 全杀再起，SO_REUSEADDR 会让两个进程都绑上却不平分连接 |
| 抓完行情全红 | connect.cfg 还指向 127.0.0.1 | py build\\pw227.py 从 build\\cfg_info.bak 恢复，再 netstat 确认 7709 直连 |

### 11.1 登录通道 7715 的抓法

登录口与行情口同 IP 不同端口，改端口会把行情带断，所以用分流器：build\pw201p.py 监听 17715，窥探首帧命令字，小于 0x0100 转 7715，其余转 7709，日志落 build\route.log；build\pw202.py 把 HQHOST01 指向 127.0.0.1:17715，改前留 build\cfg_info.bak，抓完用 build\pw227.py 还原。7715 不认行情口的 Setup 握手，先发握手会被 RST。交易窗口是自绘面板，UIA 与 ToolBar 都枚举不到按钮，登录要靠 CEF 调试口或静态组包。调试口试过两种：TdxW.exe 加 --remote-debugging-port 能起 DevTools（Chrome/81），但交易这条不经过网页；xiadan\config.ini 的 [CefCmdLine] 里加 --remote-debugging-port=9333 重启下单程序，端口仍不开，故 CEF 一路要先解决怎么把它点起来。改 xiadan\config.ini 前留备份，改完记得还原。

### 11.2 双端口分流器与界面观察（30 章）

单口分流器后来发现上游端口写死 7715，把 0x000D、0x0015 这些行情命令送进了交易口，全静默有一半是这个造成的。改用双端口：buildpw249.py 同时监听 17709 与 17715，前者固定转主站 7709，后者固定转主站 7715，日志落 build
oute2.log；buildpw250.py 把 HQHOST01 指 127.0.0.1:17709、UserHost 指 127.0.0.1:17715。改完重启客户端，0x0015 立刻有应答。还原仍用 buildpw227.py。

界面侧四条经验：

1. 交易窗（类 #32770，标题含 V6）是空壳，子窗口只有停靠条，抓图只能用 PrintWindow 标志 2，标志 0 是整幅灰；没有子控件就没有焦点，SendKeys 键入无处落。
2. 枚举模块要用 32 位宿主：& "$env:SystemRoot\SysWOW64\WindowsPowerShell\v1.0\powershell.exe" -File build\mods.ps1。64 位 PowerShell 只会给出 wow64 桩。
3. UIA 的元素矩形是子坐标不是屏幕坐标，本机屏幕 2559 宽而帧自报 2561（DPI 虚拟化），点击坐标要乘 2559/3840 再送。
4. 想脱离鼠标就用消息注入：对帧 PostMessage WM_COMMAND（0x0111），扫 id 会真的弹出对话框，但帧没有 Win32 菜单（GetMenu 为 0），id 拿不到只能盲扫，见 build\cmd.ps1 与 build\sweep2.ps1。

另外 xiadan.exe 是同花顺的独立下单端（sysconfig\NetWorkSetting.json 里是 115.236.34.168:8009、CRYPTEXTEND=HEXIN），不是 7715 的客户端；它的登录窗可以被 UIA 枚举（1237 客户号、1108 通讯密码、1006 委托登录），但元素都是 Pane、没有 ValuePattern，只能按矩形点。观察交易失败原因看 T0002\log\DataModule.log。

### 11.3 点击前必须先收走 Edge（31 章）

31 章查明：本机屏幕上有一个最大化的 Edge 窗口（msedge.exe，-7,-7,2566x1239）压在最上层。它是个顶层窗口，不属于应用，但 WindowFromPoint 在整屏任何一点都返回它，于是所有鼠标点击都被 Edge 吃掉。应用的窗口照样能被 SetForegroundWindow，GetForegroundWindow 回读也正确，光看这两个调用发现不了。

所以动界面前固定先跑：

~~~
python build\pw291.py
~~~

它把 msedge 的可见窗口逐个 ShowWindow(6) 最小化，跑完再点。之后 WindowFromPoint 才会返回应用自己的 AfxWnd100。

坐标：UIA 的子坐标除以 1.5 就是屏幕坐标（版面容器 y=123 对 MDIClient 屏幕 y=82，123 除以 1.5 正好 82）。顶栏按钮在屏幕 y 约 8 至 22。

验证点击是否真被处理，用 PrintWindow 前后比像素，别凭感觉：

~~~
python build\pw294.py
~~~

期望输出一行“像素总数 …… 变化 ……”，变化占比到百分级就说明点击进了消息映射（点 1000 号按钮实测 93634 / 3171668 即 3.0%）。

现成的两个盲扫器都带判据（17715 接入数或 V6 子窗数变化），命中即停：

~~~
python build\pw292.py
python build\pw295.py
~~~

写到本手册这一版时两处都没命中，说明交易入口不在顶栏也不在标签带，别把重跑当成新结论。


### 11.4 交易通道：抓包与回放（32 章）

交易口不是 7715。真实入口是 cfg 里 TPSYS 与 TPSUBSYS 段的 TPHOST（端口 9914 与 9915）。要确认现场，直接按 PID 看连接：

~~~
$p = (Get-Process TdxW).Id
Get-NetTCPConnection -OwningProcess $p -State Established | Select-Object RemoteAddress, RemotePort
~~~

抓包用系统自带的 Wireshark 命令行，明文 TCP，不涉 TLS：

~~~
& "C:\Program Files\Wireshark\tshark.exe" -D
& "C:\Program Files\Wireshark\tshark.exe" -i 3 -f "tcp port 9914 or tcp port 9915" -w build\trade.pcap -q -a duration:50
~~~

接口号按 -D 的实际输出填（本机以太网是 3）。抓包同时起客户端，50 秒自动停。导出帧序列：

~~~
& "C:\Program Files\Wireshark\tshark.exe" -r build\trade.pcap -Y "tcp.stream==0 and tcp.srcport==57362 and tcp.len>0" -T fields -e tcp.len -e data.data > build\cframes.txt
~~~

流号与客户端端口按 -z conv,tcp 的输出替换，否则筛出空集。

回放只读 cframes.txt，依次发送并打回应答：

~~~
python build\pw319.py 1.202.143.37 9914 3
python build\pw319.py 218.205.130.77 9915 3
~~~

前 3 帧（握手启动、客户端声明、密钥交换）都能拿到正常应答；给到第 4 帧才发送 ACL.checkuser，它一定回 0 字节，因为密文绑当次会话密钥，重放旧密文对不上。这不是脚本坏了。

解码帧头要看第 2、3 字节的步序 cat（递增 3、4、5、7、11、15）与 commtype（0x2600 加序号，26AC 至 26AE 握手、264B 命令、26B1 收尾），首字节 0x0B 上的 0x80 表示载荷压缩、0x40 表示带后续，别当成固定魔数。

做差分定位密文段时用 build\pw318.py：它按 cframes.txt 打印每帧的首字节、cat、双长度、commtype 和载荷前 20 字节，比对着看就能判断新抓的帧是否同型。


解 checkuser 密文用 build\pw326.py：它把第 4 帧解成载荷、切出 00 1F 5F 起的密文段（落 build\cu_seg.bin），再按三种起点 × 四种密钥派生 × 八种算法跑 openssl，打印可打印率。当前最好 0.406，说明写死串 ##DoNotTellYouOk123 不是这把钥匙（那个串只用于 %s/system.uid，见 32.8），别以为它一跑就该出 JSON。

写死串的引用点只有 sub_1000BE90，加解密是 sub_1014B0E0(密钥, 3) 与 sub_1014B2D0(缓冲, 240)；顺着这两个函数的调用点才能摸到会话密钥的来路。


### 11.5 tdxzsbase 未知列的辨别（33 章）

两件前置必须先做，否则全是噪声：一是日期要跟快照同日（C:\neptune 的 tdxzsbase 是 20261103，而本地 day 文件末根是 20261016，拿 build\new 里 20261016 那版对），二是每只都要 day 文件存在且末根等于快照日。

~~~
python build\pw342.py
~~~

它逐列算 值 / 口径 的比值中位数与相对偏差中位数，口径含 2-9 日额均量均与当日值。判据是“中位近 1 且偏差小”，已知列能复现（列 3、4 比值 7-8），但列 23、24、25 各窗口偏差并列在 0.388-0.41，分不出天数——成交额自相关太强，别指望调参能分开。

另记两条硬约束：本地 day 文件全是 288 字节即 9 根，窗口上限只有 9；category 9 的线上记录按 日期 u32 + 四价 varint + 量额 切，第 2 条起就错位，尾部补 4 到 9 字节都对不上（build\pw340.py 逐档试过），所以想吃 10 日以上得先修记录解析。


### 11.6 权息同步的并发与补抓（34 章）

~~~
python build\pw349.py      # 并发 4/8/16/32/64/128 各 120 只，打印秒数、只/秒、完成只、失败、记录
python build\pw350.py      # 同一棵树连跑三轮，看失败被补抓吃掉（78→99→99 只）
python build\pw351.py      # 默认参数一次调用，应为 1-2 秒、完成 99 只、失败 0
~~~

判断标准：64 并发下第一轮有失败是正常的，补抓轮必须把失败收敛到 0，完成只数三轮内不再增长就是全部抓完（空结果也算完成）。若失败率超过三成，把 jobs 降到 32、rounds 提到 5。

取码表（security_list）失败与并发无关，服务端会偶发直接断连，报“请求失败: 连接被关闭”时重跑即可，别去调 jobs。


### 11.7 财务包：7711 上的 0x1F42（35 章）

HTTP 别再试了，www 与 data 两个站对 tdxbase/、cw/ 一共九条候选全 404（build\pw353.py 一次跑完两个站）。

真通道是资讯口 7711：12 字节帧（cat 1、ver 0、flag 0、commtype 0x1F42），body 直接写路径，例如 cw/gpcw.txt、cw/gpszsh.txt。

~~~
python build\pw365.py "cw/gpcw.txt"
python build\pw363.py
~~~

pw365 会收完整应答、按 16 字节头拆帧、解压并落 build\fin_*；pw363 是命令扫描，用来复核 0x1F42 是唯一有实质回包的命令。

应答解压后固定 59994 字节，开头是 K 加十进制偏移，三次取同一路径给的是不同偏移的块——分块坐实，续传参数还没解出来（同连接不收第二块，发 0x1F43 会被关连接）。下一步先解 0x1F44 返回的那 40 字节。


### 11.8 0x1F42 分页读（36 章）

不要找偏移参数，命令就是分页读：每调一次给下一窗口，换连接也照样推进。

~~~
python build\pw373.py    # 六次同路径，证明块内容在变
python build\pw375.py    # 连拉到绕环为止，统计字节与去重码
python build\pw378.py    # 路径前缀换数据集（.txt 出条目流，.dat/.zip 出键值记录）
~~~

每块固定 59994 字节，是服务端缓冲区，不是文件大小；流是环形的，读空判断不可靠，必须按条目或代码去重。tdxbase/ 与 cw/ 前缀只是不同数据集，扩展名不决定格式。


### 11.9 0x053E 快照行情（37 章）

“本服不通”已作废。形状对就有包，关键是两件事：请求体必须正好 12 字节前缀加每只 7 字节，声明长度必须等于实际长度（12+7n）。

~~~
python build\pw381.py    # 对照：同连接 0x000F 有包、0x053E 无包（证明链路正常）
python build\pw391.py    # 声明长度与实际不等的五种写法，全超时
python build\pw392.py    # 五种前缀形状，只有 05 00 00 00 + 四零 + 数量 + 00 00 有回包
python build\pw393.py    # 一只 60 字节、三只 194 字节，验证列表被消费
~~~

回包用 01 开头，不是 parse_quotes 期待的 B1 CB，解析要另写。记录里的六字节当前恒为 600839，别当成请求代码的回显。


### 11.10 13/14 权证折价（38 章）

权证代码已不在现行码表，挂牌价取不到；别按内在价值硬折，认沽多半会被截成零。用拟合系数：WARRANT_Q 或 --warrant-q，默认关。

~~~
python build\pw400.py    # 列出 24 条 13/14，看清都是 2005-2006 股改
python build\pw403.py    # 日线 start 是回退条数，不是日期
python build\pw409.py    # 三种口径误差表 + Q 网格，最优 Q=0.35
python build\pw124.py    # 回归：001207 两种口径都应是 0.0792
~~~

换市场要重跑网格，别把 0.35 当常数搬走。


### 11.11 资讯通道读命令族（39 章）

窗口里全是代码不是钱。对齐是 代码六 + 标志一 + 零，别按标志在前切。

~~~
python build\pw423.py    # 0x1F43 回卷后再读，能看到二十字节流头
python build\pw424.py    # 0x1F44 按整条记录读（40 字节），0x1F45 回状态文本
python build\pw426.py    # 解 0x1F44 那条记录：字段数九 + 日期 + GBK 表名
python build\pw431.py    # 状态文本恒为“网络错误”，与路径无关
~~~

0x1F45 有不回包的脾气，超时给足；无路径的 0x1F42 是“接着上次位置读”，位置全局共享。

### 11.12 40 章：指数与截断的坑（加列语义）

这一段的脚本都在 build\pw46x.py，跑之前先读 40.1 与 40.2。

~~~
python build\pw458.py     # 指数一次只回一根：count 800 也只有 1 条
python build\pw459.py     # 开户即断开：每请求一条连接才稳定（60/60）
python build\pw460.py     # 市场列取值：1 是沪、0 是深、2 是北
python build\pw464.py     # 按日位移算残差，定位列 24/25 的日期口径
python build\pw466.py     # 列 24 对当日额、列 25 对前一日额的滞后结构
python build\pw468.py     # 可复现回归：截断前后同一请求的日期与全部数值
~~~

三个规矩：

1. security_bars 的日期闸不要动：日期不在 19900101-20301231 就跳出，尾部填充当不了记录。
2. 取指数一律按“一根一请求”，别指望 count。
3. 判列语义用比值加离散度，不用相关系数：靠得住的判别是“对齐后相对离散度趋近 0”，本轮的 0.0000 就是这么来的。

列 24 是当日成交额、列 25 是前一交易日成交额，单位万元；比值恒 1e-4。20-23 是涨跌幅族，天数未定；列 19 仍是疑。

### 11.13 tdx_realtime.py：盘口采集（41 章）

> 本节写的是采集层的用法（池化、节奏、落盘）。帧、握手、记录编解码与价格分母都在 tdx_client，本模块只传参；要改协议去第 3 章。

只读网络，不碰安装目录。协议细节与实测数字在报告 41 章，这里只讲怎么用、怎么坏、怎么修。

~~~
python tdx_realtime.py probe-l2                     # 探 L2 逐笔，通=退出 0，不通=3
python tdx_realtime.py rtt --codes "600000" --n 60 --concurrency 8   # 量往返分布
python tdx_realtime.py once --codes "600000,600519"                  # 打一份五点快照
python tdx_realtime.py run --watch watch.txt --cycle 3 --mode auto --out q.jsonl
~~~

几个约定：

1. --codes 在 PowerShell 里**要加引号**，逗号不加引号会被拆成多个参数。
2. 码表写法三种：600000、0:000001、1:600519；不写市场按首位推断，6/5/9 走沪，其余走深。
3. run 的 --out 是 JSONL 追加写，stdout 也是 JSONL；stats 走 stderr，别把两股混在一个文件里。
4. --mode：l1 只发快照，l2 先探逐笔，auto 探不通就静默降 L1。--l2-every N 每轮给前 N 只补逐笔。
5. 差分只在价量变化时放行，零点空转不会刷屏；要看全量就每条都改 vol 之外的东西没有意义，直接 once。

坏了先看三处：

| 症状 | 处置 |
|---|---|
| 满屏 RtdError('连接被关闭') | 主机在关连接。health 会筛，若筛完仍报，把 keep 调小或换 connect.cfg 的 HQHOST |
| last_error 是超时且 errors 长期不减 | 先 probe-l2 与 once 各打一次；都超时按“服务时段”处理，不是协议坏了 |
| 记录里 code 与请求不符 | 位置放错时才会错一条，见 11.14 第 1、2 条；位置对时回包码与请求逐条对应 |

三个规矩：

1. 别把固定 56 当步长。价格是 varint，有值时记录会变长（实测 81），必须顺序消费。
2. 别预读固定条数的握手应答，按 commtype 认领业务应答，否则随机吞包。
3. 工人用完连接就 close，别做连接复用：服务端不留空闲连接。

请求体末两字节 0C 01 必须放**证券表之后**，放到数量后面会让服务端按错位解析（代码只认末位、回包恒 600839、数据错开一条）。沪市与深市都能取，不需要挑票（11.14 有自检命令）。


### 11.14 盘口快照的字段口径与自检

快照（0x053E）的记录字段是 2026-11-05 凌晨用 000001 当日日线逐字段校准出来的，单位与顺序如下：

| 序 | 字段 | 类型 | 单位/说明 |
|---|---|---|---|
| 1 | market | u8 | 市场，0 深 1 沪 |
| 2 | code | 6s | 证券代码 |
| 3 | active | u16 | 活跃度，与尾部 active2 同值 |
| 4 | price | varint | 基准价，分（1/100 元） |
| 5 | last_close | 差分 | 前收 |
| 6-8 | open/high/low | 差分 | 开、高、低，0x40 位为负 |
| 9 | amount_hundred | varint | 成交额，百元 |
| 10 | skip1 | varint | 未定性，常等于负的基准价 |
| 11 | vol | varint | 成交量，手 |
| 12 | cur_vol | varint | 现手 |
| 13 | amount | 浮点 | 成交额，元 |
| 14-15 | s_vol/b_vol | varint | 外盘、内盘（顺序待再验） |
| 16-17 | skip2/skip3 | varint | 未定性 |
| 18-37 | 五档 | 价差分 + 量 varint | 买价、卖价、买量、卖量 乘 5 档 |
| 38 | raw2 | 2 字节 | 定长占位 |
| 39-42 | skip4..7 | varint | 定长占位 |
| 43 | speed | i16 | 涨速，百分之一 |
| 44 | active2 | u16 | 同 active |

价格一律除 100（PRICE_SCALE），别用旧的除 1000。

三条硬约束：

1. 请求体 = 12 字节前缀 + u16 只数 + 每只 7 字节（市场 + 6 位 ASCII）+ 0C 01，声明长度必须等于 12+7n+2。尾巴的位置是关键，放错就退化成“只按末位解析”。
2. 回包记录与请求逐条对应，回包里的代码就是解析结果，可以用来核对；位置放对之后沪市完全正常（600036 回 42.54、前收 42.69）。
3. 记录是变长的，全零 56 字节、有值 81 字节，解析只能顺序消费，改成定长切必错位。

自检命令（对当日日线比价量，应 3/3 命中）：

~~~powershell
python tdx_realtime.py once --codes "0:000002,0:000006,0:000009"
~~~

沪市自检（预期现价 42.54/1446.44、前收 42.69/1480.00，命中说明尾巴位置对）：

~~~powershell
python tdx_realtime.py once --codes "1:600036,1:600519"
~~~

跨市场批量自检（沪 12 深 12 一次发出，24 行都应带回包码且无 mismatch，价量额相互自洽）：

~~~powershell
python tdx_realtime.py run --watch build\watch_2m.txt --seconds 10 --out build\live2.jsonl
~~~

要把客户端自己的请求重录一遍（怀疑形状时才用）：先 python build\cfg_hook.py on，再起 build\hq_proxy.py serve 记包，客户端翻票后 decode 看请求体，最后 python build\cfg_hook.py off 还原。

### 11.15 L2 逐笔（0x0FC5 分钟版 / 0x0FC2 带秒版，同一条流）

先跑 probe-l2，通才有下面这些。

~~~
python tdx_realtime.py probe-l2                                    # 通=0，不通=3
python tdx_realtime.py run --codes "1:601881" --mode l2 --l2-every 1 --cycle 1 --out q.jsonl
python tdx_realtime.py run --codes "1:601881" --mode l2 --l2-every 1 --l2-orders --cycle 1 --out q.jsonl
~~~

字段（JSONL，kind 为 tick 或 order）：

| 字段 | 说明 |
|---|---|
| t | 成交时间，HH:MM:SS；tick 的秒位恒 00，order 的秒是真的（3 秒粒度） |
| price | 成交价，元 |
| vol | 成交量，手 |
| trades | 这一跳聚合的成交笔数 |
| side | 0 主动买、1 主动卖、2 中性、5 盘后固定价格 |
| sec | 只有 order 有，秒，0 到 59 且恒为 3 的倍数，跨 60 回卷 |
| kind_code | 同 sec，老字段名，保留兼容 |
| market / code / kind / mode / ts | 采集器补的定位与时间戳 |

四条硬约束（改代码前先读）：

1. **帧头是 0x0801 / 0x0300 / flag 1**，不是 0x0817 / 0x0101。旧值在本服一帧不回。
2. **声明长度要比实际体多 2 字节**，_call 的 extra=2 就是干这个的。少这两字节服务端一直等，不回包不断连。
3. 握手应答必须读干净再发业务帧，_call 里先读后发那两步别合成一次 sendall。
4. 价是差分：0 等于上一条同价，0x40 位是负号。按绝对值解析会得到一堆 0 和负数。

去重：逐笔没有自增序号，_tick_job 用 (t, price, vol, trades) 当签名，只放新成交，签名表按只封顶 800 条。

自检：整日量对得上就是对的。601881 从 start 0 每轮 900 条翻 5 轮，4090 条合计 317414 手，对当日日线 317108 手差 0.096%。翻到 4500 返回空，说明 start 是从最新往回数的条数。

~~~
python -c "import os,sys;os.environ['TDX_CFG']='build\hosts_ok.cfg';sys.path.insert(0,'.');import tdx_realtime as R;p=R.Pool(1);w=p.workers[0];s=0;t=0;n=0
for _ in range(6):
    g=w.ticks(1,b'601881',s,900);w._drop();s+=900
    if not g: break
    n+=len(g);t+=sum(r['vol'] for r in g)
print(n,t)"
~~~

现成脚本更省事，改过解析或换站换票都先跑它，差超过 0.5% 就报不通过（退出码 1）：

~~~
python re-tools\probes\_l2_sumcheck.py --market 1 --code 601881
python re-tools\probes\_l2_sumcheck.py --market 1 --code 601881 --expect 300000   # 故意给错基准，应不通过
~~~

夜间 15:00 之后整段都是 side 5、价格不动，做分钟聚合时要么单列要么剔除，别当正常成交统计。


### 11.16 0x054C 与 0x0FC2 的取舍（41.18 收口）

**0x0FC2 不是逐笔委托，是同一条成交流的带秒版。** 1200 条逐条比对，价格、量、方向全同，只多一个秒字节；0x0FC0 到 0x0FC9 只有 0FC2 与 0FC5 应答，本服没有独立委托流。那个字节是秒，0 到 59、恒为 3 的倍数、跨 60 回卷，3600 条里 0 条越界。所以要时间就用 order 那一路，kind_code 与 sec 都是它。

**0x054C 是 0x053E 的加长变体，多出来的是固定 56 字节扩展区**，一档之前与 053E 逐字节相同，56 字节 = 14 随票 + 16 高熵块 + 24 恒零 + 2 复读 active（十只票 10/10 三条律全中）。那 56 字节不承盘口价量：高熵块按 varint、u16、u32、f32、f64 解都不成立。取数用 053E，别为了"多 16 字节"去用 054C。

要自己复验：

~~~
python build\pw692.py          # 十只票验 56 字节定长 + 零段 + 复读尾
python build\pw685.py          # 四只票各 900 条验秒的取值域与差 3
python build\pw686.py          # 1200 条比对 0FC2 与 0FC5，应量价方向全同
~~~

盘中已复验（2026-11-05 09:41，十只票）：零段照样 10/10 全零，同一刻 053E 与 054C 的价量逐项相等，所以零段不是十档留槽、054C 也没给多一档的数据。14 字节里那几个小整数的名字仍未定。


### 11.17 tdx_dash.py：单页实盘看板（演示）

只给眼睛用，不参与主链。绑 127.0.0.1，标准库 http.server，上游按代码缓存 0.9 秒，浏览器 1 秒一刷也打不爆主站。

~~~
python tdx_dash.py --port 8811
~~~

开 http://127.0.0.1:8811/ 就是看板：左上大号价与前收差、中间逐笔折线（红买绿卖）、右侧五档带量条、下方逐笔明细，顶栏可换代码、有 L2 指示灯与上游耗时。dash.html 是单文件页面，改样式只动它。

三个接口：

| 接口 | 给什么 |
|---|---|
| GET / | dash.html |
| GET /api/snap?codes=1:600036 | 一份 L1 快照（价量额、五档） |
| GET /api/ticks?code=1:600036 | 最新逐笔 200 条（0x0FC2，带秒） |

要点：缓存键按代码，命中时照样回上一次的真实上游耗时（显示 0 会让人以为没查）；页面侧按 (t, sec, 价, 量, 笔数) 去重，服务端每次都给整窗，浏览器只把新的画上去。停服务按 Ctrl+C，池子会关。

打开方式两种都行：走 http://127.0.0.1:8811/，或者直接双击 dash.html。双击时页面是 file:// 协议，相对路径解析不了，所以里面认协议、file 下改打 http://127.0.0.1:8811，服务端也放了 Access-Control-Allow-Origin: *。**服务没起时页面不会静默不动**，会显示"连不上服务，先跑 python tdx_dash.py --port 8811"。选的代码存 localStorage，刷新不丢。

边界：单进程两个 worker，只适合看几只票；要成规模还得走 run 那条采集链。

### 11.18 RtdError: 连接被关闭 是什么

不是协议错，是主机表里有“接受连接后立刻关闭”的机器（报告 41.6 记的那批）。这种关闭发生在 TCP 建连成功之后，_connect 的循环发现不了，于是 sendall 看着成功、recv 立刻回空，报成连接被关闭。

本轮已从两头堵住：

1. Worker._call 现在带 CALL_TRIES=3 的重试，每失败一次就把该主机挪到队尾（Pool.ban，留最后一台兜底），重试用尽才把异常抛给调用方。这个重试对池内所有取数都生效，不只网页。
2. 看板连续错 5 次会自动重跑一遍 host 健康探测（note_error 起后台线程），长跑进程不会再抱着死表不放。

实测：改前 40 次连打 40 错，改后 0 错；三只票各 6 次也全通。看到这条报错先确认服务在跑；仍高频出现就重跑 python tdx_dash.py（进程重启会重探主机），别只刷新页面。


### 11.19 l2 子命令：逐笔增量落盘与回放

逐笔是 3 秒粒度的流水，一只票一天几千条。要长期用就得先落盘，别每次现拉。

~~~
python tdx_realtime.py l2 fetch --codes 1:601881 --count 3000 --out build\ticks   # 往回翻
python tdx_realtime.py l2 tail  --codes 1:601881 --seconds 60 --out build\ticks   # 只落新的
python tdx_realtime.py l2 replay --market 1 --code 601881 --speed 1 --out build\ticks
python tdx_realtime.py l2 check  --market 1 --code 601881 --out build\ticks
~~~

文件是 <根>/YYYYMMDD/<市场><代码>.<种类>.jsonl，一行一条，按 (分钟, 秒, 价, 量, 笔数) 去重，写入时按时间正序。tail 启动时从文件尾部回种已见签名，重启接着采，不重不漏；kind 进文件名，成交（order，0FC2）与不带秒的 tick（0FC5）不会互串。

check 两套判据：**窗内**（默认主判据，缺省 0.5%）拿文件的整段去比在线回包同一时间窗的量，盘中就能用；**日线**那条要对到当日整日量，盘中文件只覆盖一段必然偏小，只作参考。任一条过就算通过。

判据要点：服务端对 0FC2 一次最多回五百多条（实测 800 与 3000 都只回 561），往回翻靠 start 递增，单次别指望拿全；日线记录是日期倒序、0 是最新那根。

主站分页上限意味着 fetch 的 count 只是上限，取不满就是没有了，别当失败。


### 11.20 退市股：fill 的空结果、--from 拷贝、delisted 清单（42 章）

主站只服务在册证券，退市代码（300431、300104 这类）日线一律 0 条，25 台主机与 13 个 category 全试过，码表里也没有。别以为是站的问题、别指望换站。

fill 遇到 0 条的处置：

- 有旧文件就原样保留，只报一行“无数据（退市或已摘牌）”，不会写成 0 字节
- 没有任何文件就不造空文件
- 末尾汇总带“N 只无数据”，一眼能看出这轮有几只是白问

三条配套命令：

~~~
python tdx_bootstrap.py delisted C:\旧安装 --out build\delisted.txt     # 本地有、码表没有的代码
python tdx_bootstrap.py fill 新树 --market 0 --codes 301139 --from C:\旧安装   # 离线拷 .day，按日合并
python tdx_bootstrap.py sync 新树 --codes 300431                        # 权息还能取，因子照算
~~~

判据要点：差集里不全是退市，转债到期、ETF 摘牌、B 股、指数都会命中；代码前导零会被 PowerShell 当数字数组吃掉（000016 变 16），fill 与 sync 已统一按六位补零（norm_codes），别处调用要自己补。



### 11.21 分钟线：kind 与 24k 条硬顶（43 章）

分钟族有两道闸：单只单周期约 24000 条，以及滚动只留约两年。1 分钟先撞条数闸，只有四个半月；5/15/30 分钟都停在 2024-10 下旬（实测 23904/7920/3976 条，起点 10-21/10-24/10-22）。25 台主机结果一致，换站无解；日线不受影响。2015 年起的分钟只能靠旧备份 .lc1、客户端盘后数据下载或第三方源补齐。

~~~
python tdx_bootstrap.py fill 树 --market 1 --codes 600000 --days 0 --kind min
python tdx_bootstrap.py fill 树 --market 1 --codes 600000 --days 0 --kind 5min
~~~

- kind：day（cat 9，lday/.day）、min（cat 7，minline/.lc1）、5min（cat 0，fzline/.lc5）、15min/30min/60min（cat 1/2/3，fzline/.lc15/.lc30/.lc60）
- days 给 0 才翻到底；给数字就按条数收（单包上限 800）
- 合并键是 (日, 时, 分)，重跑幂等；单段失败就地重连重试三次；0 条保留旧文件、不造空文件
- 分钟记录 32 字节，打包日在 bit11 以上（年-2004），低 11 位是 月*100+日

校验口径：把某日的分钟量求和与当日日线量比，应等于 1.0000（600000 五个交易日实测都是 1.0000）。改了分钟解析或打包必须重跑这个比值。


### 11.22 价量之外有什么、PB 怎么算（44 章）

服务端**没有** PB/PE/换手率这类现成字段，都是客户端拿财务与当日价自己算的。能拿的是：逐笔（含方向与笔数）、当日 240 点分时、五档与内外盘、0x000F 的股本与分配、码表里的每手股数与小数位，以及本地 base.dbf 的 40 个财务字段（总资产、净资产、净利润、主营收入、股东人数、行业、总股本、流通 A 股等）。

财务只有"现在"一期，历史某天的每股净资产取不到，所以按天的估值只能自己攒：

~~~
python tdx_bootstrap.py archive C:\安装目录          # 存 db\base\YYYYMMDD.dbf
python tdx_bootstrap.py archive 树 --day 20261105   # 补存指定日
~~~

归档的是 base.dbf 整份，同一天重复跑只要大小一致就不重写。自造 base.dbf 的财务字段是 0，必须用真安装的副本。

派生量写法：PB = 价 ÷ (净资产/总股本)，PE = 价 ÷ (净利润/总股本)，换手率 = 当日量 ÷ 流通 A 股。财务包金额字段仍未解（35/36/39 章），解开才能补历史估值。


### 11.23 ETF 与基金的市场判定（45 章）

ETF 不用额外接口，053E 一样给，问题只出在市场判定。老规则"6/5 算沪、其余算深"会把 1 开头的沪市 ETF 判成深市、把 15 开头的深市 ETF 判成沪市，回包变成全零记录。

现在的 guess_market：6/9 沪、0/2/3 深、5 沪、11/13 沪、12/15/16/17/18 深、8 与 920 北交所。冒号写法优先；显式市场与前缀不符先提示，若首轮价为零再按前缀改判重取，命中记录带 remapped。

~~~
python tdx_realtime.py once --codes 589020
python tdx_dash.py --port 8811        # 或直接开 http://127.0.0.1:8811/?c=1:589020
~~~

实测：589020 回 8.82、159915 判 0 回 25.24、511880 判 1 回 1009.61。改判只救市场写错，代码本身写错仍是全零；4 开头的老三板按沪市兜底。


### 11.24 价格分母按证券类型分三种（46 章）

固定系数一定会错。三个通道口径不同【实连】：053E 快照是 10 的 小数位 次方（股票 1/100、ETF 1/1000、转债 1/10000），052D K 线一律 1/1000，0FC2/0FC5 逐笔股票 1/100、ETF 与转债 1/1000。小数位取自码表 0x0450 记录偏移 20。

小数位表按市场整体拉一次，落 db\precision.json 缓存一周（首拉约 20 秒、之后 5 秒内），过期自动重拉。

码表这族命令的写法与快照不同，抄错就超时：三条握手要一条一条发、每条读应答；声明长度要把 2 字节 commtype 算进去；一页 1000 条、页间 0.05 秒、失败换机重试五次。表按市场加载一次进内存，失败按 100 兜底。

校验：python tdx_realtime.py once --codes 600036,589020,111024 --mode l1 应给 42.63 / 0.882 / 218.455，与同日 K 线收盘一致。改任一口径都要重对这三只的三通道。

成交量单位另有一层：快照是手，K 线是股（ETF 是份、转债是张）。算换手率要乘每手股数，别拿两边直接除。
## 12. 变更须知

源码里的中文注释是给 review 用的：改了帧字段、密钥表偏移、记录布局，注释要跟着改，别留过期说明。

1. 改 CATEGORY、CT_*/CAT_*/VER_* 必须整组一起改，并重跑 smoke。
2. 改 KEY_HEX 等于换密钥，旧 gbbq 全部读不出；改完先跑 200 条往返。
3. 改 ratio 公式后必须重跑 001207 全段比对（最大跳空应回到 8.91%）。
4. 改 T0002_DIRS 或 REF_DIRS 后重跑 init 并实测起窗，别只看 verify。
5. 改 _CFG_B64 前先解压成 JSON 校对，分片不可手工拼接。
6. 新增字段先加进 SCHEMA 的建表语句，再改 upsert，最后改 export，顺序反了会静默丢列。

## 13. 从零自举教程（不依赖 exe）

目标：只用本仓库的脚本，从空目录造出一棵能连行情、能起窗的客户端树。全程不装官方安装器、不联网下载安装包；唯一的外部依赖是可选的 --ref 参照目录（提供 exe 与 chrome 等二进制，exe 是 VMP 的造不出来）。

下面把 $root 当目标目录，示例用 data\tut；$tdx 当参照安装目录，例如 D:\neptune。

### 13.1 先看清单

~~~powershell
python tdx_bootstrap.py plan data\tut
~~~

打印将创建的目录数与几个样例，并说明会写 connect.cfg、gbbq 占位、bootstrap.json。此时什么都没落盘，先确认目标路径。

### 13.2 造骨架

~~~powershell
python tdx_bootstrap.py init data\tut --ref $tdx
~~~

产出 72 个目录、connect.cfg（25 分区、站点由参照的种子填）、gbbq 空占位、bootstrap.json 清单，并按白名单复制 148 个静态件。期望末行是"已生成 ... 目录 72"。

不想碰安装目录也能跑：去掉 --ref，得到 72 目录、4 站点、0 复制的纯脚本树，只是没有 exe。

### 13.3 造证券主表

~~~powershell
python tdx_bootstrap.py base data\tut
~~~

从码表抓在册证券，写成 T0002\hq_cache\base.dbf（约 5 万行、25 MB）。这一步联网、按市场分页，耗时几十秒；中途撞上连上即关的主机会自动换机重试。

### 13.4 补权息

~~~powershell
python tdx_bootstrap.py sync data\tut --market 1 --limit 50
python tdx_bootstrap.py sync data\tut --market 0 --limit 50
~~~

从 0x000F 抓除权除息写 gbbq，断点续传，重跑只补缺的。默认并发 64，失败会自动补抓三轮。要全市场就把 --limit 去掉慢慢跑。

### 13.5 补行情

~~~powershell
python tdx_bootstrap.py fill data\tut --market 1 --codes 600000,600036 --days 800
~~~

按日期合并写入 vipdoc\sh\lday。攒历史就每天跑一次；--days 给多大取多少，最多 800。

### 13.6 校验

~~~powershell
python tdx_bootstrap.py verify data\tut
~~~

期望"结论: 通过"。它逐项查目录、配置分区与站点、gbbq 长度自洽、主表行数与快照日；有缺项会列出来并返回 1。

### 13.7 起窗

~~~powershell
Start-Process data\tut\TdxW.exe -WorkingDirectory (Resolve-Path data\tut)
~~~

等 20-30 秒看有没有主窗口。判据必须是**窗口**，不是进程存活——缺 chrome/ 时进程活着、日志只有版本号、永不建窗。

### 13.8 常见偏差

| 现象 | 原因 | 处置 |
|---|---|---|
| init 报目标非空 | 目录里已有文件 | 换目录，或先清空 |
| verify 报 UserHost.IPAddress 为空 | 配置没带站点种子 | 用 --ref 重跑 init，或 regen |
| 进程在但没有窗口 | 缺 chrome/ 等程序目录 | 带 --ref 重新 init（会补复制） |
| base 行数远少于预期 | 主站限流或站点全挂 | 换站后重跑，别连打 |
| sync 失败一堆 | 并发过高 | 降到 --jobs 16 再跑 |

### 13.9 这一章的边界

自举出来的树能连行情、能起窗，但**交易通道仍要登录**（checkuser 密文未解，见 32 章），L2 权限同理。树里的 gbbq 与 vipdoc 都是自己抓的，来源可控；base.dbf 是现造，财务字段为 0，要真值得靠财务包（35、36 章）。

## 14. 使用样例（照抄即可）

下面每条都是能在仓库根直接跑的。命令都写 python，Windows 用 py 也一样；输出里有中文，控制台乱码时先 chcp 65001 或加 PYTHONIOENCODING=utf-8。

### 14.1 先确认环境

~~~powershell
python -m py_compile tdx_local.py tdx_client.py tdx_adjust.py tdx_factors.py tdx_bootstrap.py tdx_realtime.py tdx_dash.py
python tdx_client.py smoke --market 1 --code 600000
python tdx_realtime.py probe-l2
~~~

期望：编译 0；smoke 连通并回包；probe-l2 输出 l2 true。任一条红先看第 11 章，别往下走。

### 14.2 一份实时快照

~~~powershell
python tdx_realtime.py once --codes 600036
python tdx_realtime.py once --codes 600036,000001,589020
~~~

一行一只的 JSON，字段含价、前收、开高低、量额、五档。批量一次最多 80 只，多了拆包并发走 rtt 那套。

### 14.3 逐笔（带 3 秒时标）

~~~powershell
python tdx_realtime.py once --codes 600036 --l2-orders
~~~

给的是最近几十笔。要连续流水别用 once，用增量落盘：

~~~powershell
python tdx_realtime.py l2 tail --codes 1:600036,0:000001 --seconds 60 --out 数据目录
python tdx_realtime.py l2 replay --market 1 --code 600036 --speed 60 --out 数据目录
python tdx_realtime.py l2 check  --market 1 --code 600036 --out 数据目录
~~~

落盘是 <根>/YYYYMMDD/<市场><代码>.order.jsonl，一行一笔，按时间正序；tail 重启接着采不重放。check 的窗内偏差应接近 0。

### 14.4 当日分时

~~~powershell
python tdx_client.py minute --market 1 --code 600036
~~~

240 个点，一分钟一个。要落文件用 --out。跨日分时见 14.6。

### 14.5 看板

~~~powershell
python tdx_dash.py --port 8811
~~~

开 http://127.0.0.1:8811/ ，或直接双击 dash.html（file 协议也能用，服务没起会提示）。换票用顶栏，或带参数 http://127.0.0.1:8811/?c=1:589020 直接进 ETF。

### 14.6 历史行情

~~~powershell
python tdx_bootstrap.py fill 数据树 --market 1 --codes 600036 --days 800        # 日线，最多 800 根
python tdx_bootstrap.py fill 数据树 --market 1 --codes 600036 --days 0 --kind min   # 1 分钟，翻到底
python tdx_bootstrap.py fill 数据树 --market 1 --codes 600036 --days 0 --kind 5min  # 5 分钟
python tdx_bootstrap.py fill 数据树 --market 1 --codes 600036 --days 0 --kind 30min # 30 分钟
~~~

日线能到 2003；分钟族只留约 24000 条且滚动两年（1 分钟 4.5 个月，5/15/30 分钟到 2024 年 10 月，见 43 章）。**取指定日期**就是按 days 往回取够，再从落盘文件里按日期过滤——记录是定长 32 字节，日期在头 4 字节（分钟是 u16 打包日）。

### 14.7 ETF 与转债

~~~powershell
python tdx_realtime.py once --codes 589020,159915
python tdx_realtime.py once --codes 111024
python tdx_bootstrap.py fill 数据树 --market 1 --codes 589020 --days 0 --kind min
~~~

市场会自动按前缀判（5 沪、15 深、11 沪），写错市场会提示并按前缀改判。价格口径三种：快照按证券小数位、K 线一律 1/1000，别拿两边的价直接比。ETF 与转债的量单位是份与张，不是股。

### 14.8 退市股与旧历史

~~~powershell
python tdx_bootstrap.py delisted C:\旧安装 --out build\delisted.txt
python tdx_bootstrap.py fill 数据树 --market 0 --codes 301139 --from C:\旧安装
python tdx_bootstrap.py fill 数据树 --market 1 --codes 589020 --kind min --from C:\旧安装
~~~

delisted 列出“本地有、主站没有”的代码（退市、到期转债、摘牌 ETF 都会中）；--from 只拷不联网，合并写不覆盖已有记录。

### 14.9 权息与复权

~~~powershell
python tdx_bootstrap.py sync 数据树 --market 1 --codes 600000 --limit 5
python tdx_adjust.py dump --gbbq 数据树\T0002\hq_cache\gbbq --out build\ev.csv
python tdx_client.py bars --category 4 --market 1 --code 600000 --adjust hfq
~~~

sync 断点续传，重复跑只补缺的；复权用 hfq 或 qfq，因子缺失会明说并按不复权输出。

### 14.10 估值（PB/PE）

~~~powershell
python tdx_bootstrap.py archive C:\安装目录
python tdx_bootstrap.py archive C:\安装目录 --day 20261105
~~~

每天存一份 db/base/YYYYMMDD.dbf，之后按 PB = 价 ÷ (净资产/总股本) 回算。自造 base.dbf 的财务字段是 0，要用真安装的副本。

### 14.11 换站

~~~powershell
python tdx_local.py hosts-update --cfg connect.cfg --path 清单目录
python tdx_local.py sitecfg-check --path connect.cfg
~~~

只重写 HQHOST/TRYHQHOST/INFOHOST/DSHOST 四段，写前自检、留 .bak。

### 14.12 自举一棵能起窗的树

~~~powershell
python tdx_bootstrap.py init 新树 --ref C:\neptune
python tdx_bootstrap.py base 新树
python tdx_bootstrap.py verify 新树
~~~

要起窗必须有 chrome/ 等程序目录，所以 --ref 指安装目录；纯脚本自举按第 13 章做。


### 14.13 沪深指数与板块指数

指数也是证券，接口一样通，但有三处与个股不同：代码歧义、没有五档、K 线一次只回一根。

~~~
python tdx_realtime.py once --codes 1:000001          # 上证指数
python tdx_realtime.py once --codes 0:399001          # 深证成指
python tdx_realtime.py once --codes 1:880008          # 板块指数（这里是 880008）
python tdx_client.py bars --market 1 --code 000001 --category 4 --count 5
python tdx_client.py minute --market 1 --code 000001
python tdx_client.py tick --market 1 --code 880008 --count 5
~~~

实测：000001 回 3655.83、399001 回 11197.98、880008 回 1329.16；日线、分时 240 点、逐笔都出数，逐笔方向多为 2（中性），量是全市场合计。

**代码歧义**：裸写 000001 会按前缀判成深市，也就是平安银行；指数必须带市场（1:000001 或 sh000001）。399xxx 属深市，880/881 属沪市。

**没有五档**：指数的 bid1/ask1 与量是残值，别当真；看价、量、额、开高低就够。要盘口深度得用个股。

**K 线与分时一次一根**：指数请求 count 再大也只回一根，所以 fill 与 minute 都改成按根往后挪。分时 240 点等于 240 次请求，比个股慢一个量级，批量取指数别开大并发。

板块指数清单在安装目录的 T0002\hq_cache\tdxzsbase.cfg：管道分隔，第 1 列市场、第 2 列代码，880/881 前缀是板块，共 1072 条；个股码表里没有它们，所以 delisted 这类按码表比对的命令不会碰到板块。

## 15. 报错对照

| 报错 | 真因 | 处置 |
|---|---|---|
| connect.cfg 与 newhost.lst 都没有可用主机 | 目标树没建过、没有站点表 | 先 init，或 --cfg 指别的配置 |
| RtdError: 连接被关闭 | 撞上"接受连接后立刻关闭"的主机 | 已内建换机重试；仍多就重启进程重探主机 |
| TdxError: commtype 不匹配 | 帧头或声明长度写错 | 对照 46.1 与第 3 章，别凭记忆改 |
| 回包价量全零 | 市场写错（例如 0:589020） | 写对市场，或让前缀自动判 |
| 代码只按末位匹配、回包恒带 600839 | 快照尾巴 0C 01 放错位置 | 尾巴必须排在证券表之后 |
| 分钟只回几十条就空 | 撞到 24000 条保留闸 | 用 5/15/30 分钟，或从旧树搬 |
| 退市股 0 条 | 主站库只留在册证券 | delisted 找旧备份，fill --from 拷 |
| Python 里文件名带中文报错 | 控制台编码 | chcp 65001 或 PYTHONIOENCODING=utf-8 |
| 看板显示"连不上服务" | 服务没起 | 先 python tdx_dash.py --port 8811 |


## 附录 A. 命令一览

一行一条，只写用途；参数细节看对应章节。全部在仓库根执行，路径按自己的机器改。

**看行情（tdx_client.py / tdx_realtime.py）**

| 命令 | 用途 | 样例 |
|---|---|---|
| client smoke | 连通自检，回 5 笔逐笔 | python tdx_client.py smoke --code 600000 |
| client count / codes | 证券数 / 码表分页 | python tdx_client.py codes --market 1 --count 5 |
| client bars | K 线，--category 4 日线、7 分钟 | python tdx_client.py bars --code 111024 --category 4 --count 5 |
| client minute | 当日分时 240 点 | python tdx_client.py minute --code 589020 |
| client tick | 逐笔（无秒） | python tdx_client.py tick --code 111024 --count 5 |
| client local | 读本地行情文件 | python tdx_client.py local --code 600036 |
| client xdxr / login / info-hello | 除权除息 / 登录口探测 / 资讯口问候 | python tdx_client.py xdxr --code 600000 |
| realtime once | 一份快照，加 --l2-orders 出带秒逐笔 | python tdx_realtime.py once --codes 589020 |
| realtime probe-l2 | 本服是否给逐笔 | python tdx_realtime.py probe-l2 |
| realtime rtt | 量往返 | python tdx_realtime.py rtt --codes 600000 --n 3 |
| realtime run | 按订阅表轮询 | python tdx_realtime.py run --watch watch.txt |
| realtime l2 fetch / tail | 历史逐笔 / 实时落盘 | python tdx_realtime.py l2 tail --codes 1:600036 --seconds 30 --out build\b1 |
| realtime l2 replay / check | 回放 / 落盘量核对 | python tdx_realtime.py l2 check --market 1 --code 600036 --out build\b1 |
| dash | 起看板 | python tdx_dash.py --port 8811 |

**造树与数据（tdx_bootstrap.py）**

| 命令 | 用途 | 样例 |
|---|---|---|
| plan / init / regen | 预览 / 造树 / 重造配置 | python tdx_bootstrap.py init data\tut --ref $tdx |
| base | 生成证券主表 | python tdx_bootstrap.py base data\tut |
| sync | 抓权息，断点续传 | python tdx_bootstrap.py sync data\tut --limit 50 |
| fill | 抓行情落盘，--kind 选周期 | python tdx_bootstrap.py fill data\tut --market 1 --codes 600036 --days 800 |
| fill --from | 从旧树拷，不走网络 | python tdx_bootstrap.py fill data\tut --codes 301139 --from C:\旧安装 |
| delisted | 列“本地有、主站没有”的代码 | python tdx_bootstrap.py delisted C:\旧安装 --out build\d.txt |
| archive | 按日存 base.dbf，供估值回算 | python tdx_bootstrap.py archive C:\安装目录 |
| login / verify | 探登录口 / 校验树 | python tdx_bootstrap.py verify data\tut |

**权息与因子（tdx_adjust.py / tdx_factors.py）**

| 命令 | 用途 | 样例 |
|---|---|---|
| adjust check | 校验 gbbq | python tdx_adjust.py check --gbbq $tdx\T0002\hq_cache\gbbq |
| adjust dump / factors / apply | 导出事件 / 算因子 / 套复权 | python tdx_adjust.py factors --gbbq $tdx\T0002\hq_cache\gbbq --market 1 --code 600000 |
| factors import / sync / export / gbbq | 入库 / 线上同步 / 导出 CSV / 回写 gbbq | python tdx_factors.py export --db factors.db --out build\f.csv |

**配置与升级（tdx_local.py）**

| 命令 | 用途 | 样例 |
|---|---|---|
| sitecfg-parse / -check / -build / -defaults | 解析 / 自检 / 生成 / 默认值 | python tdx_local.py sitecfg-check --path connect.gen.cfg |
| hosts-update | 按清单换站（--path 目标 cfg） | python tdx_local.py hosts-update --path connect.cfg --from T0002\newhost.lst --dry |
| master / industry / index / snapshot / blocks / files | 主表 / 行业 / 指数 / 横截面 / 板块 / 文件清单 | python tdx_local.py --root $tdx master --limit 5 |
| autoup-list / autoup-get / autoup-probe | 升级清单 / 取整包 / 探包名 | python tdx_local.py autoup-probe --base http://<站>/autoup/ a.zip b.zip |

市场写法：可省（按前缀判），要精确就写 市场:代码，例如 1:000001 是上证指数、0:000001 是平安银行。指数与板块没有五档、K 线一次一根（见 14.13）。
