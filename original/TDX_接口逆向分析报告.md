# 通达信「海王星」v11.68（银河证券版）行情接口逆向分析报告

> 目标：C:/neptune/TdxW.exe
> 目的：纯静态还原行情获取链路（日线 / 分钟线 / 逐笔 tick / 快照 / L2），供自建量化程序复用其协议与本地数据格式。
> 方法：IDA Pro 9.4 + IDALib（headless）+ Hex-Rays；PE 结构解析、字符串与交叉引用、常量扫描、本地数据文件字节级实证；公开协议实现（pytdx）作为对照而非依据。
> 约定：每条结论标注证据级别——【静态】由指令/数据交叉引用直接读出（给出 VA = 镜像基址 0x400000 + RVA）；【文件】由磁盘文件字节实证；【参考】仅来自公开项目，已在报告内与二进制常量互校；【推断】静态可见但语义需运行时确认。
>
> 本报告全程未运行 TdxW.exe，无动态调试、无抓包；唯一的外部输入是对主站 7709 端口的明文报文实测，集中在第 10 章，并已回填到第 3 章。
>
> 阅读顺序：要快速上手看 0 → 3 → 10 → 6 → 7；要做离线数据看 5 与 11；要接 L2 / 批量报价看 4 与 12。

> 第 16 章讲 connect.cfg 的解析与从零重建（schema、生成器、校验、实机验证）。
> 第 17 章讲复权：现状是不复权、gbbq 的加密布局与因子算法、因子仓库怎么建。
> 第 18 章讲自举：从空目录造出可运行安装树，含 chrome/ 这个静默坑。
> 第 19 章讲权息自举：服务端 0x000F 抓除权除息、事件库落库、gbbq 加密回写。
> 第 20 章讲五个缺口的收口：base.dbf 自造、登录口定位、缩股权证折算、断点续传、冗余清理。
> 各脚本的常量、函数契约、命令与排错见配套的 TDX_工具链使用手册.md。
> 新会话接手先读 AGENTS.md：环境、铁律、坑、已核实事实、交付标准都在那里。

> 第 11–15 章是第二轮深挖：客户端安装目录里的证券主表、行业分类、板块成分、期权链、期货规格、交易规则、财务清单与大数据 HTTP 通道，全部离线可解析，并已随附提取器 tdx_local.py。

---

## 0. 结论摘要（先看这段）

1. 行情通道有两套，都跑在同一份 connect.cfg 的主机表上：
   - 老式二进制协议（端口 7709，TdxAsioComm.dll 承载）：报头固定 10 字节，形如 0C <cmd> 08 <ver> <ver2> 01 <len:u16> <len:u16>，随后 body 以 commtype(u16) 开头。K 线、分时、分笔、快照、板块、F10 全走这条。
   - 新式 BDHQ 通道（同一 socket；body 头换成 0x2572 的批量结构，或直接走 JSON 文本 JsonReqBDHQData / JsonReqBDSearch）：批量报价、逐笔成交与委托（L2）、分时明细、条件筛选、增量码表。
2. 不必启动客户端也能取数：7709 是明文 TDX 行情服务，报文结构已逐字段还原并**已实连跑通**（第 10 章给了完整的发/收字节记录）；vipdoc 下的 .day/.lc1/.lc5 是客户端落盘的未加密定长记录文件，32 字节/条，可直接读。
3. 本地数据格式已字节级确认：.day = u32 YYYYMMDD, u32 开/高/低/收(×100), float 成交额, u32 成交量, u32 保留；.lc1 = u16 打包日期, u16 当日分钟数, float 开/高/低/收, float 成交额, u32 成交量, u32 保留。日期打包：年 = date/2048+2004，月 = (date%2048)/100，日 = (date%2048)%100。
4. 价量在协议里是压缩整数：价格是带符号可变长整数（首字节 bit6 = 符号，bit7 = 续位，低 6 位起算，7 bit 步进），单位 1/1000 元；成交量是 IEEE 风格 4 字节对数浮点。K 线/分时记录的价格先差分编码，再增量累加还原。
5. 复权因子在 T0002/hq_cache/gbbq（193719 条 × 29 字节），证券基础表 base.dbf（7988 条 × 481 字节，DBF 格式，含总股本/流通股本/财务字段）。
6. 安装目录里有一整套离线资产（11 章）：7988 条证券主表含股本结构、股东户数与上市日期，5668 条行业映射，958 个板块 114899 条成分，1208 条期权链，99 条期货合约规格，193719 条权息，交易规则常量，400+ 大数据表清单与 HTTP 云通道。无需连服务端即可构成 universe、因子底表与衍生品链。
7. 自建程序有 3 条路径（第 6 章）：直接实现 7709 协议（推荐，实时与历史都可取）、走 BDHQ 通道（L2/逐笔更强）、纯读本地 vipdoc（零风险但滞后）。

---

## 1. 样本与静态分析环境

### 1.1 指纹【文件】

| 项 | 值 |
|---|---|
| 路径 | C:/neptune/TdxW.exe |
| 大小 | 17,945,520 字节 |
| SHA-256 | 2773b723f015a293c04314f87f55d5ba8991e4d77374aac879e1a356b75f010f |
| MD5 | a7fa9792be7a831588a62866487f7ed4 |
| 类型 | PE32 i386 GUI，MFC 100，MSVC 10 风格 |
| 镜像基址 | 0x400000（下文 VA 均为 基址 + RVA） |
| 入口 | RVA 0x140C284 → VA 0x180C284，位于 .vmp1，VMProtect 加壳入口 |

节表：

| 节 | RVA | 虚拟大小 | 特征 | 说明 |
|---|---|---|---|---|
| .text | 0x1000 | 0x8F0000 | 0x60000020 | 主代码，约 9 MB |
| .rdata | 0x8F1000 | 0x136000 | 0x40000040 | 字符串与常量表 |
| .data | 0xA27000 | 0xB70000 | 0xC0000040 | 全局变量 |
| .vmp0 | 0x1197000 | 0x1CF000 | 0x60000060 | VMProtect 段 |
| .vmp1 | 0x1366000 | 0x213000 | 0x60000060 | VMP 桩与导入描述符 |
| .rsrc | 0x149F000 | 0x308000 | 0x40000040 | 资源 |

【静态】.vmp1 中存在完整的 IMAGE_IMPORT_DESCRIPTOR 副本，例如 RVA 0x142F8A0 处 dd rva ?MakeUserCommModule@@YAPAVVUserComm@@XZ 与 dd rva aViewthemDll。VMP 把关键调用改写成 call ds:<.rdata 伪 IAT 槽>，伪 IAT 槽值形如 0x800005A8（VMP 导入 ID），因此 Hex-Rays 里大量调用显示成 MEMORY[0x8000013C](...)。这是本报告多数结论改以反汇编取证的原因。

### 1.2 分析手段【静态】

- headless IDALib 装载后对整段 .text 做线性扫描，补齐 27,436 个函数边界（原生自动分析只有 174 个，因为入口在 VMP 段），之后交叉引用才稳定可用。
- 关键定位手法：字符串交叉引用 → 所在函数 → 反汇编读结构体偏移；常量立即数扫描（端口、commtype）；PE 数据目录与节表解析。
- 复现脚本：C:/projects/reverse/ida_shim.py（IDALib + JSON-RPC 覆盖层）、q.py（查询客户端）、strings.txt（36.7 万条 GBK 字符串及地址）。

---

## 2. 连接模型

### 2.1 主机表来自 connect.cfg【文件】

C:/neptune/connect.cfg 为 INI：

~~~
[HQHOST]              ; 行情主站，实测 12 条，端口 7709，含 IPv6 直连地址
HostNum=12
Host01=...:7709
[TRYHQHOST]           ; 备用/试连主站
[INFOHOST]            ; 资讯主站，端口 7711
[WTHOST]              ; 委托网关，端口 7708
[Other]
PreName=海王星金融终端v3.0-中国银河证券
Corp=银河证券
AutoConnectIn=1
~~~

同目录另有 T0002/usercomm.ini、syscfg.json、bigdata*.dax 与 hq_cache 缓存。

### 2.2 配置解析实现【静态】

主站数量读取（段 HQHOST、键 HostNum），字符串 HQHOST @ VA 0xD22918，TRYHQHOST @ 0xD24560，同函数内按顺序处理多个段。交叉引用点：0x681138、0x69967C、0x69BCCA、0x78DF53、0x7B2401、0x7FB1CE、0x80839C。函数 sub_6995F0 是其中一处读取器：

~~~
savedregs = "HQHOST";
v17 = GetProfileInt("HQHOST", "HostNum", 0);   // 上限 150
if ( v17 > 0 ) {                               // 逐条读 Host%02d / Port%02d / Weight%02d
   ...
~~~

端口默认值以立即数压栈【静态】：

| 端口 | 指令 | 站点 VA |
|---|---|---|
| 7709 行情 | push 1E1Dh | 0x6954BC、0x6956EE、0x695AAA、0x695C7C、0x69685E、0x699772 |
| 7711 资讯 | push 1F1Eh | 0x696A6D、0x696CDE、0x699C05、0x699E52 |
| 7708 委托 | push 1C1Eh | 0x69A296、0x69A734、0x69ABD2、0x69AF02、0x7F92B6 |

0x696830 起的完整取值序列：

~~~
69683b: push offset aTqhost      ; "TQHost"
696840: call <GetProfileString>
696843: lea  eax, [ebp-788h]
696849: push offset aPort02d     ; "Port%02d"
69684e: push eax
69684f: call <CString::Format>
696857: ...
69685e: push 1E1Dh               ; 缺省端口 7709
696869: call <CString ctor>
696870: push offset aTqhost
696875: call GetProfileInt        ; GetProfileInt("TQHost", "Host%02d", 7709)
696889: push offset aWeight02d    ; "Weight%02d" —— 主机权重，用于选优
~~~

### 2.3 传输层【静态】

TDXAsioComm.dll 只导出三样：?MakeUserCommModule@@YAPAVVUserComm@@XZ（工厂）、?DelUserCommModule@@YAXAAPAVVUserComm@@@Z、以及 VUserComm / CUserComm 的 vtable 数据符号。TdxW.exe 通过 RVA 0x142F8A0 的导入描述符引用工厂；CUserComm vtable 位于该 DLL RVA 0x9C248，19 个有效槽（0x10015540 … 0x100046B0）。实际收发为 WS2_32 的 WSASend / WSARecv（调用点 0x100177D0、0x100204F0、0x10021590，单 WSABUF，lpOverlapped 可空 → 同步/异步两条路）。

【推断】抽象层与 VMP 使「业务组包 → DLL 发送」之间多一层虚调用；业务组包全在 TdxW.exe 内部，DLL 只搬字节，不下发任何帧格式。

【实测补充】该服务端**每连接只服务一条业务请求**：响应给出后连接保持静默，客户端再发第二条即被断连；四个不同主站（123.125.108.101 / 114.141.177.118 / 27.151.2.90 / 202.100.166.12）表现一致。所以自建客户端应按「一请求一连接」实现，实测约 3 请求/秒；也解释了大客户端为什么要开多条连接。

### 2.4 限流与保活【静态】

各请求函数以 __alloca_probe 开大栈帧（单帧 0x1644，见 sub_7229E0）。发送器先查连接标志 [esi+1454h] 与 [esi+1159Bh]，失败即 or eax,-1 返回（0x722A1F）。每连接维护在途计数 [esi+14h]，上限 5（0x722AE9 cmp [esi+14h], 5），超出则暂缓入队并回退。这就是客户端自带的并发窗口，可直接照搬（见 6.3）。

---

## 3. 老式二进制协议（端口 7709）

### 3.1 帧结构【静态 + 参考互校】

发送帧由公共发送器 sub_7229E0（VA 0x7229E0）构造。关键指令序列：

~~~
722A34: mov  eax, [esi+8]          ; 连接阶段
722A37: cmp  eax, 1
722A3A: jz   loc_722A57
722A3C: cmp  eax, 2
722A3F: jz   loc_722A57
722A47: cmp  eax, 28h
722A49: jl   loc_722A57
722A4B: mov  al, [ebp-141Ch]       ; 业务帧
722A51: and  al, 0FCh
722A53: or   al, 0Ch               ; 首字节 = 0x0C
722A55: jmp  loc_722A61
722A57: mov  al, [ebp-141Ch]       ; 心跳/空载帧
722A5D: and  al, 0F1h
722A5F: or   al, 1                  ; 首字节 = 0x01
722A61: mov  cx,  word_128FC4C      ; 帧头 +3..+4
722A69: mov  dl,  byte_E34232       ; 帧头 +5
722A70: mov  ax,  word_128FC44      ; 帧头 +1..+2
722A80: mov  [ebp-141Bh], ax
722A8E: mov  [ebp-1419h], cx
722A95: mov  [ebp-1417h], dl
722A9B: mov  [ebp-1416h], di        ; +6..+7 = 载荷长度
722BA5: mov  [eax+8], di            ; +8..+9 = 载荷长度（重复）
722BB3: ...                          ; +10 起 memcpy 载荷
~~~

由此得到固定 10 字节报头，其后是载荷：

| 偏移 | 长度 | 内容 | 说明 |
|---|---|---|---|
| +0 | 1 | 0x0C / 0x01 | 业务帧 / 心跳帧 |
| +1 | 2 | word_128FC44 | 低字节是请求类别（K 线 01、分笔 17、分时 1B），高字节 08 |
| +3 | 2 | word_128FC4C | 类别子版本（K 线 0164、分笔 0101、分时 0100） |
| +5 | 1 | byte_E34232 | 恒 1 |
| +6 | 2 | 载荷长度 u16 | |
| +8 | 2 | 载荷长度 u16 | 与 +6 相同（冗余校验） |
| +10 | n | 载荷 | 首字段是 commtype(u16) |

这三个帧头全局变量的写入点共 60 余处，集中在 0x44A000–0x44D500 与 0x556000–0x569000，即各业务组包函数；写入模板形如：

~~~
44A629: push 6Ch
44A62A: mov  word_128FC44, ax     ; ← 类别
44A630: mov  word_128FC4C, cx     ; ← 子版本
44A637: ...                        ; 随后填载荷、调用发送器
~~~

响应侧：先读 16 字节头，按 <IIIHH> 解出 zipsize（+12）与 unzipsize（+14）；两者不等时对后续 zipsize 字节做 zlib 解压，得到 body。【参考】pytdx 的 BaseParser 即按此实现，属该服务端既有行为。

### 3.2 commtype（载荷首字段）

commtype 是业务操作码，作为参数压栈传入（push <imm32> 立即数），全量扫描 .text 得到的取值集合：

- 0x500–0x517：板块、分类、F10 一类（0x500 出现 8 次，0x509 出现 12 次，0x517 出现 12 次）
- 0x51E–0x53F：行情取数核心族（0x520/0x52C/0x52D/0x52E/0x52F/0x530…0x53F）
- 0x544–0x54B：扩展品种
- 0xF00–0x1100 段：L2 逐笔与委托

已被静态确认语义的（函数内直接写进载荷）：

| commtype | 语义 | 证据 |
|---|---|---|
| 0x52D (1325) | K 线（日/周/月/分钟/分钟线各周期） | 0x491D9C、0x5B838C push；【参考】pytdx get_security_bars 同值 |
| 0x52E (1326) | 分时图（当日） | 0x48C17E、0x491D7B push，且 sub_6E4470 在 flag 分支写入 |
| 0x52F (1327) | 分笔成交（L1 快照切片） | sub_6E46B0 写入（a6==0 且某状态为 0 分支） |
| 0x532 (1330) | 分笔成交（L1 另一模式） | sub_6E46B0 另一分支 |
| 0x537 (1335) | 分时图（带明细/成交） | sub_6E4470 分支；0x5AEE72 push |
| 0x539 (1337) | 分时图（第三模式） | sub_6E4470 第三分支；0x4F432A push |
| 0xFC2 (4034) | L2 逐笔委托/成交（模式 A） | sub_6E46B0 a6!=0 且状态非 0 |
| 0xFC5 (4037) | L2 逐笔成交（模式 B，与 pytdx 完全一致） | sub_6E46B0 a6!=0 且状态 0 |

分时请求函数 sub_6E4470（VA 0x6E4470）载荷布局【静态】：

~~~
+0  u16 commtype（1337 / 1326 / 1335）
+2  u16 market        ← 取自证券对象 +0x118（= +280）
+4  代码串            ← loc_A1EFC8 调用的赋值
+13 u8  子类型 = dword_E47E84 % 256
随后 sub_7229E0(dword_11F5624) 发送
~~~

分笔请求函数 sub_6E46B0（VA 0x6E46B0）载荷布局【静态】：

~~~
+0  u16 commtype（4034 / 4037 / 1327 / 1330）
+2  u16 market        ← 取自证券对象 +0x118
+4  代码串
+10 u16 a4            ← 起始序号
+12 u16 a5            ← 条数
~~~

K 线请求（按 commtype 0x52D 与既有实现互校）模板如下，38 字节【参考，模板与二进制常量 0x0C/0x08/0x64/0x01/0x052D 全部吻合】：

~~~
偏移  长度  字段
 0    2    0x010C        固定
 2    4    0x01016408    固定（= 帧头 +1..+4 的 01 08 64 01）
 6    2    0x001C        载荷长度
 8    2    0x001C        同上
10    2    0x052D        commtype
12    2    market        0 深 / 1 沪 / 2 北
14    6    code          ASCII，不足补 0
20    2    category      周期，见 3.3
22    2    1             固定
24    2    start         起始偏移（自最新向前）
26    2    count         条数，单次上限 800
28    4    0
32    4    0
36    2    0
~~~

实抓样例（category=9，start=10）：

~~~
0C 01 08 64 01 01 1C 00 1C 00 2D 05 00 00 30 30 30 30 30 31
09 00 01 00 0A 00 00 00 00 00 00 00 00 00 00 00 00 00
~~~

### 3.3 category 周期表【参考】

| 值 | 周期 | 值 | 周期 |
|---|---|---|---|
| 0 | 5 分钟 | 7 | 1 分钟 |
| 1 | 15 分钟 | 8 | 1 分钟（同 7） |
| 2 | 30 分钟 | 9 | 日线 |
| 3 | 1 小时 | 10 | 季线 |
| 4 | 日线 | 11 | 年线 |
| 5 | 周线 | 12 | 除权日线 |
| 6 | 月线 | | |

### 3.4 记录解码【参考 + 与本地格式互证】

K 线 / 分时 body：u16 条数，随后逐条 4 字节时间 + 三个压缩整数（价格差分、成交量、成交额）：

~~~
price_int, pos = get_price(buf, pos)   # 可变长带符号；int 单位 1/1000 元
vol,       pos = get_price(buf, pos)
amount,    pos = get_price(buf, pos)
last += price_int
price = last / 1000.0
~~~

get_price 的实际位运算：首字节 b：int = b & 0x3F；若 b & 0x40 则取负；若 b & 0x80 则循环 pos+1 读下一字节，累加 (b & 0x7F) << 6,13,20…。

成交量另有 4 字节对数浮点编码 get_volume(ilog)：指数 = logpoint*2-0x7F，尾数按 hleax/ lheax/ lleax 三段按 2 的幂加权，hleax & 0x80 时尾数整体 ×2。

时间字段：category < 4、=7、=8 时为 u16 zipday + u16 分钟数（年 = zipday/2048+2004，月 = (zipday%2048)/100，日 = (zipday%2048)%100，时 = 分钟/60，分 = 分钟%60）；其余类别为 u32 YYYYMMDD + u32 分钟数。

> 上述编码与本地 .day/.lc1 的日期打包完全同构（第 5 章），可互相印证。

### 3.5 分点协议报文与握手【参考】

建立连接后必须先发三条初始化帧，服务端才受理业务请求：

~~~
1) 0C 02 18 93 00 01 03 00 03 00 0D 00 01
2) 0C 02 18 94 00 01 03 00 03 00 0D 00 02
3) 0C 03 18 99 00 01 20 00 20 00 0F DB D5
   D0 C9 CC D6 A4 A8 AF 00 00 00 8F C2 25 40
   13 00 00 D5 00 C9 CC BD F0 D7 EA 00 00 00 02
~~~

其中第 3 条载荷含 GBK 的终端标识串（D5 D0 C9 CC BD F0 D7 EA = 「站内金砖」族标识）。注意本二进制里 18 93 / 18 94 常以 DWORD 立即数 0x01009418、0x01009494 形式出现于 sub_6995F0 等函数，说明客户端是逐字段填帧而非整体常量，与上表不冲突。

三条帧的帧头词序与业务帧不同：第 1、2 条 +1..+2 为 0x1802、+3..+4 为 0x0093/0x0094；第 3 条为 0x1803/0x0099。服务端对前两条必有应答（能力块），对第 3 条不回应——发完要立即转入业务请求，不能阻塞读。详见第 10 章实测记录。

逐笔请求模板【参考；commtype 用 0x0FC5（L2 逐笔），本客户端 L1 分笔则用 0x52F/0x532，帧头一致】：

~~~
0C 17 08 01 01 01 0E 00 0E 00 C5 0F <market:u16> <code:6s> <start:u16> <count:u16>
~~~

分时请求模板【参考；commtype 用 0x051D，本客户端则用 0x52E/0x537/0x539，帧头一致】：

~~~
0C 1B 08 00 01 01 0E 00 0E 00 1D 05 <market:u16> <code:6s> <reserved:u32>
~~~

> 帧头第二字节（01 / 17 / 1B）才是真正的"请求类别"，commtype 只是载荷里的子类型；两者必须配对，否则服务端按类别解析会错。

### 3.6 其他常用业务 opcode（按类别第二字节）

| 第二字节 | 业务 | 帧头 +1..+2 / +3..+4 / +5 | commtype |
|---|---|---|---|
| 0x01 | K 线 / 证券列表 | 0x0801 / 0x0164 / 01 | 0x052D（K 线）、0x0450（列表） |
| 0x02 | 会话初始化第一帧 | 0x1802 / 0x0093 / 01 | 0x000D |
| 0x02 | 会话初始化第二帧 | 0x1802 / 0x0094 / 01 | 0x000D |
| 0x03 | 终端标识上报第三帧 | 0x1803 / 0x0099 / 01 | 0x0FDB |
| 0x0C | 证券数量 | 0x180C / 0x016C / 00 | 0x044E |
| 0x17 | 逐笔成交 | 0x0817 / 0x0101 / 01 | 0x52F / 0x532 / 0xFC2 / 0xFC5 |
| 0x1B | 分时图 | 0x081B / 0x0100 / 01 | 0x52E / 0x537 / 0x539 |
| 0x0F | 代码表旧口 | 0x080F / 0x0100 / 01 | 0x500 族 |

证券数量与代码表：实测走的是 0x044E（只带市场，回 u16 总数）与 0x0450（市场 + 起始序号，回 u16 条数 + 29 字节记录，详见 10.2/10.3）；0x500/0x501 族是另一套旧口，语义相近【参考】。这是构建全市场码表、再做增量订阅的入口。

快照行情（多股）：commtype 0x53E，帧头 <HIHHIIHH> 变体，body 为 (u8 market, char[6] code) 重复，单包上限约 80 只【参考】。

---

## 4. BDHQ 新通道

BDHQ 是同一连接上的第二代取数层：批量报价（多股）、逐笔成交/委托（L2）、分时明细、条件筛选、增量码表。它有两种载体。

### 4.1 二进制载体：0x2572 请求块【静态】

构造函数 BDGetBDData（VA 0x6F1100）先做形参校验：

~~~
6F1100: ... __thiscall
        if ( (unsigned)(a11 - 2) > 0x11 ) return -1;   // commtype 合法区间 2..0x13
6F11xx: v12 = *(int*)*this;                            // 复用的发送缓冲
        *(_WORD*)v12 = 9586;                           // 0x2572，块魔数
        v13 = a3; if ( a3 >= 500 ) v13 = 500;          // 单包品种上限 500
        *(_WORD*)(v12 + 2) = v13;
        for ( i = 0; i < *(_WORD*)(v12+2); ++i ) {
            *(_WORD*)(v12 + 4 + 2*i)   = *(_WORD*)(a4 + 2*i);   // 市场数组
            *(_BYTE*)(v12 + 1004 + i)  = *(_BYTE*)(a5 + i);     // 代码数组
        }
        ... 代码串拼接：按 0xDB96 / 每只长度 上限 2048 字节，逗号分隔 ...
        *(_WORD*)(v12 + 1506) = v20;    // 拼接后长度
        *(_WORD*)(v12 + 1504) = v31;    // 实际入包只数
        *(_DWORD*)(v12 + 1586) = a7;    // jobid
        *(_BYTE*)(v12 + 1509)  = a8;
        *(_DWORD*)(v12 + 1510) = a9;
        *(_BYTE*)(v12 + 1514) = a10;
        sub_7229E0(dword_11F5624);      // 与老协议同一个发送器
~~~

块布局：

| 偏移 | 类型 | 内容 |
|---|---|---|
| +0 | u16 | 0x2572 魔数 |
| +2 | u16 | 品种数 n（上限 500） |
| +4 | u16[500] | 市场号数组 |
| +1004 | u8[500] | 代码数组（定长池，与市场数组同下标） |
| +1504 | u16 | 实际入包只数 |
| +1506 | u16 | 逗号分隔代码串长度 |
| +1508 | u8 | 标志位（dword_F740D0 == 2 时置 1） |
| +1509 | u8 | 形参 a8 |
| +1510 | u32 | 形参 a9 |
| +1514 | u8 | 形参 a10 |
| +1586 | u32 | jobid（同时写入全局 dword_EAD58C） |

调用方：sub_7F1B00（0x7F1B4B 处 call）与 sub_7F1BE0（0x7F1C32 处 call）。相应轨迹串：

~~~
0xD2CD58 [trace]BDGetBDData,commtype=%d,jobid=%u,ForceNotReturnNoRight=%d,marketandcode,%s
0xD2CDAC [trace]BDGetBDData,commtype=%d,jobid=%u,colandtype,%s
0xD2CDFC [trace]%s,jobid=%u,%s,%s
~~~

### 4.2 单一品种分页请求 GetBDDataS【静态字符串 + 体量】

~~~
0xD2C2E8 [trace]GetBDDataS,JobID=%d,setcode=%d,stock=%s,offset=%d,wantnum=%d,nDataType=%d,colums=%s,SortID=%d,SortType=%d
0xD2C390 [trace]GetBDDataSACK,JobID=%d,setcode=%d,stock=%s,nDataType=%d,RecNum=%d,colums=%s
~~~

形参序列即服务端约定：市场、代码、偏移、请求条数、数据类型、列集合（逗号分隔列名）、排序字段号、排序方式。这是逐笔、分时明细、财务、资金流等的统一入口，offset/wantnum 负责翻页。

分析图（K 线族）另有专用轨迹串，说明支持起止日期与周期：

~~~
0xD2CBB0 [trace]获取%d#%s的分析图数据,jobid=%u,offset=%d,wantnum=%d,DataSource=%d,PriceType=%d,ValuationType=%d,PeriodType=%d
~~~

其中 begindate / nEndDate 在内部请求描述符里成对出现：

~~~
0x8FED78 [BDHQ]InBuff,获取单股行情数据2,commtype=%d,jobid=%u,code=%s,nSetcode=%d,offset=%d,whichview=%d,cDataTypes=%d,begindate=%d,nEndDate=%d,wantnum=%u,hasrecvednum=%u
0x8FECF0 [BDHQ]InBuff,获取单股行情数据,commtype=%d,...
~~~

### 4.3 JSON 载体【静态字符串】

~~~
0x920B88 JsonReqBDHQData
0x920B78 JsonReqBDSearch
0x92100C datatype
0x921954 setcode
0x904764 [JSONBDHQ]req,json获取多股行情请求,jobid=%u,Ret=%d,jsonreq: %s
~~~

JSON 请求以字符串发出（同一 socket、同一帧头，body 为 JSON 文本），键名包含 datatype、setcode 等；返回走同一个 0x2572 二进制应答解析器之外的 JSON 分支。

### 4.4 应答解析器【静态】

~~~
BDGetBDDataJjsCjACK  ← 逐笔成交  轨迹：0xD2C748 [trace]...datanum=%d,columnnum=%d,明细如下:
                                 明细行：0xD2CA60 [BDHQ]分时数据明细,成交,jobid=%u,%d/%d,time=%d,price=%.4f
BDGetBDDataJjsBjACK  ← 十档报价  轨迹：0xD2C870
                                 卖档：0xD2C958 [trace]卖档%d/%d,price=%.4f,%s
                                 买档：0xD2C978 [trace]买档%d/%d,price=%.4f,%s
单股多档盘口        ← 0xD2C9A0 [trace]单股多档盘口,Ans,jobid=%u,明细如下:
~~~

应答是"列数 + 行数 + 按列切片"的表结构（colums 决定列序），因此客户端必须按请求时的列集合解析，不能假定固定布局。

### 4.5 推送

BDHQ 还承担订阅推送（报价、逐笔多档、筛选结果），带 tableid/jobid 与 CFA 表窗口句柄：

~~~
0x8FEA10 [BDHQ]Push 延迟,准备通知数据,tableid=%I64d,cfaparenthwnd=%p,jobid=%u,当前业务:%d
0x8FEB20 [BDHQ]Push 新一轮,准备通知数据,...
0x8FEE70 [BDHQ]InBuff,多股筛选取消订阅,Count=%d,...
0x965A44 逐笔千档推送:%s
~~~

订阅类 commtype 与取消订阅配对，jobid 是取消的唯一键。

---

## 5. 本地数据格式（全部字节实证）

### 5.1 目录结构【文件】

~~~
C:/neptune/vipdoc/
├─ sh/ sz/ bj/          ; 沪 / 深 / 北
│  ├─ lday/             ; 日线 .day
│  ├─ minline/          ; 1 分钟线 .lc1
│  ├─ fzline/           ; 5 分钟线 .lc5
│  └─ eday/             ; 除权/财务 .cah 族
├─ ds/                  ; 扩展市场（外盘/期货），文件名为 <市场号>#<代码>
├─ ot/ cw/              ; 其他、财务
~~~

实测规模：lday 9537 个 .day、minline 9559 个 .lc1。路径模板字符串（0x90DEB0 起，含正斜杠与反斜杠两种写法）：

~~~
0x90DEB0 %s%s\lday\%s%s.day      0x90E3BC vipdoc/%s/lday/%s%s.day
0x90DF40 %s%s\minline\%s%s.lc1   0x90E404 %s%s/minline/%s%s.lc1
0x90DF10 %s%s\fzline\%s%s.lc5    0x90E3D4 %s%s/fzline/%s%s.lc5
0x90DEC4 %sds\lday\%d#%s.day     0x90E438 %sds/lday/%d#%s.day
0x918240 %s%s\fzline\%s%s.5      0x918254 %s%s\minline\%s%s.01   ; 短扩展名变体
0x922E30 %sbj\minline\bj%ld.lc1  0x918FCC %ssh\lday\sh000001.day  ; 北交所 / 上证指数
~~~

### 5.2 .day（日线）32 字节记录【文件】

实样本 C:/neptune/vipdoc/sh/lday/sh000001.day（288 字节 = 9 条）：

~~~
38 3E 34 01 | 86 16 05 00 | 2F 1C 05 00 | 87 10 05 00 | 44 17 05 00 | B8 0A 6C 52 | 7D B6 94 0B | 59 03 41 03
~~~

| 偏移 | 类型 | 含义 |
|---|---|---|
| +0 | u32 | 日期 YYYYMMDD（0x01343E38 = 20201016） |
| +4 | u32 | 开盘价 ×100（0x00051686 = 333446 → 3334.46） |
| +8 | u32 | 最高价 ×100（334895 → 3348.95） |
| +12 | u32 | 最低价 ×100（331911 → 3319.11） |
| +16 | u32 | 收盘价 ×100（333636 → 3336.36） |
| +20 | float | 成交额（元，0x526C0AB8 ≈ 2.5345e11） |
| +24 | u32 | 成交量（股，0x0B94B67D = 194295421） |
| +28 | u32 | 保留（本样本 0x03410359） |

### 5.3 .lc1（1 分钟线）32 字节记录【文件】

实样本 sh000001.lc1 首记录：

~~~
F7 B3 | 3B 02 | 33 EB 67 45 | CD 0C 68 45 | 3D B6 67 45 | 14 E6 67 45 | 42 9A 1B 51 | FB 7A E5 18 | 3B 00 DF 08
~~~

| 偏移 | 类型 | 含义 |
|---|---|---|
| +0 | u16 | 打包日期 0xB3F7 = 46071 → 2026-10-15（年 = 46071/2048+2004，月 = 1015/100，日 = 15） |
| +2 | u16 | 当日分钟数 0x023B = 571 → 09:31 |
| +4 | float | 开 3710.70 |
| +8 | float | 高 3712.80 |
| +12 | float | 低 3707.39 |
| +16 | float | 收 3710.38 |
| +20 | float | 成交额 4.1769e10 |
| +24 | u32 | 成交量 417692411 |
| +28 | u32 | 保留 |

.lc5（5 分钟）同构，日期打包与分钟数一致，仅周期不同。

### 5.4 复权因子 gbbq【文件】

T0002/hq_cache/gbbq，5,617,855 字节 = 4 字节条数（193719）+ 29 字节 × 193719 条：

~~~
+0  u32 日期 YYYYMMDD
+4  u8  类型（1 除权除息 / 2 送配 / 3 增发 / 4 股本变动 …）
+5  u8  市场
+6  char[6] 代码
+12 float[4] 四个因子（送股、配股、配股价、分红）
+28 u8  保留
~~~

配合 base.dbf 的 ZGB（总股本）、LTAG（流通股本）即可做前/后复权。

### 5.5 证券基础表 base.dbf【文件】

T0002/hq_cache/base.dbf：7988 条 × 481 字节，DBF III 头（记录数 7988、头长 1313、记录长 481），字段（节选）：

| 字段 | 类型 | 长度/小数 | 含义 |
|---|---|---|---|
| SC | C | 1 | 市场 |
| GPDM | C | 6 | 股票代码 |
| GXRQ | N | 8 | 更新日期 |
| ZGB | N | 14,2 | 总股本 |
| GJG | N | 14,2 | 国家股 |
| FQRFRG | N | 14,2 | 发起人法人股 |
| FRG | N | 14,2 | 法人股 |
| BG | N | 14,2 | B 股 |
| HG | N | 14,2 | H 股 |
| LTAG | N | 14,2 | 流通 A 股 |
| ZGG | N | 14,2 | 总股本（另一口径） |
| ZPG | N | 14,2 | 转配股 |
| ZZC / LDZC / GDZC / WXZC / CQTZ / LDFZ | N | 14,1 | 总资产 / 流动资产 / 固定资产 / 无形资产的财务项 |

同目录另有 code2name.ini（代码→名称）、base.map（索引）、blocknew（板块）。

### 5.6 其他缓存（完整清单见 11 章）

第 11 章把 hq_cache 下 90 个文件逐个定性：base.dbf 是 40 字段的证券主表（含股本结构、财务快照、股东户数、上市日期），tdxhy.cfg 是行业/地域映射，9 个 block 文件共 958 个板块 114899 条成分，还有期货合约规格、期权链、权息、交易规则与大数据表清单。

| 路径 | 内容 |
|---|---|
| T0002/hq_cache/ | 码表、基础表、复权因子（见上） |
| T0002/bdhq_cache/ | BDHQ 应答落盘（实测为空） |
| T0002/blocknew/ | 自选/板块 |
| T0002/cloud_*、T0002/export/ | 云同步与导出 |
| T0002/User/ | 用户个性化 |
| ds/lday/%d#%s.day | 扩展市场日线（外盘、期货），市场号前缀 |

---

## 6. 自建量化程序的落地路径

### 6.1 三条路径对比

| 路径 | 实时性 | 历史深度 | 实现成本 | 风险 |
|---|---|---|---|---|
| A 直连 7709 自实现协议 | 实时（秒级） | 服务端可回溯数千条，配合翻页可取长历史 | 中（本报告已给全帧格式） | 需自建码表/复权；主站有并发限制 |
| B 走 BDHQ 通道 | 实时 + 推送 | 支持 begindate/nEndDate/offset/wantnum 分页 | 高（表结构 + jobid 生命周期） | 解析复杂，列集合必须与请求一致 |
| C 纯读本地 vipdoc | 取决于客户端落盘频率 | 同本地文件深度 | 低 | 必须让 TdxW.exe 在线跑；数据滞后 |

推荐组合：**A 取行情落库 + C 兜底校验 + B 仅用于 L2 逐笔**。

### 6.2 路径 A 的执行步骤

1. 解析 connect.cfg 的 [HQHOST]/[TRYHQHOST]，取 IPAddress/Port 列表；按 Weight 降序选优，失败换下一个。
2. 连接后立刻按 10.1 节发三条初始化帧（前两条读干净应答、第三条不阻塞），然后才发业务请求。
3. 先拉码表（0x044E 取数量、0x0450 分页取代码，见 10.2/10.3），本地建 (market, code) → 名称索引；码表只增量更新。
4. 日线：category = 9，start 自 0 递增，count ≤ 800；分钟线 category = 7/0，取最近 N 天；逐笔 commtype 0xFC5。
5. 落库：原始压缩值解码成 (time, price, vol, amount) 后写 Parquet/SQLite；同时保留原始帧便于回溯。
6. 复权：用 gbbq 因子在**读取层**做，落盘保持原始价。
7. 连接管理：一请求一连接（见 2.4 实测），用连接池把并发控制在 2–4 条；每条连接在途请求 ≤ 5（对照 [esi+14h] 上限）。

### 6.3 实现要点与坑

- **长度双写**：帧头 +6 与 +8 必须都等于载荷长度，缺一即被丢弃。
- **commtype 与第二字节必须配对**（见 3.6），错配不会报错，只会拿到空 body。
- **价格单位**：协议内是 1/1000 元；写 .day 时乘 100 取整（.day 是 1/100 元）。指数与个股口径不同，落库前统一成"元"的 Decimal。
- **成交量/成交额**：协议是压缩浮点，需按 get_volume 还原成 double；.day 里成交量是 u32 股、成交额是 float 元，float 只有 7 位有效数字，超过 1 亿元的成交额会丢精度，长历史建议以协议原值重算。
- **停牌与空档**：返回条数为 0 不等于错误；start 越过头部同样返回 0 条，需与"当天无数据"区分。
- **除权跳变**：category = 12 是除权日线，可与 9 交叉核对复权因子是否生效。
- **zlib 应答**：解压前必须校验 unzipsize 上限（客户端有 0xDB96 级预算），防畸形包炸内存。
- **心跳**：一请求一连接时用不上；若改成长连接复用，需按 0x0C 0x01 形态周期发心跳并重置在途计数。
- **BDHQ 的 jobid**：每请求唯一，取消订阅必须用原 jobid；超时不取消会持续占服务端资源（0x91FE20「长时间没有处理完」即此逻辑）。

### 6.4 合规与边界

- 协议为明文私有协议，服务端未做鉴权即可取 L1 快照与 K 线；L2 逐笔需登录态（Tc.dll 的 KHSS_SIG_* / TC_Login 负责）。
- 单 IP 高频取全市场会被降速，建议按板块分批 + 夜间补历史。
- 本报告基于 v11.68 银河版；主站与协议随版本演进，升级前先跑一遍三条初始化帧与一条 K 线做冒烟测试。

---

## 7. 参考实现

随报告附 C:/projects/reverse/tdx_client.py，实现路径 A 的最小闭环，已对实盘主站跑通（第 10 章的全部命令都由它发出）：

- TdxConnection：connect.cfg 解析（IPAddress/Port/Weight，含 IPv6 字面量）、按 Weight 选优、三条初始化帧（前两条读干净应答、第三条不阻塞）、10 字节帧头组包（长度双写）、16 字节应答头 + zlib 解压、在途计数限流（上限 5）、心跳、失败换站重连。
- security_count / security_list：码表数量与分页（29 字节记录，GBK 名称）。
- security_bars(category, market, code, start, count)：K 线；按「开差/收差/高差/低差 + 量/额对数浮点」还原 OHLCVA。
- minute_time_data / transaction_data：分时与逐笔。
- read_day / read_lc1 / load_gbbq：本地 .day/.lc1 解析与复权因子加载。

用法示例：

~~~
python tdx_client.py smoke
python tdx_client.py count  --market 1
python tdx_client.py codes  --market 1 --start 0 --count 100
python tdx_client.py bars   --market 1 --code 600000 --category 9 --count 200
python tdx_client.py tick   --market 1 --code 600000 --count 50
python tdx_client.py local  --market 1 --code 000001
~~~

---

## 8. 证据索引

| 主题 | 证据 VA / 路径 |
|---|---|
| HQHOST 解析 | 0x6995F0（串 0xD22918）、交叉引用 0x681138/0x69967C/0x69BCCA/0x78DF53/0x7B2401/0x7FB1CE/0x80839C |
| 端口默认值 | 7709: 0x69685E；7711: 0x696A6D；7708: 0x69A296 |
| 帧头构造 | 0x722A34–0x722A9B、0x722BA5（sub_7229E0） |
| 帧头全局 | word_128FC44 / word_128FC4C / byte_E34232，写入点 0x44A62A、0x44D400、0x5690A2 等 60 余处 |
| 分时请求 | sub_6E4470（0x6E4470），commtype 0x539/0x52E/0x537 |
| 分笔请求 | sub_6E46B0（0x6E46B0），commtype 0xFC2/0xFC5/0x52F/0x532 |
| BDHQ 请求块 | sub_6F1100（0x6F1100），魔数 0x2572 于 0x6F11??；调用方 0x7F1B4B、0x7F1C32 |
| BDHQ 轨迹串 | 0xD2CD58、0xD2C2E8、0xD2CBB0 |
| 分笔/报价应答 | 0xD2C748、0xD2CA60、0xD2C958、0xD2C978 |
| 本地格式 | vipdoc/sh/lday/sh000001.day、vipdoc/sh/minline/sh000001.lc1 |
| 路径模板 | 0x90DEB0、0x90E3BC、0x90DF40、0x90DF10 |
| 复权/基础表 | T0002/hq_cache/gbbq、T0002/hq_cache/base.dbf |
| 传输层 | TDXAsioComm.dll：工厂导入描述符 0x142F8A0、CUserComm vtable 0x1009C248、WSASend 调用点 0x100177D0/0x100204F0/0x10021590 |

## 9. 静态分析的已知边界

1. Hex-Rays 对含 __alloca_probe + SEH 的大函数（BDHQ 组包、InBuff）会失败并退化，本报告对这些函数仅做反汇编级取证；结构体字段语义以轨迹串形参顺序推定，属【推断】。
2. VMP 把调用改成伪 IAT 槽（0x8000xxxx），部分被调函数身份只能由参数形状与既有实现反推。
3. 未观测运行时：应答表的确切列序、jobid 分发、订阅取消时延均需一次实连验证。
4. commtype 全表虽已枚举，但 0x500–0x54B 中多数仍只有"被压栈"这一事实，语义靠上下文分组推定。
5. L2 通路依赖登录态，本报告只覆盖到请求构造，未涉鉴权报文。
6. 分时序（commtype 0x051D）只还原到「头 + 前几分钟」，中后段的额外字段（疑似成交额、笔数）未完全对齐，生产上建议以本地 .lc1 或 BDHQ 分时明细替代。


## 10. 实连验证结果（本次实测，非静态部分）

报告主体是静态的；为确认帧格式，本机对 connect.cfg 首选主站做了一轮明文实测，全部命令与应答如下（16 字节头中 +12 为 zipsize、+14 为 unzipsize，已实测确认）。

### 10.1 握手（必须按序，必须读干净前两条的应答）

~~~
C->S 0C 02 18 93 00 01 03 00 03 00 0D 00 01
S->C B1 CB 74 00 1C 02 18 93 00 00 0D 00 5E 00 BD 00 + zlib(95B -> 189B 能力块)
C->S 0C 02 18 94 00 01 03 00 03 00 0D 00 02
S->C B1 CB 74 00 1C 02 18 94 00 00 0D 00 5E 00 BD 00 + 同上
C->S 0C 03 18 99 00 01 20 00 20 00 DB 0F D5 D0 C9 CC BD F0 D7 EA 00 00 00 8F C2 25 40 13 00 00 D5 00 C9 CC BD F0 D7 EA 00 00 00 02
S->C （无应答）
~~~

要点：第三帧服务端不回应，若等它就超时；前两帧的应答若不读干净，后续报文立即错位；漏发第三帧则业务请求被直接断连。

应答头结构（16 字节）：

| 偏移 | 类型 | 内容 |
|---|---|---|
| +0 | u16 | 0xB1CB 魔数 |
| +2 | u16 | 0x0074 |
| +4 | 6 字节 | 原样回显请求头 +0..+5 |
| +10 | u16 | commtype 回显 |
| +12 | u16 | zipsize |
| +14 | u16 | unzipsize |

### 10.2 证券数量（commtype 0x044E）

~~~
C->S 0C 0C 18 6C 00 01 08 00 08 00 4E 04 01 00 75 C7 33 01
S->C ... 02 00 02 00 E7 19  ->  0x19E7 = 6631（深市口径示例）
~~~

实测沪市返回 27697。

### 10.3 证券列表（commtype 0x0450，29 字节/条）

~~~
C->S 0C 01 18 64 01 01 06 00 06 00 50 04 <market:u16> <start:u16>
S->C <num:u16> 后接 num × 29 字节：<code:6s><volunit:u16><name:8s><4s><decimal:u8><preclose:4B><4s>
~~~

实测返回 999999 上证指数、000001 上证指数、000016 上证50 等。

### 10.4 K 线（commtype 0x052D，按 3.2 节模板实测成功）

600000 日线最后三根：

| 日期 | 开 | 收 | 高 | 低 | 量 | 额 |
|---|---|---|---|---|---|---|
| 2026-10-20 | 9.79 | 9.76 | 9.86 | 9.68 | 605132 | 589321408 |
| 2026-10-21 | 9.72 | 9.66 | 9.76 | 9.61 | 462562 | 447085824 |
| 2026-10-22 | 9.66 | 9.62 | 9.73 | 9.55 | 625298 | 602885504 |

价量自洽（低 ≤ 开/收 ≤ 高），与本地 .day 同源，说明 3.4 的解码方向正确。

应答记录的真实字段序（比 3.4 节更精确，以此为准）：

~~~
u32 日期 | 开差 | 收差 | 高差 | 低差（4 个可变长整数，单位 1/1000 元） | u32 量(对数浮点) | u32 额(对数浮点)
open = 上一根收 + 开差；close = open + 收差；high = open + 高差；low = open + 低差
~~~

### 10.5 分时（commtype 0x051D）

请求 0C 1B 08 00 01 01 0E 00 0E 00 1D 05 <market:u16><code:6s><u32 0>。

应答头是 <num:u16> 00 00 01 <code:6s> <2 字节填充>，记录自偏移 13 起。实测前几分钟按 (价差, 量) 能还原（9.62 / 9.66 / 9.59，量 4 / 11 / 15299139），但中后段字段序仍有未对齐处（疑似含成交额与笔数），**分时优先读本地 .lc1 更稳妥**。

### 10.6 逐笔（commtype 0x0FC5）

请求 0C 17 08 01 01 01 0E 00 0E 00 C5 0F <market:u16><code:6s><start:u16><count:u16>，实测返回 15:07 9.62 量 22、15:08 量 10 等；字段序 <u16 分钟><价差><量><笔数><买卖方向><保留>。

### 10.7 验证状态汇总

| 命令 | 状态 |
|---|---|
| 三条握手 | 已验证（含第三帧无应答） |
| 证券数量 / 证券列表 | 已验证 |
| 日线 / 周线 / 月线 / 分钟 K 线 | 已验证（含价格差分与对数浮点还原） |
| 逐笔 | 已验证 |
| 分时 | 部分验证（头与偏移确认，中后段字段序待确认） |
| BDHQ（0x2572 / JSON） | 仅静态 |

> 实测该服务端每连接只服务一条请求，客户端因此在失败时自动重连重试（最多 3 次），连续调用的成功率即 100%；单请求往返约 0.15 s。

## 11. 本地数据资产全表（量化视角）

客户端为了少请求服务端，把大量"静态但极难拿到"的数据落盘在安装目录里。这些文件**不加密、不鉴权、可离线解析**，是本项目里性价比最高的部分。清单与字段如下，全部为本机实测（文件字节 + 解析结果）。

### 11.1 证券主表 T0002\hq_cache\base.dbf【文件】

dBASE III，7988 条，记录 481 字节，40 字段。覆盖深 4036 / 沪 3599 / 北 353，**每条都带上市日期**，是构建 universe 的权威底表。

字段字典（顺序即记录布局，首字节为删除标记）：

| 字段 | 类型 | 含义 | 单位 |
|---|---|---|---|
| SC | C1 | 市场 0 深 / 1 沪 / 2 北 | |
| GPDM | C6 | 代码 | |
| GXRQ | N8 | 数据日期 | |
| ZGB | N14.2 | 总股本 | 万股 |
| GJG / FQRFRG / FRG / BG / HG | N14.2 | 国家股 / 发起人法人股 / 法人股 / B 股 / H 股 | 万股 |
| LTAG | N14.2 | 流通 A 股 | 万股 |
| ZGG / ZPG | N14.2 | 职工股 / 转配股 | 万股 |
| ZZC / LDZC / GDZC / WXZC / CQTZ | N14.1 | 总资产 / 流动资产 / 固定资产 / 无形资产 / 长期投资 | 元 |
| LDFZ / CQFZ | N14.1 | 流动负债 / 长期负债 | 元 |
| ZBGJJ / JZC | N14.1 | 资本公积 / 净资产 | 元 |
| ZYSY / ZYLY / QTLY / YYLY | N14.1 | 主营收入 / 主营利润 / 其他利润 / 营业利润 | 元 |
| TZSY / BTSY / YYWSZ / SNSYTZ | N14.1 | 投资收益 / 补贴收入 / 营业外收支 / 上年收益调整 | 元 |
| LYZE / SHLY / JLY / WFPLY | N14.1 | 利润总额 / 所得税 / 净利润 / 未分配利润 | 元 |
| TZMGJZ | N14.3 | 每股净资产 | 元 |
| DY / HY | C3 / C4 | 地域码 / 行业码 | |
| ZBNB | N2 | 股本变动次数（次新股筛选用） | |
| SSDATE | C8 | 上市日期 YYYYMMDD | |
| MODIDATE | C6 | 修改日期 | |
| GDRS | N8 | 股东人数 | 户 |

要点：LTAG 与 ZGB 直接给出流通比例，ZBNB 是现成的次新股因子，GDRS 是股东户数因子，SSDATE 可算上市天数。全部字段一次性可得，无需逐股请求。

### 11.2 行业与地域 T0002\hq_cache\tdxhy.cfg【文件】

5668 行，格式 市场|代码|行业码|?|?|板块码，行业码 112 个（T 前缀为通达信行业树，X 前缀为地域/概念板块）。是行业中性化、行业暴露约束、板块轮动的基础映射。

### 11.3 板块与成分（9 个文件，958 个板块，114899 条成分）【文件】

| 文件 | 板块数 | 语法 | 内容 |
|---|---|---|---|
| infoharbor_block.dat | 547 | 头 #GN_名称,市场,板块指数码,起,止,, + m#code 逗号 | 概念/题材 |
| hkblock.dat | 177 | #名称 + 每行代码 | 港股 |
| mgblock.dat | 138 | 同上 | 美股 |
| jjblock.dat | 38 | #名称 + market,code | 基金 |
| spblock.dat | 35 | #名称 + 7 位 mcode | 商品/板块 |
| sbblock.dat | 14 | 每行代码 | 新三板 |
| csiblock.dat | 7 | #名称 + 62,codeCNY01 | 中证系列指数成分 |
| ukblock.dat | 2 | 每行代码 | 英股 |

概念板块头部的第三个字段就是该板块对应的 880xxx 指数代码，可直接与 11.4 关联，做板块—成分双视图。

### 11.4 板块指数 T0002\hq_cache\tdxzs.cfg【文件】

604 条，格式 名称|880xxx|类型|?|?|序号。类型 5 为概念、3 为地域、1 为行业。与 11.3 组合即可还原通达信的板块体系。

### 11.5 横截面指标缓存 T0002\hq_cache\tdxzsbase.cfg【文件】

1126 行，29 列：market|code|流通市值|总市值|…|日期|换手|涨跌…，可当每日截面快照做横截面因子校验。列语义需按 11.1 的口径对齐后使用。

### 11.6 期货合约规格 T0002\hq_cache\code2name.ini【文件】

99 条：code,name,交易所,,,合约乘数,保证金率,最小变动,涨停幅度,跌停幅度,币种,报价单位,…,交割月份规则。做期货/期权时必须的合约参数全在里面。

### 11.7 期权链 T0002\hq_cache\ggqqcode.txt、szqqcode.txt【文件】

1208 条，首行 日期,数量，记录 id,合约代码,名称,标的,标的价格码,月份,行权价,数量,行权价2,起,止,剩余天。沪（510050 等）与深（159901 等）分文件，可直接还原整条期权链与到期日。

### 11.8 权息与港股权息 T0002\hq_cache\gbbq、hkqxinfo.dat【文件】

gbbq 为 A 股权息：u32 条数 193719 + 29 字节记录（日期、类型、市场、代码、4×float 比例、标志），配合 5.4 可精确复权。hkqxinfo.dat 1.16MB 为港股权息同类数据。

### 11.9 财务数据包机制 vipdoc\cw【文件 + 静态】

gpcw.txt 是清单：gpcwYYYYMMDD.zip,md5,字节数（覆盖 2015–2026 各报告期）；gpszsh.txt 是同格式的逐股 gpszXXXXXX.dat 清单。落地路径为 vipdoc\cw\gpcwYYMMDD.dat（格式串 %scw\gpcw%04d%04d.dat，0x90B9F8）。本机只装了清单、没装数据包，需要联网下载；清单自带 md5 与长度，可做完整性校验。

### 11.10 交易规则 T0002\hq_cache\hqrule.dat【文件】

11 个常量，含 CYBZDRatio=0.20、CYBZCDate=20200822（创业板 20% 涨跌幅及其生效日）、KCBOpenDate、SHGTDayMax=520 等。做涨跌停价、可交易性判定时用它而不是硬编码。

### 11.11 扩展市场定义 dsmarket.dat、dsys.dat【文件】

INI，声明港股/美股/期货/期权市场开关与 GUI 市场编号（HasHKMarket、HasUSMarket、HasQQMarket 等），决定 market 码的合法取值域。

### 11.12 大数据表与云端通道【文件 + 静态】

T0002\bigdata_all.txt 列出 400+ 数据表 ID：JZFX 资金分析、ZDTFX 涨停分析、LHBFX/YZLHB 龙虎榜、RZRQ 融资融券、HSGT 沪深港通、GDRS 股东人数、JGCC 机构持仓、CWZB 财务指标、QXFA 权息方案、KZZ 可转债、QHTJ 期货统计、JJCW/JJTJ 基金。

通道是 **HTTP**：T0002\bigdata.cfg 把表名映射为 http://www.treeid/cloudpadcode_<ID>，connect.cfg 给出云地址（该文件如何从零重建见 16 章） TdxCloudAddress_TP=123.125.135.12:7199;…、TdxCloudAddress_TP2=page1.icfqs.com:7615;…，##tdxcloudaddress_*## 为占位符。用普通 HTTP 客户端替换占位符即可取表，不需要行情端口。

---

## 12. 协议侧增量证据

### 12.1 快照行情 commtype 0x053E【静态 + 参考】

请求块由 0x53E760 组包（[esi+0x120]=0、[0xB8]=1、[0xC0]=1），载荷为证券对数组；公开实现互校得到的帧头为 0C 01 20 63 00 02、长度字段、commtype 0x053E、05 00 00 00、00 00、n，随后 n 组 <B6s>。应答为 B1 CB + u16 n + 每只 <B6sH> 起始的价格基准，再依次为 价/昨收差/开差/高差/低差/…/五档价量/涨速。

本机对该帧做了 19 组变体实测：服务端**接受但不回应**（既不回包也不断连），说明该命令在银河 v11.68 的主站上被门控（可能要 L2 权限或先订阅）。生产环境优先用 4 章的 BDHQ 或 11.12 的 HTTP 表取截面，不要依赖它。tdx_client.py 的 security_quotes()/parse_quotes() 已按上述布局写好，换到放行的主站即可直接出五档数据。

### 12.2 逐笔委托与千档 0x0FC2 / 千档推送【静态】

0x0FC2 与 0x0FC5 同族（0x0F 类别），请求结构同 3.5 的逐笔模板（分钟、起始、条数）；千档由 逐笔千档推送:%s（0x965A44）驱动，界面侧上限 6 个组件。逐笔委托字段为 委托号/时间/价格/数量/方向，北交所无逐笔委托数据（0x97EC9C）。

---

## 13. 量化落地：这些资产拼得出什么【推断】

1. **Universe 与可交易性**：base.dbf 的 SSDATE/ZBNB/LTAG 加 hqrule 的涨跌阈值得出"上市满 N 日 / 非次新 / 流通市值区间 / 涨跌停价"，一次本地扫描即可，不耗请求。
2. **因子底表**：11.1 的 30 余个财务与股本字段加 11.5 的截面快照，可构造规模、杠杆、盈利质量、股东户数变化等基础因子；财务单位是元/万股，需统一量纲后再取对数或排位。
3. **板块轮动**：11.3 × 11.4 得到板块指数与成分股双视图，用板块指数日线算动量、拥挤度，再映射到成分。
4. **期权/期货**：11.6 的合约乘数与保证金、11.7 的整条期权链（行权价、到期日）可直接搭波动率曲面与跨式/日历价差。
5. **复权与对齐**：5.4 与 gbbq 精确复权，避免用未复权价算动量；财务数据按公告日而非报告期对齐，防前视偏差。
6. **数据校验**：用 11.9 清单的 md5 校验财务包；用 .day 与协议日线互校（10.4 已给出同源样本）。

---

## 14. 随附 tdx_local.py

本仓库的 tdx_local.py 把第 11 章全部实现为只读解析器，纯标准库，零依赖：

~~~
python tdx_local.py master   --out master.csv     # 11.1 7988 条
python tdx_local.py industry --out ind.csv        # 11.2 5668 条
python tdx_local.py blocks   --out blocks.json    # 11.3 958 板块/114899 条
python tdx_local.py blocks   --custom             # blocknew 自选板块
python tdx_local.py index    --out index.csv      # 11.4 604 条
python tdx_local.py snapshot --out snap.csv       # 11.5 1126 条
python tdx_local.py futures  --out fut.csv        # 11.6 99 条
python tdx_local.py options  --out opt.csv        # 11.7 1208 条
python tdx_local.py rules                         # 11.10 规则
python tdx_local.py files                         # vipdoc 覆盖 9537 个日线文件
python tdx_local.py bars --market 1 --code 600000 --out sh600000.csv
python tdx_local.py sitecfg-parse --path connect.cfg        # 解析站点表
python tdx_local.py sitecfg-check --path connect.cfg        # 校验（错误退出 1）
python tdx_local.py sitecfg-defaults --ref connect.cfg --out spec.json
python tdx_local.py sitecfg-build --spec spec.json --out connect.cfg
python tdx_adjust.py check --gbbq T0002\hq_cache\gbbq        # 权息库自检
python tdx_adjust.py apply --gbbq ... --csv bars.csv --market 0 --code 001207 --mode hfq --out adj.csv
python tdx_bootstrap.py init build\tdx --ref $tdx --spec spec.json
python tdx_bootstrap.py sync build\tdx --market 1 --limit 5000   # 线上取权息写 gbbq
python tdx_factors.py sync --db factors.db --market 1 --limit 200 # 事件库
python tdx_factors.py build --db factors.db --vipdoc vipdoc
~~~

CSV 一律 UTF-8 BOM，Excel 与 pandas 直接可读；blocks 输出 JSON 保留成分数组。

---

## 15. 局限

1. **本地历史很浅**：本机 sh\lday 4837 个文件共 1.2MB（约 8 根/只）、minline 70.8MB（约 480 根/只）。要长历史必须走 7709 协议翻页或自行积累——积累方式见 20.8。
2. base.dbf 的财务字段是报表原值快照，只有一条最新记录、带更新日期 GXRQ，不区分报告期；做因子前需按公告日重排，字段与单位见 20.8。
3. 11.5 的横截面字段没有官方列名，只有位置；结构与日期对齐点已在 20.10 定死，逐列语义仍需与 .day 反推后才能用于生产。
4. 0x053E 在本服未通（20.8 复测两种帧仍超时）；逐笔委托与千档只有静态证据，未实连。
5. 大数据 HTTP 通道的鉴权（QSID/Token）与返回体格式尚未实测，表 ID 仅确认存在；财务包的托管地址同样没定位到，试过的路径见 20.10。
6. 财务包与港股权息文件本机缺失，只有清单，需联网补齐。

## 16. connect.cfg 的完整重建（不依赖安装包）

### 16.1 为什么必须会自己造

完全新建一套库时，不可能为了一个 12 KB 的文本文件再下一遍 17 MB 安装包；而这个文件一旦缺失，客户端**不会自愈**：exe 里没有 [HQHOST]、HostNum=、PrimaryHost= 任何字面量，只有 IPAddress%02d 一个格式化串，段名全靠运行时拼装，所以删掉它等于把站点表清空。它的缺失还会同时带走云地址、更新链路、交易站与资讯站配置。

### 16.2 它确实是安装包文件【文件】

- 安装器自己的卸载清单 uninstall.cfg（[FOLDER]/[FILE] 两段）里第三条就是 FILE03=c:\neptune\connect.cfg，说明它由 setup 释放并被登记。
- CreationTime 与 TdxW.exe、base.dbf 同一秒（2026/10/17 0:53:40）；Update 目录与全盘 zip 扫描均无副本，排除“运行期生成”与“更新包下发”。
- 生命周期分两段：装上时释放 → 之后被 newhost.lst 的候选表合并改写（见第 2 章）。重建出的文件落在同一位置、同一格式，与原始文件等价。

### 16.3 完整 schema（25 分区 / 318 键）

| 分区 | 键数 | 作用 | 必需性 |
|---|---|---|---|
| HQHOST | 7 | 主行情站表：HostNum、PrimaryHost、HostNameNN、IPAddressNN、PortNN、WeightNN、AreasNN | 连接必需 |
| TRYHQHOST | 7 | 试连候选池（同构，可空） | 建议 |
| INFOHOST | 12 | 资讯站，额外 HostTypeNN、INFO、InfoURL、InfoURLStr、AUTOPOP、EXTERNINFO | 功能需要 |
| WTHOST | 10 | 交易站，额外 WT、WTFACE、AUTOIN、EXTERNWT、HexinDLLWt | 功能需要 |
| DSHOST | 6 | 数据站（端口 7720，含 IPv6 样例） | 功能需要 |
| OTHERHOST | 6 | **不是站点表**：L2PrimaryHost、SSDSPrimaryHost、SSDS2/3PrimaryHost、VipHQ(2)PrimaryHost，是指向 L2/SSDS/VIP 表的索引，-1 表示无 | 兼容占位 |
| CHATHOST / ICQHOST | 2 / 2 | 单站点形态，只有 Port 与 IPAddress | 兼容占位 |
| PHONE | 2 | PHONENUM、PrimaryPhone（-1 允许） | 兼容占位 |
| INFOUSER | 2 | UserName=guest、SavePass=0 | 兼容占位 |
| UserHost | 29 | 登录方式、端口 7715、QSID、CloudQSID、各登录开关 | 功能需要 |
| Other | 133 | 品牌文本、功能门控、自动更新（AutoupURLDir、AutoUpZip、OtherFileName）、云端地址、重连与网络开关 | 功能需要 |
| Version | 20 | 版本行为开关与 ##tdxcloudaddress_*## 占位符替换源 | 功能需要 |
| EXTERN 21 / TPSYS 18 / TPSUBSYS 9 / PROXY 6 / TDXPROXY 7 | — | 外挂与交易通道、代理 | 兼容占位 |
| Integrity 3 / VitalBtn 3 / IETOOLBAR 5 / SHELLEXE 3 / STARTMENU 2 / user 2 / Autoup 1 | — | 界面与外壳 | 兼容占位 |

必需性只有三档：连接必需、功能需要、兼容占位。全部默认值取自同版本参照安装，已压缩内嵌进 tdx_local.py（4.6 KB），所以**没有参照文件也能从零生成**。

### 16.4 生成流程

~~~powershell
# 1) 起始 spec（身份与数据站取自参照安装，主站用实连验证过的四台）
python tdx_local.py sitecfg-defaults --ref $tdx\connect.cfg --out spec.json
# 2) 从零生成（缺省键自动补齐，写出前自检）
python tdx_local.py sitecfg-build --spec spec.json --out connect.cfg
# 3) 独立校验
python tdx_local.py sitecfg-check --path connect.cfg
~~~

生成器强制以下不变式：

1. 编号一律两位、从 01 起连续；HostNum 与实际组数**强制相等**——正好消掉参照文件里 TRYHQHOST HostNum=9 却残留 13 组编号的历史脏数据。
2. 地址接受 IPv4、不带方括号的 IPv6 与域名；端口限 1–65535；权重缺省 90；PrimaryHost 越界自动归零。
3. 段内地址重复报错，跨段重复只警告（试连候选本来就与主站重叠，参照文件里 9 条候选全部与主站重复）。
4. 空站点段写 HostNum=0 并清空全部编号键，不留孤儿行。
5. 生成后立刻回读自检，出错抛异常且不落盘。

spec 结构：identity（品牌与身份键）、hosts / try_hosts / info_hosts / wt_hosts / ds_hosts（站点数组）、single（CHATHOST、ICQHOST）、otherhost（L2/SSDS/VIP 索引）、primary、sections（任意分区覆盖）。

### 16.5 校验器

sitecfg-check 检查：分区集合（缺段/未知段/重复段）、分区外脏行、HostNum 与编号是否一致、编号是否连续、地址合法性、端口合法性、段内重复、PrimaryHost 越界、单站点段地址、OTHERHOST 引用是否整数与越界。

分级与退出码：**错误**（阻断）返回 1，**警告**（可自愈，如跨段重复、缺名称、OTHERHOST 缺键）不影响退出码。参照文件当前就是 1 错误 + 10 警告，错误正是 TRYHQHOST 的编号残留。

### 16.6 一致性与实机验证

静态一致性：生成结果 25 分区 / 318 键，与参照文件**键集完全相同**（无缺键、无多键），parse → build → parse 往返稳定，自检 0 错误。

实机三组（同一台机、同一启动方式）：

| 组 | 配置 | 结果 |
|---|---|---|
| 对照 | 原始 connect.cfg | 主窗口正常起来，80 s 内无 TCP 连接 |
| 生成 | 本工具生成（品牌与站点沿用参照） | 表现与对照组一致，进程不崩、不退出 |
| 标记 | 生成 + TitleName=量化自建配置MARKER | 窗口标题出现 **量化自建配置MARKERV11.68**，并出现 通达信网上交易V6、TcComm Notification Sink 组件窗口 |

标记组是关键：客户端确实把生成文件读进去并应用了其中的 TitleName，同时交易组件照常加载，说明重建文件在结构上被完整接受。

补充一组反向实验：把 [Other] AlwaysGuest 与 [UserHost] GuestLogin 打开后再跑，仍不自动发起 7709 连接。结论是**自动连接还要靠界面上的站点/登录一步**，与配置文件是否完整无关——所以本项验证只做到“文件被接受”，没做到“跑通行情”。

### 16.7 落地建议

- spec.json 进版本库，connect.cfg 当**构建产物**，不再手工编辑。
- 启动前先 sitecfg-check，失败就回退上一份可用文件。
- 换券商只改 spec.identity 的十来个品牌键，不动代码。
- 候选池写进 spec.try_hosts，让文件由我们生成，避免客户端合并造成编号残留。
- 云地址（##tdxcloudaddress_*##）与 QSID 必须与券商版本配套，抄错只会静默失效。

### 16.8 局限

1. 键语义多由命名与默认值推定，未逐键反编译：VMP 使 .rdata 的分区名字符串既无 xref 也无指针槽（0x922918、0x924560 等实测均为 0），静态追不到读取点。
2. 本文件只解决“指向哪里”，L2 权限、Token、QSID 校验仍在服务端。
3. HasSSL、HasDS、InsideVersion 等开关的实际作用面未实测，保持参照值最稳。
4. 重建文件可让客户端起来，但**官网更新链路**（AutoupURLDir/AutoUpZip）仍依赖券商仍在维护的包名。

## 17. 复权：现在下的到底是不是复权数据

### 17.1 直接回答

**不是。** 两条路都是原始价：

- 走 7709 抓的 bars：服务器给的就是除权后的原始价，没有任何折算（3.3 节的 price 增量编码本身就是原始价）。
- 读本地 vipdoc\*.day：同样是原始价。

tdx_client.py 里确实有个 load_gbbq，但此前只用来打印“有 N 组因子”，**没有任何一条路径真的把因子乘进价格**。所以要建库，复权必须自己补。

### 17.2 为什么这件事不能省

数据库地基一旦踩在未复权价上，后面所有跨除权日的计算全错：均线在除权日出现假跳空、动量与波动率被除权放大、配对与回测的收益多算一段不存在的跌幅。以 001207 为例，2026-04-08 十送四派五，原始价从 29.60 直接掉到 21.59，单日 -27.06%，而公司当天其实**涨**了 3.9%。

### 17.3 gbbq 不是明文表

T0002\hq_cache\gbbq 每条 29 字节：前 24 字节加密（市场 1 + 代码 7 + 4 个 float32），后 5 字节明文（日期 u32 + 类别 u8）。解密是三段 8 字节的 Feistel 结构，密钥是 4176 字节固定表（0x44 初值 + 四张 256 项轮表 + 轮常量）。头部 u32 是条数，必须满足 4 + 条数×29 == 文件长度，可据此先验校验。

类别字段是事件类型：1 除权除息、2 送配股上市、3 非流通股上市、5 股本变化、6 增发新股、8 增发新股上市、9 转配股上市、10 可转债上市、11 扩缩股、13/14 送权证。只有**类别 1** 带派息、送转、配股比例；类别 5 的四个浮点其实是绝对股本数（本例 19726.0156 / 21146.0781），不是比例，混用会把因子算爆。

### 17.4 单位与公式

四个浮点按**每 10 股**记：f1 = 10 派 N 元记 N，f2 = 配股价（元/股，不折算），f3 = 10 送转 N 股，f4 = 10 配 N 股。实测判别很干脆：001207 若按“每股”解释，系数算成 0.1662，与实际的 0.7294 差一个数量级；按每 10 股算 0.7022，与开盘 0.7294 只差当日涨幅。

除权折算系数：

~~~
ratio = (前收 - f1/10 + f2 × f4/10) / (前收 × (1 + f3/10 + f4/10))
~~~

后复权把该日及以前的价格乘以 1/ratio 的连乘，前复权把该日以后的价格乘以 ratio 的连乘。

### 17.5 tdx_adjust.py

~~~
python tdx_adjust.py check   --gbbq T0002\hq_cache\gbbq
python tdx_adjust.py dump    --gbbq ... --out events.csv --category 1 --since 20150101
python tdx_adjust.py factors --gbbq ... --market 0 --code 001207 --closes-csv bars.csv
python tdx_adjust.py apply   --gbbq ... --csv bars.csv --market 0 --code 001207 --mode hfq --out adj.csv
~~~

客户端也接上了：bars 与 local 都支持 --adjust none|hfq|qfq（默认 none，因子默认从 ../T0002/hq_cache/gbbq 取，可用 --gbbq 覆盖）；gbbq 里没有该股记录时打一行提示并按不复权输出，不静默。

实测（001207 全段 320 个交易日，2026-03-20 至 2026-04-30）：最大单日跳空由原始 27.06% 降到后复权 8.91%，前复权同为 8.91%，且后复权序列在除权日前后连续（29.600 → 30.745）。残余的 8.91% 是 04-21 的真实跌幅（19.65 → 17.90），不是除权假跳空。另有一处 2026-10-09 十派三，复权后 18.490 → 18.790、18.250 → 18.546，跨日连续。

结构自检（check）在现有库上通过：193719 条、6331 只、1990-03-01 至 2026-10-28、类别分布正常、0 问题。

### 17.6 因子仓库怎么建【推断】

1. 建两张表：events（市场、代码、日期、类别、四浮点、来源哈希）与 factor（市场、代码、日期、cum_qfq、cum_hfq）。
2. 只在 events 有新纪录时重算受影响的那一段，cum 因子按日期单调累积，绝不允许回填时改历史。
3. 复权价**不落库**，用视图或物化视图由原始价 × 因子现算，避免与因子表不同步。
4. 每次刷新把 gbbq 整体哈希存证，条数只增不减，减少即报警（回退的库会把未来事件丢掉）。
5. 因子表按（代码, 日期）建唯一索引，重复导入天然幂等。

### 17.7 陷阱

1. **除权日 ≠ 股权登记日**：gbbq 记的是除权除息日，价格恰好从那一天起跳空，折算必须用**前一交易日收盘**，不是登记日。
2. 前收为 0 或缺失（新股首日、长期停牌）时 ratio 无意义，要跳过并标记，不能拿 1.0 冒充。
3. 配股价 f2 常为 0（纯送转），此时 f2×f4/10 自然为 0，公式不用特判。
4. 类别 5/9/11 的比例若误当送转会把因子放大或缩小若干倍，只认类别 1。
5. gbbq 只含**已发生**事件，公告未实施的不在里面；靠它做“未来除权日历”会漏。
6. 退市/长期停牌股的事件仍在库里，复权后会出现长平台，回测要按成交额过滤。
7. 复权价不可与原始价混用：市值、成交额、每股收益都得跟着换算，股本用原始价。

### 17.8 与“自举”的关系

自举一套库，光有连接配置不够，还得有可复算的权息底账：connect.cfg 决定去哪拿数，gbbq 决定拿回来怎么还原成连续序列。两者都在本地、都可脱机重建，这套程序才算不依赖安装包也不依赖第三方数据源。

（解密与布局参考 pytdx 的 gbbq_reader 实现，密钥表为通达信自带，非本项目推导。）

## 18. 自举：从空目录到可运行安装树

### 18.1 定义

本章的“自举”指：给定一个空目录和一份安装来源，我们的程序**不联网、不跑官方安装器**，就能造出一棵可以启动、可以填数据的目录树，并且造出来的东西可以被校验。

它分三层，前两层已完全离线自主：

1. **配置层**：connect.cfg 由第 16 章的生成器现造，不复制。
2. **数据层**：目录骨架、gbbq 占位、清单文件由 tdx_bootstrap.py 生成。
3. **程序层**：TdxW.exe、DLL、CEF 运行时等二进制只能来自某份现成安装（新造不了），由 --ref 复制。

### 18.2 一棵树由什么组成

- 76 个目录：T0002 下 51 个（含 hq_cache、wt_cache、bdhq_cache、cloud_* 等）+ vipdoc 下 6 个市场 × 4 个周期目录。
- 12 个程序目录递归复制：chrome、files、funcs、GNPlugins、jspages、QHData、SEPlugins、TCloudCalc、TCPlugins、webs、xiadan、ZDPlugins。
- 根目录程序文件按扩展名白名单复制（exe/dll/dax/daq/dai/xmb/ini/dat/dbf/cfg/json/txt/html/css/chm/bmp/jpg/ql/up），跳过 idb、bak。
- 生成物：connect.cfg、T0002\hq_cache\gbbq（4 字节空表）、bootstrap.json（构建时间、spec 哈希、配置哈希、目录数、复制清单）。
- T0002 下 2 MB 以内的状态文件（user.ini、syscomm.ini、stdqs.cfg、PriCS.dat 等）随 --ref 一起复制，界面布局与本地参数才有着落。

### 18.3 一个必须记住的坑：chrome/

逐组排除出来的结论：只造目录 + 复制根文件 + 全量 T0002 内容时，进程**能起来、能写日志，但永远不建窗口**，tdxsys3.log 里只有一行版本号。

- 换成原始 connect.cfg：一样不建窗 —— 排除配置。
- 补 vipdoc 日线：一样 —— 排除数据。
- 补 12 个程序目录（其中 chrome/ 是 CEF 运行时）：立即可正常起窗。

所以 chrome/ 是启动门槛，缺它时没有任何报错，只有静默卡住。自举清单里必须包含它，且不能用“进程还活着”当作成功判据，要看窗口。

### 18.4 命令

~~~
python tdx_bootstrap.py plan   <目标目录>
python tdx_bootstrap.py init   <目标目录> --ref $tdx --spec spec.json
python tdx_bootstrap.py fill   <目标目录> --market 1 --codes 600000,600519 --days 120
python tdx_bootstrap.py verify <目标目录>
~~~

--ref 只影响程序层与 T0002 状态文件，可省略：不带 --ref 也能造出纯配置层 + 数据层的骨架（实测 76 目录、4 站点、verify 通过），只是没有 exe 可跑。init 拒绝非空目录，避免覆盖既有库；verify 检查目录完整性、connect.cfg 是否可解析且有站点、gbbq 长度与条数是否自洽、清单里的配置哈希是否被改动，任何一项不过都返回 1。

### 18.5 实测

- init：76 个目录、142 个静态件、合计 3836 个文件（其中 chrome/ 70 个）。
- 生成物 connect.cfg 与第 16 章的产物**逐字节相同**（SHA-256 相等），不是复制来的。
- 启动：窗口标题正确显示 海王星金融终端v3.0-中国银河证券V11.68，进程稳定，可正常关闭。
- fill：写入 600000、600519、601318 各 120 条日线到 vipdoc\sh\lday，verify 仍通过。
- verify 统计：25 分区、36 条站点、gbbq 4 字节 0 条。
- 不带 --ref 的裸 init 同样通过（76 目录、4 站点、0 复制），配置层与数据层不依赖安装来源。

### 18.6 和建库的关系

我们要的最终产物是**数据目录**，不是复刻一份客户端。程序层可以一次性复制、之后不再变；真正反复重建的是配置层与数据层，这两层现在都能离线生成并机器校验。库里要有一份 spec.json 与一份安装来源的路径，就足以在任意机器上按 18.4 复现整套环境。

### 18.7 局限

1. 程序层仍需一份安装来源（exe、45 个 DLL、chrome/）。exe 是 VMP 打包的，无法自己编译。
2. base.dbf、gbbq、tdxhy.cfg 等主表要由服务端刷新或从来源复制，init 只放 gbbq 空占位。
3. 没有登录态：UserHost 里的 QSID/Token 抄的是参照安装，换券商要换。
4. 界面首启可能弹出向导，不影响数据链路。
5. 空 gbbq 意味着复权在数据补齐前是恒等变换，不会报错也不会提示，建库流程要先保证 gbbq 到位（第 19 章给了线上取法）。
## 19. 权息自举：从服务端抓除权除息、落库、回写 gbbq

### 19.1 为什么还要这一层

第 17 章把复权算法补齐了，但因子来源仍靠本地 gbbq 文件——那是客户端自己刷来的。真自举要求我们自己能从线上把权息取回来。

结论先给：**服务端提供这个接口**，commtype 0x000F，按单只证券返回其全部除权除息明细，内容与本地 gbbq 同源同格式，可以直接互相校验。

### 19.2 请求与应答

请求体与 0x0450 证券列表完全同构，只是换了命令：

~~~
0C 1F 18 76 00 01 | 0B 00 0B 00 | 0F 00 | 01 00 | 市场(1) 代码(6)
~~~

应答：9 字节头 + 条数 u16，然后每条 29 字节 = 市场 1 + 代码 6 + 保留 1 + 日期 u32 + 类别 1 + 载荷 16。类别 1 的载荷是四个 float32（派息、配股价、送转、配股，单位仍是每 10 股）；类别 11/12 是缩股比例；类别 13/14 是权证行权价与份数；其余是变动前/后的绝对股本。

### 19.3 交叉校验

同一只票，服务端与本地 gbbq 逐条比对（只比类别 1）：600000 两边各 27 条、001207 两边各 8 条，**双向差集都是 0**。字段值、单位、类别全部一致，说明它就是 gbbq 的线上来源。

### 19.4 反过来也能写：gbbq 加密器

解密是 Feistel，逆过程即是加密。实现要点：解密末步先异或 k0，所以加密首步就是 numold = 明文0 ^ k0；轮序由 0x40→0x04 反成 0x04→0x40，轮函数作用于 numold 而不是 num；末步 c0 = num ^ k44。

实测拿去编码原文件前 200 条，**200/200 与原始字节完全一致**（含尾部 5 字节明文）。有了它，写出的 gbbq 是正规格式，客户端与我们的读取器都认。

### 19.5 复权事件库 tdx_factors.py

表结构：

- events(市场, 代码, 日期, 类别, 派息, 配股价, 送转, 配股, 来源)：主键四元组，ON CONFLICT 覆盖，只增不删。
- factor(市场, 代码, 日期, cum_qfq, cum_hfq, basis)：每次由 events 全量重算。
- meta(key, value)：创建时间、同步时间。

子命令：sync（按市场全量或指定代码，逐只抓）、import（吃现有 gbbq）、build（可选 --vipdoc 取前收算真实比例，--togbbq 回写）、stats、export、gbbq。

关键设计：比例算不出来（缺前收）时退化为股本比例并标 basis=share，绝不把 1.0 当真实比例；basis=ratio 的行才是价格级复权。

实测：本地 gbbq 全量导入 193719 条，因主键去重落库 193718 条（源文件本身有一对完全重复的记录）；6331 只、63755 条价格类事件。给 001207 补 400 根日线后重算，三处 ratio 行与 tdx_adjust 直接复权的结果逐点相等（63755 行比对 0 处不一致）。回写的 gbbq 通过 check：193718 条、类别分布正常、0 问题。

### 19.6 自举链路上的 sync

tdx_bootstrap.py 增加 sync：给一个空目录，直接抓权息写成 gbbq。

~~~
python tdx_bootstrap.py init   <目录>
python tdx_bootstrap.py sync   <目录> --market 1 --limit 5000
python tdx_bootstrap.py verify <目录>
~~~

实测在临时目录上同步 2 只票 108 条，gbbq 自检通过、目录 verify 通过。这样“空目录 → 有权息库”不再需要任何安装来源。

### 19.7 局限

1. 一次一只、6331 只全量同步约 6331 次请求，服务端每连接只服务一次业务请求，得靠重连机制串行跑，耗时以十分钟计。
2. 服务端只回已发生事件，公告未实施的看不到。
3. 主键去重会吃掉源文件里的完全重复记录（这反而是想要的）。
4. 比例依赖前收，前收要自己先有行情；只有股本比例时复权会偏（派息部分缺失）。
5. 类别 11/12 的缩股、13/14 的权证目前只存不参与折算，遇到缩股股要人工确认。

---

## 20. 五个缺口的收口

第 19 章之前留下的五件事在本章全部落地，都有可复现命令与实测结论。

### 20.1 base.dbf 改由自己生成

base.dbf 是 40 字段、481 字节记录的 dBASE III 表，键是 SC(市场) + GPDM(6 位代码)，其余是股本结构、财务快照与股东数。原表 3.8 MB、7988 行，只覆盖有数据的个股。

现在按官方字段布局自己写：

~~~
python tdx_local.py  base --out base.dbf --markets 0,1
python tdx_bootstrap.py base <目录> --markets 0,1
~~~

实现要点：

- 头 32 字节 = 12 字节元信息 + 20 字节保留，随后每字段 32 字节描述，0x0D 结束字段区，记录区首字节是删除标志，文件尾 0x1A。
- 数值字段右对齐空格填充，字符字段左对齐；SC/GPDM 填真实值，其余留 0 与空。
- 记录数、头长、记录长按实际写入，字段名与长度照抄原表 40 项（SC..GDRS）。

实测：生成 52316 行 / 25.1 MB，回读 40 字段正确；放进自举树后启动客户端**正常出窗**（标题 海王星金融终端v3.0-中国银河证券V11.68），证明格式被接受。此前树里的 base.dbf 只有 1313 字节、0 记录，客户端不报错但主表是空的。

局限：财务字段是零值，不是真实报表数；要真值仍得联网拉财务包。

### 20.2 登录态：端口定位与探测入库

结论先给：**7709 没有登录分支，登录走 7715**。

实测三种载荷（空、u8、16 字节名）打 7709 的 0x0004：要么超时、要么被断连，服务端不认。而 UserHost.Port=7715 的地址 TCP 可连，只是发 7709 的 Setup 帧后静默——7715 的报文格式与 7709 不同，属于交易/登录通道，尚未逆出。

把探测做成命令，结果落盘留证：

~~~
python tdx_client.py   login --cfg <cfg> --out login.json
python tdx_bootstrap.py login <目录>
~~~

login_probe() 返回三段：hosts（实际连过的地址）、main（每载荷的成败与回包头 32 字节）、login_ports（TCP 是否可达）。两次实测 login.json：main 两条全超时，login_ports 114.117.72.207:7715 可达。

另外把站点表内置化：SEED_HQ/SEED_TRY/SEED_INFO/SEED_WT/SEED_DS 与 SEED_USERHOST 写进 tdx_local，sitecfg-defaults 不带 --ref 也产出 25 分区完整配置，UserHost.IPAddress 直接有值。此前裸生成的 UserHost.IPAddress 是空、DSHOST 是 0 台。

### 20.3 缩股与权证参与折算

字段位置先用数据判死：417 条 11/12 事件 f1 全为 0、f3 全非零；9 条 13 与 15 条 14 事件 f1/f3 全非零且 f1 像行权价（2.00 至 10.00）、f3 像每 10 股份数（1.0 至 9.0）。所以：

- 11/12：f3 是份额拆分比，价格除以它。
- 13/14：f1 行权价、f3 每 10 股份数，按内在价值折价，认购取 max(0, 前收-行权价)、认沽取 max(0, 行权价-前收)，再乘以份数。

实测 588810 在 20260909 有 11 类事件 f3=2：原始序列在该日跳空 50.25%，后复权后最大跳空降到 14.40% 且不在该日；因子库对同一天的累计系数与直接复权逐值一致（差异 0）。f3 最小值 0.0013、最大值 441.64，两个方向都覆盖。

13/14 的折价公式属推断（【推断】），无 2005–2006 的本地行情可对照，入库与折算路径已通。

### 20.4 断点续传

两处同步都改成可续：

- tdx_factors.py sync 建 done(market, code, at, events) 表，默认跳过已完成代码，--redo 重来；每只成功即提交。
- tdx_bootstrap.py sync 用 T0002/hq_cache/.xdxr_state.json 记进度，gbbq 每只增量落盘，--redo 清空该市场进度。

两个同步都改成按 (市场, 代码, 日期, 类别) 去重后再写 gbbq，重复跑不再产生重复记录。

实测：bootstrap 连跑两次，第二轮“跳过 2 只、新增 0 条”，gbbq 仍 133 条；--redo 后仍 133 条（去重生效）；factors 第二轮“待同步 0 只（已完成跳过 2 只）”。

### 20.5 build\full 删除

751 MB / 7716 文件的整份复制是当年排查“不建窗”的对照物，结论已进 18.3，删除后 build 只剩 654 MB 的 build\tdx。

### 20.6 第 15 章其余局限的推进

- 第 1 条（本地历史浅）：不改结论，但 fill 已能一次补 250 根，长历史靠积累。
- 第 2 条（财务字段无报告期）：base.dbf 自造后字段结构在手，缺的只是数据源。
- 第 6 条（财务包缺失）：本轮无法联网补，路径已由 base.dbf 生成器铺好。
- 第 4 条（0x053E 未通）：本轮复测仍无响应，维持 L2 门控判断。
- 第 3、5 条维持原判。

### 20.7 仍未闭合

1. 7715 登录报文格式未逆出，QSID 仍是券商品牌串而非令牌，L2 权限无法自己拿。
2. base.dbf 财务字段为零值。
3. 13/14 折价只有推断，没有历史行情对照。
4. 全市场同步慢这件事没变，只是不再怕中断。

### 20.8 第 15 章逐条的推进

- 第 1 条：fill 增加合并模式并设为默认，新抓的按日期并进去、同日以新值为准，加 --no-merge 才覆盖写。实测先写 1 根 19900101 的假记录再抓 20 根，结果 21 根、旧记录仍在、日期不重复；连抓两次仍 20 根不膨胀。这样每天跑一次 fill（--days 20）就能一天天把历史攒厚。
- 第 2 条：不用再猜字段语义。base.dbf 每条都有 GXRQ（600000 为 20260828、000001 为 20260815），是快照更新日而不是报告期；MODIDATE 在这批样本里全空。单位是万股与元（600000 的 ZGB=3330583.75 即 333 亿股、ZZC=10405354496 即 104 亿元），ZGG 是每股收益、TZMGJZ 是每股净资产。只有一条最新记录，所以披露滞后与重述都看不到。
- 第 4 条：0x053E 用两种帧头（类别 0x02 与 0x01、版本 0x0069、hi 0x18）各打一次，都超时，维持“本服不通”的结论。
- 第 3、5、6 条：仍需另找数据源或抓包，未动。

### 20.9 登录态的配置层自造

登录态拆两层看：配置层的要素（UserHost 段 29 键、登录口地址与端口、QSID/CloudQSID、INFOUSER 的 guest 占位），与协议层的鉴权报文。前者已经能自造，后者未通。

- SITECFG_DEFAULTS 的 29 个 UserHost 键与内置 SEED_USERHOST 一起，让裸 init 直接产出 IPAddress=114.117.72.207、Port=7715。与参照配置逐键比对：键 29 对 29、差异 0 项。
- 新增 regen 命令：树里配置丢了或 IPAddress 被清空，一条命令重造并刷新清单哈希，其余内容不动。
- verify 增加三条守卫：IPAddress 为空、Port 不是 7715、QSID/CloudQSID 为空各记一个问题。旧树就是这么被抓出来的（IPAddress 空），regen 后 verify 通过、smoke 正常回包、窗口照常起。

协议层把能想到的载荷都试了：7715 上 HTTP GET、裸 0x0004、0x0C 帧（类别 0x02 与 0x01、版本 0x0093 与 0x0069、hi 0x18 与 0x00、commtype 0x0001/0x0002/0x0004/0x0005）共 7 种，全部静默到超时。结合 7709 的 0x0004 也不认，判断 7715 要的是客户端自己拼的登录包（很可能带机器指纹或加密），必须从 VMP 体内挖，不是试出来的。

### 20.10 11.5 的字段结构与财务包托管地址

tdxzsbase.cfg 是单日横截面缓存：1126 行，竖线分隔 26 个字段，字段 0 是市场、1 是代码、2 到 6 是成交额/成交量/总市值/流通市值/一个比值、7 是日期、8 是标志位、9 起是各周期涨跌幅与家数、金额一类数值。代码取自指数与板块（000001 至 000009 都在，不是个股）。

对齐点验过了：字段 7 全是 20261016，与本地 vipdoc\sh\lday\sh000001.day 的最后一天同一天。所以它是“某交易日的板块横截面”，能当横截面因子源，但 9 号以后的列还得逐列拿同日 .day 与板块成分反推，别直接当涨跌幅用。

财务包的清单在本地（vipdoc\cw\gpcw.txt 8575 字节、gpszsh.txt 501800 字节，逐条是 zip 或 dat 名、md5、字节数，覆盖 2015 至 2026 各报告期），文件本体一个没有。定位托管地址做了这些尝试，全部落空：

- http://www.tdx.com.cn/products/autoup/vipdoc/cw/gpcw20261231.zip 等 4 条 autoup 前缀路径，全 404。
- http://www.tdx.com.cn/tdxbase/sgpcwdata.dat、autoup/tdxbase/ 版本，全 404。
- 对 7711 资讯主站用 HTTP GET 打根路径与 cw 路径，连接被立即断开——7711 要的是二进制帧，不是 HTTP。

autoup 的根目录是 403（不许列目录），所以真实前缀只能从客户端体内取。结论与 7715 登录包同性质：要进 VMP 挖拼接点，试是试不出来的。

### 20.11 登录链路的静态补充：默认 7615 与 JSON 主机表

在 0x695250（配置解析，USERHOST 分支）里读到两处默认值：sub_A2366E("USERHOST","Port",7615) 与 ("USERHOST","Port2",7615)。也就是说登录口默认是 7615，配置里写的 7715 只是被读进来覆盖；同段还有 BakIPAddress、BakIPAddress2、BakPort、BakPort2 四组备用键，HostNum 上限 150。

0x9718A0 是把配置项序列化成 JSON 的分发函数，其中 case 16 把整张 USERHOST 主机表拼成 {"Addr":"<ip>","Port":<port>} 数组（分隔串 0xCF4570 是逗号，0xCF89C8 是结尾包装），说明登录流程是把主机表当 JSON 交给上层，而不是 7709 那种二进制帧。

实连对照：7615 与 7715 在 114.117.72.207、1.202.143.37、123.125.108.101 上都能连上 TCP；但用 HTTP/1.1 打两个端口（GET 与 POST /jy/vips.multilogin）都只回 0 字节就断，说明端口后面不是 HTTP 服务，或者要先走它自己的握手。登录报文仍未通，但端口默认值、备用键与 JSON 形状已经是可复用的抓手。

### 20.12 不含 exe 的纯脚本自举

自举在这里的定义就是**数据链路不依赖 TdxW.exe**：只靠仓库里的 Python 从内置种子造出 connect.cfg，再用它连 7709 取码表、K 线、分时、逐笔与权息，把 vipdoc 与 gbbq 填满。exe 只是在还想看图形界面时才需要，属于可选的程序层。

实测一条全链，全程不读 C:
eptune：init（复制静态文件 0 个）→ base（从 0x0450 取清单自造主表）→ fill（取 10 根日线落盘）→ verify 通过 → login 探测写 login.json。目录里没有任何 exe、dll、chrome，行情文件却已经能落。

顺带修掉一处缺口：只给 spec.json 而不走 default_spec 时，UserHost.IPAddress 会留空（模板里该键存在但为空，setdefault 补不进去）。现在 build_sitecfg 对 UserHost 的空值字段用 SEED_USERHOST 兜底，任何 spec 生成出来的配置都带落点，verify 不再报空。
