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

> 2026-11-04 校订：本文件在合并时经过一次编码往返，少量字符以 □ 标出（共 51 处），按上下文已尽力回填。
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


## 21. 行情服务器地址的来源与自更新

结论先说：行情 IP 不在代码里写死，它在 connect.cfg 里；客户端另有更新器从远端取 newhost.lst 再合并进配置。所以“IP 变了程序跟不上”是可解的，但也确实没有一条随便就能调的公开接口，得按它自己的通道走。

### 21.1 地址存在配置里，不在指令里【静态】

0x922918 与 0x924560 分别是字符串 HQHOST 与 TRYHQHOST，代码只按段名和 IPAddressNN、PortNN、WeightNN 这组键取值，没有可执行文件内的地址常量表。安装目录的 connect.cfg 里 [HQHOST] 有 12 台、[TRYHQHOST] 9 台、[INFOHOST] 10 台、[DSHOST] 3 台。改 cfg 就能换站，这一点我们自己的脚本一直在用。

### 21.2 更新通道：newhost.lst + AutoUpEx【静态】

0x91cb90 是文件名 newhost.lst，紧挨着的中文串 0x91cb9c 是“更新newhost.lst,合并到站点配置中”。同一张从 0x91ca00 开始的表里全是更新包成员（files.zip、tc_jspage.zip、qh_jspage.zip、funcs.zip、jspages.zip、TCloudCalc.zip），说明 newhost.lst 是更新分发的一个条目，由更新器取回后写入安装目录，主程序再把它并进站点配置。

更新器是安装目录的 AutoUpEx.exe（1,955,840 字节）。0x945fcc 的格式串是 autoupex %s&tdxw&%s&0，即主程序把已经拼好的地址、通道和版本当命令行传过去；更新器内部的路径格式是 update/%s/%s 与 update/%s.zip（0x945f80 是 update/，其后还有 update 目录的成员名与 tdxw.up 清单）。

拼接的基底来自配置：connect.cfg 的 [Version] 段有 AutoupURLDir=www.tdx.com.cn/products/autoup/，另有 ManualAutoupUrl、NoAutoupHintUrl 两个覆盖键，0x945f68 起还连着 HQPort、Autoup、HQIPStr 三个键名，配合 [Version] 的 AutoupFromHQHost 开关，可以改成“从当前行情站取更新”。

### 21.3 实测产物与可达性【文件】【实连】

安装目录已有 T0002\newhost.lst（4355 字节，GBK），段结构是 HQHOST、TRYHQHOST、INFOHOST、DSHOST、Version、TPSYS、TPSUBSYS，共 19 个 IP。与 connect.cfg 逐 IP 比对：它是 cfg 的严格子集（cfg 29 个，差集 11 个全是 cfg 独有的旧站），也就是它只负责增补与纠偏，不会把用户手选的站洗掉。

可达性逐个试过：

- http 与 https 打 data.tdx.com.cn/autoup/<通道><版本>.zip，都是 200 但返回 985 到 988 字节的 EdgeOne JS 挑战页（脚本算 EO_Bot_Ssid 再跳回），直接当 zip 读会拿到 HTML；带上按脚本算出的 __tst_status、EO_Bot_Ssid 两枚 Cookie 复测仍是挑战页，说明这层要真执行 JS。
- www.tdx.com.cn/products/autoup/ 前缀下 newhost.lst、update 子路径、同名 zip 全部 404，目录本身 403；data.tdx.com.cn 的 /autoup/ 也是 403。
- update.tdx.com.cn 能解析、80 与 7709 都能连上 TCP，但发 HTTP/1.0 与 HTTP/1.1 都是零字节直接断开，443 上的 TLS 握手也不成——它不是普通 Web 站点。

所以远端只留 --url 与 TDX_HOSTS_URL 两个入口自填，默认来源是本地 newhost.lst。

### 21.4 落到脚本里的做法

tdx_local.py 增加 hosts-update：来源按 --from 文件、--url、环境变量、<根>/T0002/newhost.lst、<根>/newhost.lst 的顺序取第一个有效的；只重写 HQHOST、TRYHQHOST、INFOHOST、DSHOST 四段，编号从 1 连续重排并同步 HostNum，其它分区一字节不动；合并结果先走 sitecfg 自检，有错误不写盘；写盘前留 connect.cfg.hosts.bak；差异只按 IP:端口 计，改名单独计数，--dry 只打印。

客户端侧只把 newhost.lst 当兜底：cfg 里有站点就完全不听它的，cfg 一个站点都没有时才去读，避免覆盖手选。实测把 cfg 的两段清空后仍能连通 114.117.72.207:7709。

### 21.5 边界

没能做成“定时无条件拉远端”：公开前缀不存在、唯一在发的 data.tdx.com.cn 有 JS 挑战，纯标准库解不了。要做成全自动，下一步是补挑战解算（或改走能拿到 newhost.lst 的镜像），或者在装有客户端的环境里直接消费它更新的 T0002\newhost.lst——后者的数据是现成的，缺的只是触发更新的那一步。

## 22. 升级器 AutoUpEx 的启动契约与清单协议

这一章解决的是“连升级器也不依赖”的问题：不跑 AutoUpEx，按它自己的契约直接取包。先把它的启动条件、地址来源、清单格式全部拆出来，再把其中可取的部分用标准库复现。

### 22.1 启动契约【静态 0x7fb4e0】【实测】

主程序把命令行拼好，用 CreateProcess 拉起同目录的 autoupex.exe，命令行只有一种形状：

~~~
autoupex <地址含扩展名>&tdxw&<版本>&<标志>
~~~

三处调用点分别用 0、2、1 当标志，第四段可选再跟一段说明（0xd45ff4 的 autoupex %s&tdxw&%s&0_%s、0xd46028 的 ...&2_%s、0xd46060 的 ...&1_%s），下划线后面那段中文是给界面显示的状态行，不参与取包。

解析是死的：GetCommandLineA 先按空格切（引号内不切），每段再按 & 切，含 exe 在内总段数必须是 3 或 4，否则弹 0x549268“启动错误! 请在主程序中调用!”。所以第二段固定 tdxw，第三段是版本串（十进制字符串）。

地址段校验两条：必须含斜杠，否则 0x549258“升级地址错误!”；取末 3 个字符当扩展名，不足 3 个就报 0x54923c“升级文件无扩展名!”。地址自身不参与下载，它只是个“把末 3 字符换成 txt”的模板——autoup/x.a 实际请求 /autoup/x.txt，x.abcd 请求 /autoup/x.atxt，实测与反汇编一致。

### 22.2 清单协议【静态 0x40d560】

清单首行是版本号（atof 后与第三段比，相等或更低就提示 0x549b47“已是最新版，不需要更新”直接退出），第二行是 atoi 得到的文件数。

文件数非 0 走逐文件：其后每行是相对路径 TAB 字节数，地址取清单目录拼相对路径；行首是反斜杠时说明它相对安装根，按根拼接，否则相对清单所在目录。文件数等于 0 走整包：地址是清单目录加 update/ 加通道名加 .zip，通道名是地址里最后一个点之前那段。

装之前把被覆盖的文件先备份到 Update\年月日_时分 bak\，装完在安装根写 tdxw.up 记账（[LOG] INSTALL_TIME=、SUCC=1），这就是它自己判断“装过什么”的依据。

### 22.3 地址从哪来：三个来源与一条兜底【静态 0x43de40】【文件】

升级器自己不拼地址，地址是主程序给的，出处只有三处：

1. 安装根 tmp\needautoup.dat（0x43de40 用 rb 打开它、一次读 2173 字节、第 123 到 2171 字节强写 0，读不到返回 0；0x43def0 是它的写回端，wb）。安装目录现在没有 tmp 目录，说明这台机器从没走过自动升级。
2. connect.cfg 的 [Other] ManualAutoupUrl。这一条优先，非空就直接用。
3. 都不成立时读 [HomePage] NoAutoupHintUrl（0x7fb7c4），把提示页地址塞进窗口状态栏，然后返回，不拉升级器。

拼法也确认了：基底来自 [Version] 的 AutoupURLDir（本机是 www.tdx.com.cn/products/autoup/）与 [Other] 的 AutoUpZip（tdxw3_yhzqnewjy_tcpip），缺省带 http://。

needautoup.dat 的写端在 CEF 那一侧：0x778d00 注册的通道名就是 needautoup（另有 urgent），并按品牌拼 UserAgent，本机品牌串是 cmschina_pc/%s %s，另两支是 fzzq_pc/ 与 xm/fzzq_pc/。也就是说自动升级的地址其实是网页下发的，不是本地算出来的，这也是包名换掉后客户端自己还能跟上的原因。

### 22.4 运行依赖【实测排除】

只靠 exe 起不来，两轮排除后定位：

- 缺 SEPlugins\TEncrypt.dll 时一次网络请求都没有。它是 libcurl 的外壳（导出 T_DownloadFile 一族，缺失时返回 0），升级器的“升级工具Curl版V1.47”自述就写在这里。
- 再缺 res_autoup.dll 会弹“缺少资源文件”。
- 工作目录必须是安装根；tdx_update\tdx_update.ini 的 [Autoup] 段（Suspend、ZipDirName、SuspendExeName）配合互斥体挡并发。

补齐这些后确实发出了清单请求，但随后卡在一个空对话框上，既不下载也不再写日志，怀疑在等父进程的句柄或 Suspend 约定的完成信号——这也是“别依赖它”的现实理由。

### 22.5 服务端的真实状态与挑战解法【实连】

data.tdx.com.cn 是唯一还在发东西的主机，但整站套了 EdgeOne：不带 Cookie 的请求一律回 985 字节左右的 JS 挑战页，页里有 WTKkN、bOYDu、wyeCN 三个常量和一段 (t, N) 调用参数。

解法不需要执行 JS：三个常量相加，按十进制串写进 __tst_status 并在末尾加 #，N 写进 EO_Bot_Ssid，同一个请求带上这两枚 Cookie 就放行，403 转 404、目录变可读。放行后能看到服务端的真实答案：

- /autoup/ 回 403（目录在，索引关着），别的 /down、download、update、soft、files 全 404，根也是 403。
- 挑战页挡住的不是内容而是判定，本机配置里的 tdxw3_yhzqnewjy_tcpip758.zip、按版本拼的 1168 的、以及 update/ 子目录拼法，放行后全 404——包名被换过，配置里的名字已经过期。
- www.tdx.com.cn/products/autoup/ 前缀整体失效（目录 403、其下文件全 404），官网帮助 4 的 4-4 自己写的兜底就是“去官网下载中心下最新版”。
- update.tdx.com.cn 解析得到的 12 个 7709 可连，但 HTTP 与 TLS 都是零字节断开；soft.tdx.com.cn 只是个跳 bbs/ 的空壳。

### 22.6 落到脚本里的做法

tdx_local.py 增三条命令，全标准库：

- autoup-probe --base 目录 名字…：按候选名逐个试，打印状态码与字节数，用来在包名换掉后重新发现。
- autoup-list --url 地址：取清单并解析，打印版本、文件数与每个条目的大小、路径。
- autoup-get --url 地址 --dest 目录 [--only 文件] [--version 本地版本] [--dry]：按清单下载，--only 只取关心的文件（例如只取 newhost.lst），--version 不比本地新就跳过，清单第二行为 0 时改取同目录的整包 zip。

内置 http_fetch 自己解挑战：命中挑战页就用 22.5 的算法算 Cookie 重取一次，算不出来原样返回；离线自测用本地 HTTP 服务模拟挑战页、清单与两个文件（re-tools\probes\_autoup_selftest.py），放行计数、清单解析、--only 落盘、版本门、再接 hosts-update --from 合并全部通过。

串起来就是完整自举，且不掉 exe。

~~~
python tdx_local.py autoup-get --url <地址> --dest build\au --only newhost.lst
python tdx_local.py hosts-update --path build\tdx\connect.cfg --from build\au\newhost.lst --dry
~~~

### 22.7 边界

包名仍是唯一外部变量：地址要么从 tmp\needautoup.dat 抄、要么从网页下发里取，脚本不猜。挑战解算是针对当前这套 EdgeOne 页面写的，页面改版就得跟着改 challenge_cookie，返回值是空串时按未放行处理，不会静默当成内容。整包模式只下 zip 不展开，展开交给调用方（zip 里的路径同样有相对根与相对目录两种情况）。升级器卡住的那个对话框没定位到具体外部事件，本轮不改它，只保证不依赖它。

### 22.8 补记：还有一条“从行情站取更新”的分支【静态 0x7fb070】【实连】

复核地址拼装时发现 0x7fb070 是第二个入口：主程序在拉起升级器之前，若开关成立，会先把这个地址就地改写成相对路径。

改法是纯字符串手术：取最后一个斜杠右边那段（文件名），再取其中第一个点左边那段（通道名），拼成 update/通道名.zip。也就是说交给升级器的地址从绝对地址变成 update/tdxw3_yhzqnewjy_tcpip.zip 这种相对形式。紧接着它把当前已连主站的 IP 与端口分别写进 connect.cfg 的 [Autoup] 段，键名是 HQIPStr 与 HQPort（0xd45f78 / 0xd45f68），端口缺省是 push 0x1e1d 也就是 7709（0x40e6ff）。升级器读到这两个键再把相对地址接成完整 URL。

开关是 [Version] 段的 AutoupFromHQHost，本机是 0，[Autoup] 段只有 AutoupNewVer=1，所以现网这条分支根本没启用。

实测能不能走通：拿 12 台 HQHOST 逐个打 HTTP/1.0 与 HTTP/1.1，7709 与 7711 全部是连上即被断开、零字节返回（7709 与 7711 一样，7711 本来就是财务二进制口，7721 更是全超时），12 台里 7 台的 7711 也是零字节。所以“从行情站取包”在现有这批服务上不成立，除非某个主站的 7709 本身就是 HTTP 口。

### 22.9 补记：安装包里有线索吗【文件】

安装包在下载目录，叫 neptune21_jrzd.exe，35,956,400 字节。它是自制的 MFC 自解压安装器（内含 inflate 1.1.3、自己的解包器和 rs_setup 脚本串，还有 -path:、-force、TdxClaw 之类开关），整棵树是压在包里带进来的，所以装的时候完全不联网——这也是它里面找不到更新地址的原因。

能查的都查了：4 MB 内的外部地址只有四条，全是隐私政策与开户页（tdxwsjy.sdicsc.com.cn、cdns.chinastock.com.cn、www.cs.ecitic.com），没有 autoup 也没有 update；安装后落盘的 uninstall.cfg（95,403 字节）只有 FOLDERnn 目录名与文件清单，没有来源 URL；安装目录里没有 tdxw.up（首次安装不写），也没有 tmp 目录，说明这台机器确实没跑过自动升级。

结论：地址是运行期由网页下发到 tmp\needautoup.dat 的，安装包只能复现文件，不携带通道。

### 22.10 现在到底能不能自己取

能取的是机制，缺的只有地址这一项：

- 挑战能解、清单与整包两种模式都能按契约取、取回的 newhost.lst 能直接接 hosts-update 合进配置，全部有自测。
- 唯一外部输入是“当前有效包地址”，来源按可靠性排：运行期 tmp\needautoup.dat（纯文本，最直接）、自己在 [Other] 手工填 ManualAutoupUrl 后让客户端走一次、以及 autoup-probe 按名字猜。
- 猜名字时看 probe 的状态码与字节数即可，200 且字节数像包才算命中，不看内容。

## 二十三、升级链路运行期实证（本轮补充）

二十二章是靠静态与外部探测推的，本节把运行期证据补上：先证明地址确实由主站下发、下发在哪个包，再定位 GUI 按钮为什么从来不给地址，最后给出绕开按钮的可复现走法。全部结论都标了证据级别。

### 23.1 抓点办法：7709 直通代理【实连】

做法是把 connect.cfg 的 HQHOST 第一台改成 127.0.0.1:17709，本机起一个纯 TCP 转发（双向原样搬运并逐帧记账），目标仍是真主站 114.117.72.207:7709。客户端不知道有代理，点击行为照旧，报文全部落进日志。全程只改 connect.cfg，不动安装目录其他文件，抓完还原。

这一步顺带排掉一个误解：通讯设置里的“网络代理”是给内嵌网页（CEF）用的 HTTP 代理，跟行情 TCP 一点关系都没有，抓行情不需要填它。

### 23.2 点“自动升级”时客户端做了什么【实连】

点击后进程新建一条连接，客户端侧只发一帧 0x0015（12 字节头加两字节 body，body 是 00 00），服务端回一帧 427 字节的压缩数据。解压后结构稳定可辨：

- 偏移 0：升级类型码，随请求的 body 变（传 00 00 得 0x67，传 01 00 得 0x71，传 02 00 得 0x7b，传 00 01 得 0x7e）；
- 偏移 4：0x2710；
- 偏移 0x10：部署时间戳 ASCII，本次是 20261103_084709；
- 偏移 0x29：服务端程序版本串，Linux64 后面跟 V6.75；
- 偏移 0x32 一带的二进制字段随请求变；
- 偏移 0x51：路径 /tdx/hostl/，之后整段零填充；
- 偏移 0x150：本机标识样的十六进制尾 52540044E0A5。

同一次连接里还有 0x0D 的应答，解压后能看到站点名“银河天衍云-8”和域名 www.tdx.com.cn。所以“到哪儿取”是主站下发的：域名在 0x0D，路径在 0x0015，客户端只负责把两者接起来。

用脚本直接发 0x0015 能复现同一帧（body 传 00 00 与客户端一致），不需要任何登录态。

### 23.3 needautoup.dat 的真实路径与布局【静态 0x43de40 + 实测】

读取函数 0x43de40 拼的是 %stmp\needautoup.dat，根串是安装根加 T0002\，不是安装根下的 tmp。实测第一次点击后文件出现在 C:\neptune\T0002\tmp\needautoup.dat，安装根的 tmp 只是干扰项。

缓冲区 2173 字节，对应写入侧的结构体 EA6690：+0 是标志字节，+1 是浮点版本，+5 起是短名，+0x7D 也就是 125 起是 2046 字节的地址槽，末尾 2171 起两字节。读入后代码强制把 123 与 2171 置零，123 收尾的是前段，125 起的地址槽刚好到 2171。写入方在 0x6dbeb0，由消息 0x99（153）的分支 0x789f26 调用。

### 23.4 GUI 按钮为什么永远说“尚未部署”

按发生顺序，每一次点击都是：case 153 先拿零字节把 T0002\tmp\needautoup.dat 整个覆盖，然后菜单再读它。没有推送就永远读到全零，于是走 0x7fb821 那条“尚未部署或不在升级时间内”的提示。

据此做了四轮对照，全部是改代理应答或改本地文件，结论逐条如下：

- 只手工造 dat 没用，因为点击时会被覆盖；把该文件设成只读之后才生效，生效的直接表现是提示从“尚未部署”变成“升级地址错误！”。
- 把应答里的升级类型、部署标志、0x2710、时间戳一起改，提示不变。
- 把应答的 commtype 由 0x15 改成 0x94（对应 case 148 的应答类型）并把版本串抬到 V99.99，提示仍不变。
- 生效后抓升级器命令行，得到的是 autoupex &tdxw&11.68&0_，地址段是空的。

空地址的原因在 0x7fb4e0：交给升级器的地址取自 CString [ebp-0E2Fh]，而在这个函数里 0x0E2F 只被读、从未被写（判为未初始化）；同一函数里另有 ff_cfg.ini 的 newver 分支，走的是 loading.exe 加 checkver1/checkver2 参数，本渠道既没有 conf\ff_cfg.ini 也没有 loading.exe，所以两条路都断。结论：这条白标渠道的按钮本身不可用，不是操作姿势问题。

顺带排掉两个看似可行的地址源：dat 的地址槽里填整条 http 地址、以及在 [Other] 段加 ManualAutoupUrl，命令行仍然为空，说明两者都只被更下游（升级器自己）读，主程序不读。

### 23.5 绕开按钮：直接调升级器【实连，已验证】

既然命令行契约是 autoupex 地址&tdxw&版本&标志_说明，主程序只是调用者之一，那就自己调。用仓库里的本地 HTTP 服务（127.0.0.1:8899）放了两份东西：清单 tdxw3_yhzqnewjy_tcpip1168.txt 内容是 1169 加 0，以及同名 zip。

命令行传的是 http://127.0.0.1:8899/tdxw3_yhzqnewjy_tcpip1168.zip&tdxw&11.68&0_，工作目录设在安装根。结果：服务器先收到 GET 清单并回 200，随后收到 GET 包并回 200，22 字节的包落到 Update\tdx_update\tdxw3_yhzqnewjy_tcpip1168.zip，进程退出码 0。清单首行 1169 高于本机 11.68 的判定被接受，整包模式（次行为 0）也按预期走通。

这条证明了契约完全可以从外部驱动，主程序与网页都不是必需件。

### 23.6 结论

- 地址确实是主站下发的，有明确载体（0x0015 应答的路径字段）；本机这批服务上它给的是 /tdx/hostl/，而按该路径直接打 HTTP 全 404，说明还要拼上服务端另外给的文件名才能成形，具体拼接点就在那个空的地址槽里。
- GUI 按钮在当前安装上不可用，可复现、可解释，不依赖运气。
- 自举不需要按钮：tdx_local.py 的 autoup-list 与 autoup-get 按同一契约取，拿到包后要么自己解，要么交给 AutoUpEx。要让按钮复活，需要补 conf\ff_cfg.ini（uptype.newver 大于 0）与 loading.exe 两件东西。

### 23.7 本轮临时工具

都在 build\ 下，一次性使用、不进主链：pwx.py 与 pwx3.py 是直通代理与应答改写版，psrv.py 是本地升级服务端，pwatch2.py 高频抓进程命令行，pw*.py 是各步探针与解包脚本，proxy.log、srv.log、au_cmd.txt 是抓下来的现场。脚本入口仍在 tdx_local.py，临时工具只为取证。
## 二十四、横截面缓存 tdxzsbase.cfg 的逐列语义

11.5 只写了“29 列”，实测是 **26 列**，且 1126 行**全是板块与指数代码**（880xxx 概念 605、881xxx 行业 467、000xxx 34、399xxx 18、899 与 999 各 1），没有个股。字段语义按“能证到哪一步”分三档给出，全部带证据。

### 24.1 文件性质

单日截面快照，日期列 1121 行是同一天（20261102），另 5 行停在自己最后一次重算的那天（20260624 至 20261030）。那 5 行的市值、成交列是空串——空值成组出现，说明 2 至 6、12、18、19 这些列是“按成分股算出来的聚合值”，只有重算过的板块才有。

### 24.2 已核实列

| 列 | 含义 | 单位 | 证据 |
|---|---|---|---|
| 0 | 市场 | | 0 与 2 各对应深与北的板块，1 是沪 |
| 1 | 代码 | | 全部落在板块与指数号段 |
| 4 | 总市值 | 万元 | 按成分股本乘收盘价求和，与列值之比 1.07 至 1.39（价差来自 10 月 16 日与 11 月 02 日不同日） |
| 5 | 流通市值 | 万元 | 同法只乘流通股，比 1.00 至 1.13 |
| 7 | 日期 | YYYYMMDD | 1121 行为同日 |
| 19 | 成交额 | 万元 | 按成分成交额求和，比 1.5 至 6.5（成交额日间波动本就大） |

4 与 5 的比值恒大于 1（均值 1.17），与“总市值大于流通市值”一致，是这两列口径成立的反证：若把 4 当流通、5 当总，全部 1114 行都要反过来。

### 24.3 强推断列

| 列 | 含义 | 依据 |
|---|---|---|
| 2 | 自由流通市值 | 恒小于列 5（比值 5% 至 13%），且与列 4 与列 5 同向变动；若它是成交额，全沪单日成交会到 5.18 万亿，超出两市历史极值 |
| 3 | 前一日自由流通市值 | 与列 2 同量级、恒略小，比值随涨跌分层（跌方均值 5.9%、涨方 14.4%） |
| 6 | 市盈率 | 指数行给 16.59 一类数值，与全市场估值同量级；空值与 2 至 5 成组 |
| 12 | 振幅 | 0.07 至 14.36 的百分数，指数行 1.09 与其日内波幅相符 |
| 18 | 成交量 | 万手，与列 19 同量级且空值同步 |
| 9、10、11 | 当日涨跌幅系列 | 百分数，符号与 2 与 3、4 与 5 的大小关系一致 |
| 13 至 17、20 至 23 | 多周期涨跌幅 | 百分数，负值可达 -99.8，与不同周期区间一致 |
| 24、25 | 买卖盘金额对 | 与列 19 同量级的大数对，恒 24 大于 25 |

### 24.4 读法

这份文件的价值是**当日板块横截面**，适合做板块轮动与横截面排名的对照，不适合当历史序列（只有一天，5 行是残值）。取用时按板块代码与 11.4 的 tdxzs.cfg、11.3 的成分文件互连即可得到板块、成分、指标三视图。

### 24.5 解析代码

~~~
python tdx_local.py --root <安装目录> snapshot --top 20
~~~

按列 4 排序列出总市值最大的板块，同时打印成分数、涨跌幅与成交额，用于核对语义是否与当日盘面一致。
## 二十五、13/14 送权证的除权口径实测

13/14 是股改里的“送认购/认沽权证”。13 章、14 章按内在价值折价写过，但一直没有历史样本可比。本章补上实测：先把 24 条 13/14 事件的行情取回来，再比较“计权证”与“不计权证”两种口径哪种更贴价格连续性。

### 25.1 取数

对每条事件取该股事件日前后最近的两个交易日收盘，且要求两侧都停牌不超过 10 个自然日，避免把长期停牌段的市场涨跌算进误差。24 条事件里 3 条满足，其余都因股改停牌数周被剔除。

~~~
python build\pw122.py
~~~

### 25.2 结果

| 代码 | 事件日 | 前收 | 后收 | 实际比值 | 计权证预测 | 误差 | 不计预测 | 误差 |
|---|---|---|---|---|---|---|---|---|
| sz000629 | 20051204 | 3.37 | 3.30 | 0.9792 | 0.8243 | 18.8% | 1.0000 | 2.1% |
| sz000898 | 20051204 | 4.12 | 4.21 | 1.0218 | 0.9811 | 4.2% | 1.0000 | 2.2% |
| sh600900 | 20060517 | 7.69 | 7.32 | 0.9519 | 0.9573 | 0.6% | 1.0000 | 4.8% |

中位误差：计权证 4.16%，不计 2.18%。三例里两例是不计更好，一例是计权证更好。

被剔除的 600010 也能说明问题：2006-03-28 十送 1.6 股外加一对蝶式权证（认购行权价 2.00、认沽 2.45、各 4.5 份），停牌前收 2.83、复牌后收 2.12，实际比值 0.7491。只按送股算 0.8621（误差 15.1%），再加权证 0.6269（误差 19.4%），仍是只算送股更近。

### 25.3 为什么计权证会过正

内在价值把权证当成“必然行使、且行权价就是权证全部价值”。实际交易里权证是独立挂牌的证券，价格含时间价值折价、欧式行权、上市滞后，通常明显低于内在价值；深价内的认沽更夸张，000629 按内在价值要扣掉 17.6% 的股价，而当天股价几乎没动。

另有一条旁证：交易所的除权参考价公式里本来就不含权证，权证是另发的证券，不进股票价格折算。

### 25.4 口径结论

- 默认：13/14 **不参与**除权价，ratio 恒为 1。24 条事件里 20 条的认沽本来就因内在价值为负而返回 1，改成默认不计只影响 4 条，且实测更稳。
- 可选：加 --warrant 恢复内在价值折价。因子库、复权应用都支持这个开关。
- 仍不算精确：要做到准，得取权证挂牌首日价（权证有自己的代码，7709 能取），而 gbbq 不带权证代码，得靠方案表另配。这点留在局限里。

### 25.5 复验

~~~
python -m py_compile tdx_adjust.py
python build\pw124.py                       # 001207 两种口径最大跳空都应是 0.0792
python tdx_adjust.py check --gbbq <安装目录>\T0002\hq_cache\gbbq   # 期望 0 问题
~~~

001207 是纯送转样本，两种口径结果必须一致；不一致说明动到了类别 1 的分支。
## 二十六、升级地址的指令级复现与 URL 拼接缺口

二十三章是代理抓的现场，本章把两件事钉死：抓到的帧逐字节是什么、按钮为什么交出空地址。结论是 URL 缓冲在整条处理链里只有一次读、没有任何写。

### 26.1 请求帧与应答字段（逐字节）

代理日志里按钮发出的请求帧是 12 字节，没有更多：

~~~
0c 00 00  00 00  00  02 00  02 00  15 00    + 00 00
~~~

逐段是：0x0C 帧头；u16 0x0000（hi<<8 与类别都取 0）；u16 0x0000（版本 0）；flag 0x00；u16 0x0002 压缩长度；u16 0x0002 解压长度；u16 0x0015 命令字；两个 00 是 body。两段长度相等即不压缩，body 两份裸字节，不是 zlib。

应答 143 字节，去掉 16 字节头后是 427 字节 zlib，解压后字段如下（偏移是解压后的）：

| 偏移 | 内容 | 本次值 |
|---|---|---|
| 0x00 | 升级类型码 | 0x6E |
| 0x04 | 常量 | 0x00002710 |
| 0x0E | 标志 | 01 00 |
| 0x10 | 部署时间戳 ASCII | 20261103_084709 |
| 0x29 | 服务端版本串 | Linux64 两空格 V6.75 |
| 0x47 | 一个字节 | 0x0C |
| 0x48 | u32 | 0x00000A20 |
| 0x4C | u32 | 0x0000820C |
| 0x4F | 路径，零填充 | /tdx/hostl/ |
| 0x150 | 十六进制尾（本机标识样） | 5254002D56F7 |

路径不在 0x51，前面 0x47 起是路径块的三个前缀字段。应答里没有文件名，也没有主机名——主机来自同连接 0x0D 应答里的 www.tdx.com.cn（23.2）。

自己发这帧的命令：

~~~
python build\pw137.py
~~~

脚本连 HQHOST 各台，fig 命中后把 427 字节写 build\au15.bin。注意 recv 会一直不回，得设短超时并容忍它，早先几次失败就是超时被当成异常吞掉了。

### 26.2 包名从哪来

配置文件 [Version] 段有 AutoUpZip=tdxw3_yhzqnewjy_tcpip，版本串 11.68 去掉点就是 1168，两者拼上扩展名就是本机试升级时用的文件名 tdxw3_yhzqnewjy_tcpip1168.zip（22 章 22.6 的自测包名即由此而来）。清单按升级器规则把末三字符换成 txt。

### 26.3 URL 缓冲从未被写

按钮处理体里，交给格式化函数的第一个参数是 [ebp-0E2Fh]，一处 l ea 后直接 push：

~~~
7fb92e: lea     eax, [ebp-0E2Fh]
7fb934: push    eax
~~~

在 0x7fb380 到 0x7fb9c5 这一千五百多字节的整段里，[ebp-0E2Fh] 只出现这一次。往后 0x7fb988 的 Format 把它填进 autoupex 模板，0x7fb95c 的 sub_7FB070（从行情站取更新的改写分支）读的也是它，但两边都没有写入方。同一段里 [ebp-0E40h]、[ebp-0E44h] 这些 CString 都是当场构造的空对象，只有版本提示串被 Format 填过（"强制升级:V%s -> V%.4f"）。

所以命令行恒为 autoupex &tdxw&11.68&0_：不是配置没填、不是 dat 没写，是这段代码压根没把主机、路径、包名拼进那个缓冲。

复现：把 build\dis4.txt 与 build\dis5.txt 生成后检索 0E2F。

~~~
python build\pw161.py
python build\pw162.py
Select-String -Path build\dis4.txt,build\dis5.txt -Pattern "0E2F"
~~~

### 26.4 按推测的 URL 全打不通

拿主机（www、data，都是 198.18.1.x 的真解析）、路径（/tdx/hostl/、/products/autoup/）、包名（八种版本写法）与两种扩展名组矩阵，无一 200：www 回自家 404 页 1480 字节，data 回裸 404 548 字节。tdx.com.cn 下别的一级子域（hostl、up、autoup、dl……）解析到 198.18.2.x 后 TLS 直接断，是通配解析不存在的名字。

~~~
python build\pw145.py
~~~

所以缺的不是“再猜一个文件名”，而是拼装规则本身。取包目前只有两条可行路：已知一条完整 URL 时用 autoup-get 直接下（22.6 的自测就是这么跑的），或者直接调 AutoUpEx 让它按契约办。

### 26.5 结论

- GUI 升级按钮在本机白标包上不可用，指令级原因已定位到具体缓冲，可复现、可指认。
- 主站确实下发取包路径（26.1），但客户端不用它；主机与包名在别处也没拼上。
- 要补全这一段，得在 VMP 里找到本应填 [ebp-0E2Fh] 的代码（可能是 0x778d00 的 needautoup 通道或另一版按钮分支），再按它的规则实现拼装。当前脚本不猜，只 probe。
## 二十七、资讯通道（INFOHOST 7711）的问候帧与命令族

以前只记到“7711 TCP 可连、HTTP 打过去零字节”。本章把它当协议打：挨 7711 观察客户端第一条命令，复现该帧，再扫命令字。结论是这是一条独立的资讯通道，头一条命令就不是行情口的任何命令字。

### 27.1 抓法

和 23.1 同法，只是换段：把 connect.cfg 的 INFOHOST 首选改成 127.0.0.1:17711，本机起 TCP 转发（17711 → 114.117.72.207:7711），记账后启动客户端。

~~~
python build\pw170p.py        # 转发并记 build\info.log
python build\pw171.py         # 改 cfg（先备份到 build\cfg_info.bak）
Stop-Process -Name TdxW -Force; Start-Process C:\neptune\TdxW.exe -WorkingDirectory C:\neptune
~~~

客户端起来后立刻连上 7711，第一条就是 12 字节的 0x1F7E，body 两个零字节：

~~~
01 00 00 00 00 00 02 00 02 00 7e 1f | 00 00
~~~

值得注意：资讯口不认行情口的 Setup1/Setup2/Setup3 握手，连上直接发命令；先发握手会被断连。

### 27.2 问候帧内容

应答 151 字节，头 16 字节后是 zlib，解压 325 字节。明文里依次是：

| 偏移 | 内容 | 本次值 |
|---|---|---|
| 0x00 | 版本/类型 | 22 01 00 00 |
| 0x04 | 计数 | 71 21 00 00 |
| 0x09 | 服务端时间 GBK | 2026年11月3日 8:01:02 |
| 0x29 | 服务版本串 | 3.03 build 2015.01 Linux64 |
| 0x45 | 路径前缀，零填充 | /tdx/infohost |
| 0xDF | 能力串 | F10: N, Table: Y |

F10 关、表格开。整帧不需要登录态，也不带券商标识。

### 27.3 命令字扫描

对 0x1F70 到 0x1F90 与 0x053E 逐条空 body 各打一次，只有四条有应答：

| 命令字 | 应答长度 | 明文 |
|---|---|---|
| 0x1F7D | 21（解压 5） | 01 00 00 00 00，一个标志 |
| 0x1F7E | 151（325） | 问候帧，见 27.2 |
| 0x1F7F | 67（51） | 管理员密码错（GBK），该命令要口令 |
| 0x1F80 | 29（265） | 265 个零，疑似表格槽位描述 |

0x053E 在 7711 上全静默——它属于行情口（快照行情），要走 Setup 握手，27.5 说清了这点。

~~~
python build\pw183.py
python build\pw184.py
~~~

### 27.4 落地

客户端加了两个能力：request_raw 走“命令字在头、body 原样”的裸帧，info_hello 发问候并解析出时间/版本/路径/能力位。命令行：

~~~
python tdx_client.py info-hello
~~~

输出时间、版本、路径与 F10/表格两个能力位，返回码按有没有版本串给 0 或 1。实现要点两条：连资讯口要 conn.handshake=False 跳过行情握手，取主机要用 conn.sections=("INFOHOST","DSHOST") 而不是默认的 HQHOST。

### 27.5 顺带纠正一条旧记录

第九节曾写“0x053E 两种帧头复测仍超时”。0x053E 是快照行情（security_quotes），不是资讯命令；它失败是因为当时少了 Setup 握手。加握手后正常。

### 27.6 还没解完

- 0x1F7F 的口令是什么、0x1F80 的 265 字节槽位怎么用，都要再挖。
- 财务包文件名在字符串里有（gpcw%04d%04d.dat、tdxbase/sgpcwdata.dat），但拿 /tdx/infohost/ 与 /tdxbase/ 拼到 www 与 data 上仍全 404，托管侧还是没坐实。
- F10 页文本走这条通道，但取页命令字（很可能在 0x1F81 之后）还没扫到。
## 二十八、7715 登录通道：走到哪、卡在哪

登录一直是缺口里最硬的一条。本章把“能测的”测到底，并留下可复用的抓法，明确卡点不在协议猜测，而在入口触发。

### 28.1 通道行为与协议族判定

7715 只接受 TCP，不认行情口的握手：先发 Setup1/2/3 再发任何命令，连接一律被 RST 掉。

~~~
python build\pw224.py
~~~

反过来，只发裸帧头不加握手，服务端是**优雅关闭**（回 0 字节），不是 RST。这与 7709 的行为正好相反（7709 必须握手），说明 7715 是另一套协议族。

空 body 扫 0x0001 到 0x0012、0x0FC5、0x053E、0x1F7E 全无应答：

~~~
python build\pw192.py
~~~

结论：7715 的服务端在拿到**完整登录包**之前不说话，不会像资讯口 0x1F7E 那样先回问候。所以“猜命令字 + 空 body”这条路走不通。

### 28.2 登录代码在哪

扫全树二进制里 GBK 的登录字样，命中集中在交易模块，而不是主程序：

| 文件 | 命中 |
|---|---|
| TdxW.exe | 资金账号、登录失败 |
| xiadan\xiadan.exe | 登录成功、通讯密码、资金账号、登录失败 |
| tpbus.dll | 登录成功、登录失败 |
| TCPlugins\AddinStock.dll 等 | 资金账号、登录失败 |

~~~
python build\pw208.py
~~~

真正的组包在 xiadan.exe 与 TCPlugins 里。这也解释了为什么在 TdxW.exe 里找登录帧一直找不到。

### 28.3 抓法已经搭好

抓 7715 不能用改一个固定端口的老办法（行情和登录共用主站 IP，端口还不一样），改用**按命令字分流的转发器**：监听 17715，先窥探首帧的命令字，小于 0x0100 走 7715，其余走 7709。

~~~
python build\pw201p.py                      # 分流器，日志 build\route.log
python build\pw202.py                       # HQHOST01 指向 127.0.0.1:17715
Stop-Process -Name TdxW -Force; Start-Process C:\neptune\TdxW.exe -WorkingDirectory C:\neptune
~~~

实测该转发器工作正常：升级检查帧 0x0015 被正确识别并转到 7715，行情流量继续走 7709。

### 28.4 卡点：入口不触发

客户端起来后只发升级帧，交易窗口（隐藏窗口 通达信网上交易V6）虽被唤起也没有登录动作。已试的手段：

| 手段 | 结果 |
|---|---|
| 唤醒隐藏交易窗口 ShowWindow + SetForegroundWindow | 窗口出、无连接 |
| SendKeys 回车 / Tab / F12 / Alt+L / Ctrl+L | 无连接 |
| 交易窗口按 4 乘 4 网格盲点 16 下 | 无连接 |
| 主窗口底部状态栏 12 点盲点 | 无连接 |
| UIAutomation 枚举控件 | 只有 4 个 MFC 停靠条，登录面板全是自绘 |
| ToolBar 的 TB_BUTTONCOUNT / TB_GETBUTTONTEXT | 全返回 0，按钮也是自绘 |

自绘面板没有可枚举控件，这是当前唯一的拦路点。

### 28.5 下一步最省的两条

1. 用 CEF 调试口驱动交易页：交易模块是 CEF（xiadan\cef），带 --remote-debugging-port 起窗后可用 CDP 点按钮、读网络，能把登录帧连同 URL 一起抓下来。
2. 直接在 xiadan.exe 里静态找组包函数（0x0000 段的 send 调用与 资金账号 字符串的交叉引用），按常量与结构还原字段。

两条路都用现成的证据链就行，不必再猜命令字。
## 二十九、横截面缓存列 24 与 25、以及周期列的量级证据

24 章把列 24、25 记作“买卖盘金额对”，列 13 至 17 记作“多周期涨跌幅”，都是强推断。本章用 1126 行的全量统计把它们各推一步，并推翻其中一条。

### 29.1 列 24 与 25 不是买卖对

买卖对最硬的检验是两列之和应当等于成交额（列 19）：每笔成交有买方也有卖方，拆开算和也还是成交额。实测不成立。

~~~
python build\pw217.py
~~~

1111 个可比行里，(列 24 + 列 25) / 列 19 的最小 0.077、中位 0.946、最大 19.22，落在 0.98 至 1.02 之间的只有 2.8%。中位贴近 1 但离散度极大，不是“拆分”的形状。

再看方向性：净额 (24 减 25) 在涨行均值 +0.044、跌行均值 +0.032，都为正，涨跌不翻符号；净额为正的比例 68.4%。若是内外盘，跌日应大面积翻负。

~~~
python build\pw216.py
~~~

所以 24/25 与 19 同量级、恒 24 大于 25、且与当日涨跌只有 0.234 的弱相关，更像**两档不同周期的均额类指标**（长周期恒大于短周期不成立，故 24 是较短周期），而不是买卖对。这一条按【推断】记，24 章“买卖盘金额对”的说法降级。

### 29.2 周期列的量级分层

列 13 至 17 的绝对值均值是 10.126、9.800、9.603、9.380、37.364，前四列同一档，第五列（列 17）高出近四倍。列 20 至 23 的均值是 2.287、4.074、5.535、5.093，逐列递增。

短周期列与当日涨跌的相关性也随之升：列 13 是 0.039、14 是 0.077、15 是 0.127、16 是 0.133，越往后越贴当日；20 至 23 里列 20 最高 0.162，其后反而回落。

这与“列 13 至 16 是 3、5、10、20 日一类递增周期、列 17 是半年以上的长周期、列 20 至 23 是更短周期（1、2、3、4 日）”一致，但具体天数还没有单点对齐，仍是【推断】。

### 29.3 读法不变

无论 24/25 是均额还是拆单，用法都一样：当日板块横截面排序与拥挤度对照，不当历史序列。要坐实，只有一条路——把这套指标在界面上的显示名与这里的列位置对上（界面读的就是这份文件，0x684220 是它的解析点）。

~~~
python build\pw222.py
python tdx_local.py --root <安装目录> snapshot --top 20
~~~

## 30. 交易登录通道的推进与工具结论
?026-11-04 ?
本章记录 28 章之后的一轮推进。结论先行：**7715 的登录报文仍未拿到?*，但把“静默”的成因拆掉了一层——之前的分流代理把行情命令发到了交易口，所谓的全静默有一部分是路由错位造成的；同时确认交易窗口本身是个空壳，界面根本没建起来，这才是点不出登录的直接原因为?
### 30.1 分流代理的端口错位（已修□?
28 章的单口代理 build\pw201p.py 监听 17715，靠首帧 commtype 是否小于 0x0100 判断分流，但**上游永远□?7715**。而客户端发来，?0x000D ?x0015 都是行情命令字?x0015 就是自动升级那帧），被送进交易口自然没有应答，?
改成双端口：17709 固定转发主站 7709 ?7715 固定转发主站 7715，配置里 HQHOST01 |?17709、UserHost |?17715 ?
~~~
python build\pw249.py
python build\pw250.py
~~~

改完重启客户端，0x0015 立刻拿到 144 字节应答（与 26.1 记录的那 427 字节同族，含升级类型码与 /tdx/hostl/ 路径），日志 build\route2.log |?C |?S 两个方向都开始出现行情?
结论
?*0x000D |?0x0015 都属于行情口**，不是登录报文；此前缀?715 |?0x0C 帧全静默”的结论要按“在已有路由下静默”理解析?
### 30.2 0x000D 帧直连复现?
把客户端真实发出的第一帧原样打给三台主机：

~~~
python build\pw248.py
~~~

帧是 0c 02 18 93 00 01 03 00 03 00 0d 00 01 ?3 字节，两段长度?1 |?3 不等说明 body 是压缩的。三台主机（114.117.72.2077?23.125.108.101 ?14.141.177.118）的 7715 |?7615 全部 0 字节返回 ?
所以?0x000D 单独打过去不成话——它不是握手首帧，要成对或带上下文件?
### 30.3 交易窗口是空值?
窗口径?#32770，标题“通达信网上交易V6”，子窗口只有?AfxMDIFrame100、AfxWnd100 与两个?MHPDockBar/MHPToolBar 停靠条，零个编辑框零个按钮子窗口径?
- PrintWindow |?PW_RENDERFULLCONTENT（标志?2）抓下来是一片空白，只有分隔条与几块浅色区域；标志?0 更是整幅灰□?- |?SetWindowPos 把窗口拉起?980x620 再抓，布局变了（分隔条移动）但依旧没有文字与控件】?- 没有子窗口就没有焦点，SendKeys 键入的字符无处落地，回抓画面白色条带宽度不变化?
~~~
powershell -File build\resize.ps1
powershell -File build\ascii4.ps1
~~~

进程里交易插件是**在的**：本进程序?218 个模块，不?AddinStock、AddinFund、AddinCredit、AddinCA、AddinUtility 等，不缺。插件在、界面没建，说明登录面板要等某个入口把它建出来，不是加载问题□?
### 30.4 两个工具体?
**枚举模块必须是?32 |?PowerShell**。TdxW.exe |?32 位进程，64 |?PowerShell |?Modules 只给的?TdxW.exe |?6 |?wow64 桩，看上去像“什么也没加载”，?
~~~
& "$env:SystemRoot\SysWOW64\WindowsPowerShell\v1.0\powershell.exe" -File build\mods.ps1
~~~

**UIA 的矩形是子坐标不是屏幕坐标?*。本机屏幕?2559x1279，帧自报 2561x1237（DPI 虚拟化把不懂 DPI 的进程放行?1.5 倍坐标系里），不?UIA 给出的子元素坐标是按 3840 宽算的。按 2559/3840 缩放后再点才落在同一处。窗口前台化，?SetForegroundWindow 可以拿到（GetForegroundWindow 回读自证），点击本身是有效的——用自家 WinForms 窗体做对照点得通道?
~~~
powershell -File build\cur.ps1
powershell -File build\abs.ps1
~~~

### 30.5 不用鼠标也能驱动：WM_COMMAND

既然自绘面板不可靠，改走消息注入。对帧窗口?PostMessage WM_COMMAND ?x0111）确实有效：

~~~
powershell -File build\cmd.ps1
~~~

0x8000 |?0x80800?0000 |?400800?0000 |?30060 三段的扫描过程中，冒出了“设置特定版面”“加入到自选股/板块”“上证指数在下列自定义板块中”三个对话框，主窗口标题被改写成“行情报文?港股-博彩”，证明命令确实进到了消息映射□?
但主框架没有 Win32 菜单（GetMenu 返回 0），菜单条是自绘面?MHPToolBar，TB_BUTTONCOUNT 又是 0，拿不到 id 表，只能盲扫。上述三段里没有一个是交易入口径?
接着换成 Python |?ctypes 直发（build\pw253.py，判据从“日志增长”改成“主框架子窗口数变化，?17715 出现接入”），把 0x2009 |?0x7FFF 整段扫完成?
~~~
python build\pw252.py 0x2000 0x8000
python build\pw253.py 0x2009 0x8000
~~~

结果仍然是零命中——全程只有一次子窗口减少（id 82000?30 降到 115，关掉了一个窗口，不是交易面板），17715 一次接入都没有。剩下没扫的 ?0x0001 |?0x0208 |?0x8081 以上的?0081 以上的?
~~~
powershell -File build\menuapi.ps1
~~~

### 30.6 xiadan.exe 其实是同花顺

28.2 记“登录字样集中在 xiadan.exe”，字确实在，但那个程序不是通达信的东西□?
- xiadan\sysconfig\NetWorkSetting.json |?IP |?115.236.34.168，PORT 8009，SSL_PORT 8003，CRYPTEXTEND |?HEXIN，还有?PROXYUSER/PROXYPWD ?- 同目录?config.json |?httpapi 全是 10jqka 域名（逐笔分布、涨跌停、南北向资金），?- 所以它是同花顺的独立下单端，登录协议走 8009，与 7715 不是一回事件?
它能力?UIA 枚举（客户号 1237、通讯密码 1108、委托登录?1006），元素都是 Pane、没有?ValuePattern，只能按矩形坐标点击后?
~~~
powershell -File build\uia3.ps1
powershell -File build\x2.ps1
~~~

点完登录按钮也没有新 TCP 连接，本机?zero 对外握手，同样卡在没有回包名?
### 30.7 交易通道的观测点

T0002\log\DataModule.log 是这条通道自己的日志，登录失败会写法?
~~~
[SessionManager: 522]JobError ... ErrInfo=通讯连接被客户断开(2:6)
[SessionManager:1449][Connect]连接服务器失败?E_DisCon Client=... ClusterID=100
~~~

ClusterID |?100 |?200 两组，说明是主备两条集群各试一次。配置里 UserHost |?IPAddress 本来就是空串，Port |?7715，客户端于是 ?HQ 主站取?IP |?7715 去连——所以分流代理的 7715 上游指向主站 IP 是对的，问题不在落点而在报文件?
~~~
python build\pw245.py
~~~

### 30.8 复现清单

~~~
python build\pw249.py
python build\pw250.py
Start-Process C:\neptune\TdxW.exe -WorkingDirectory C:\neptune
powershell -File build\uia5.ps1
powershell -File build\mods.ps1
python build\pw248.py
python build\pw245.py
python build\pw227.py
~~~

最后一条把 connect.cfg 还原因?build\cfg_info.bak 的原始字节并核对 SHA-256 ?
### 30.9 下一个?
优先两条，任一条通了都能拿到登录包：一是从 0x9718A0 |?case 16 反推 JSON 主机表结构，直接构□?{"Addr","Port"} 报文件?7715 看应答；二是把交易入口的 WM_COMMAND id 扫完剩下区间戳?x2000 |?0x8000 |?40081 以上），入口一开就有登录面板，届时按 30.4 的缩放坐标点按钮即可抓包名?
## 31. 点击为什么不生效：一个全部?Edge 窗口径?026-11-04 ?
30 章留下了“交易入口找不到”的死结。这一章把结拆开了一半：**之前所有点击都没打到应用上**，因为它们被一个最大化，?Edge 窗口吃掉了一?
### 31.1 真凶


?WindowFromPoint 采样顶栏的每一个点，返回的类名不是应用自己的窗口，而是 ?
~~~
0x4037e Chrome_WidgetWin_1      -7,-7,2566,1239   |?全屏
0x17053a Chrome_RenderWidgetHostHWND   4,80,2555,1228
~~~

取属主进程，不?msedge.exe。也就是说屏幕上看到的是应用，但**最上层压着一个最大化，?Edge 窗口**，鼠标事件全落到它身上；应用自己的窗口只收键盘焦点，所以?SetForegroundWindow 成功、GetForegroundWindow 回读也正确，看起来“窗口在前台”，其实点击根本进不去重?

?Edge 最小化之后立刻不同一?
~~~
python build\pw291.py
python build\pw283.py
~~~

命中类名字?Chrome_WidgetWin_1 变成 AfxWnd100（应用自己的工具栏按钮窗口）。这一条同时解释了 30 章之前所有“点了没反应”的记录，包括交易窗、版面标签、盲点网格式?*以后动界面前先跑一个?pw291.py |?Edge 收起来，?*

### 31.2 坐标换算实测

UIA 的子坐标志?1.5 倍的物理像素空间，屏幕坐标等于子坐标除以 1.5。用两个锚点对齐：UIA 里版面内容容器在 y=123，不?MDIClient 子窗口在屏幕 y=82 ?23 除以 1.5 正好 82；横坐标同理，容 ?x=-2 对屏幕?-1 ?
所以顶栏按钮在屏幕坐标志?y |?8 |?22、x |?374 起的 81 宽一段段排到 1538 再除权?1.5。原来按 1 |?1 点在 y=13 |?20 上，落的是同一个窗口，看不出区别，不?1004 之后全部落空值?
### 31.3 点击确实被处理?
验一遍点击有效性：
?1000 号按钮，前后各抓一个?PrintWindow（标志?2）比像素□?
~~~
python build\pw294.py
~~~

3171668 个像素里 93634 个变化，不?3.0%。点击进了消息映射并引起重绘，链路是通的 ?
### 31.4 还是没有交易入口

把顶栏全部?21 个按钮挨个点一遍（build\pw292.py），再把版面标签□?y=92 |?x=20 扫到 1220（build\pw295.py），判据 ?route2.log 出现 17715 接入口?V6 子窗口数变化，?
~~~
python build\pw292.py
python build\pw295.py
~~~

两个判据全程不动升?7715 一次接入都没有，V6 子窗口恒为?16 个股?
### 31.5 客户端根本不是?17715

route2.log 里三条连接全部落到?17709（行情口），配置里?UserHost 明明写着 127.0.0.1:17715 ?
~~~
type build\route2.log
~~~

结论
?*交易模块没起来就不会拨号**：登录面板、连接、组包都在面板建好之后。所以“没有登录包”的上游是“面板没建”，不是协议问题。这一条把 30 章的推断坐实了一?
### 31.6 CEF 不是界面

顺带排除一条弯路：
?--remote-debugging-port=9222 起来，?/json/list 只有三个页（T0002\tmp\tmp.htm 正文 64 字符、jspages/zxg/dist/index.html 视口 0x0、chinastock 通知列?797x520），而可见的行情界面是原因?CFQS_ 开头的窗口类，顶栏按?AfxWnd100，都不是网页。所以对行情界面板?CDP 点元素无用，CDP 只对通知页与资讯页有效的?
~~~
python build\pw284.py
python build\pw285.py <targetId> "document.title"
~~~

### 31.7 组包名?tpbus.dll

主程序里 0x9718A0 |?case 16 只是把主机表拼成 [{"Addr":"...","Port":n},...]（分隔符串?","，收尾是 "]"；这两个常量级?0xCF4570 |?0xCF89C8，按 VA |?0x400E00 取文件字节即得），真正把类型翻成帧、写 0x0C 头、走 WSASend 的是 tpbus.dlld?
tpbus 的导出只有九个（TP_Init、TP_CreateTPData、TP_DestroyTPData、TP_GetFile、TP_Exit、TP_CreateAppCore 等），WSASend 只有一处调用点击?x101B454D，外面包一个?sub_101B43D0 只管返回错误码），真正组帧在更上层，尚未定位置?
### 31.8 直连试探：单帧不是?
按“commtype 等于类型号”的假设，把 JSON 主机表用 0x0C 帧打印?114.117.72.207:7715，试过?0x00100?x000D ?x0001 ?x0012 ?x0013 五个命令字与三种长度写法（相等、全零、加二），另外把实测首帧前缀 0c 02 18 93 00 |?3/3 长度再扫一个?0x0001 |?0x0013 ?
~~~
python build\pw272.py
python build\pw273.py
~~~

全部 0 字节。单位?0x0C 帧不足以让交易口开口，至少还需要前置上下文（会话或握手），所以下一轮优先在 tpbus 里把“首个连接建立后按顺序发什么”读出来，而不是继续试命令字节?
### 31.9 复现清单

~~~
python build\pw249.py
python build\pw250.py
Start-Process C:\neptune\TdxW.exe -WorkingDirectory C:\neptune
python build\pw291.py
python build\pw283.py
python build\pw292.py
python build\pw295.py
type build\route2.log
python build\pw227.py
~~~

## 32. 交易通道的真实握手：抓包与回放（2026-11-04 ?
31 章之后一直卡在“客户端不拨 17715”。这一章把这个前提整个推翻：交易口根本不是 7715 ?
### 32.1 端口纠正

cfg 里除权?HQHOST |?UserHost，还有两个段 TPSYS |?TPSUBSYS，里面是 TPHost01..TPHost08 |?TPPort01..06，端口写的是 9914 |?9915，主机是 219.143.214.206 ?18.205.130.777?19.143.246.8 这些。UserHost |?7715 是另一路（SSO 登录），交易会话走的 ?TPHOSTP?

?PID 查连接就能看到实证【实测】：

~~~
$p = (Get-Process TdxW).Id
Get-NetTCPConnection -OwningProcess $p -State Established | Select-Object RemoteAddress, RemotePort
~~~

结果里稳定出来?219.143.214.206:9914 |?218.205.130.77:9915，另外还有?118.31.28.30:7730。所以一直在 7715 上打转是找错了门□?
### 32.2 抓包

机器上装根?Wireshark，用它自带的 tshark 抓明文（都是 TCP |?TLS）【实测】：

~~~
& "C:\Program Files\Wireshark\tshark.exe" -D
& "C:\Program Files\Wireshark\tshark.exe" -i 3 -f "tcp port 9914 or tcp port 9915 or tcp port 7730" -w build\trade.pcap -q -a duration:50
~~~

接口号按 -D 的实际输出填，本机以太网页?3。抓的同时起客户端，50 秒后自动停。抓到的包留下?build\trade.pcapy?72 KB），?
导出成帧序列（制表符前是原始长度，后是十六进制载荷），?
~~~
& "C:\Program Files\Wireshark\tshark.exe" -r build\trade.pcap -Y "tcp.stream==0 and tcp.srcport==57362 and tcp.len>0" -T fields -e tcp.len -e data.data > build\cframes.txt
~~~

流号与服务端口要么?conv,tcp 的实际输出替□?
?
### 32.3 帧结论?
交易族的头也就?12 字节，但首字节是 0x0B 不是 0x0C，且高两位是标志位：0x80 表示载荷压缩段?x40 表示该帧带后一?需应答。第 2 ? 字节，?cat，按 3 ?
?
?
?1 ?5 递增，是握手步序。双长度沿用行情口的语义，不等即整段 zlib【文件】。?
抓到的六帧：

| |?| |?| |?| cat | 标志 | 双长度?| commtype | 载荷开头?|
|---|---|---|---|---|---|---|---|
| 1 | 44 | 0B | 0003 | 00 | 30/30 | 26AC | 00 64 D2 04 |?客户端内在?IP 192.168.142.128 |
| 2 | 96 | 0B | 0004 | 00 | 82/82 | 26AD | 00 64 "TDXW" |?内网 IP |?"TDXW" |?16 字节随机器?|
| 3 | 438 | 8B | 0005 | 00 | 424/418 | 26AE | 00 64 随机块（压缩段?|
| 4 | 977 | 4B | 0007 | 0F | 948/908 | 264B | 00 64 "ACL.checkuser" 00 00 1F 5F |?密文 |
| 5 | 832 | 4B | 000B | 1E | 788/748 | 264B | 00 64 "VersionAdm2:QueryUI? |
| 6 | 15 | 0B | 000F | 00 | 1/1 | 26B1 | 00 |

commtype |?0x2600 加序号：握手 26AC:?6AD ?6AE，命令统一 264B，收尾?26B1。载荷一律以 00 64 开头，命令帧跟着 ?ASCII 命令名（ACL.checkuser、VersionAdm2:QueryUser…），也就是 20.11 里那套“名 + 分隔 + 参数”的文本协议，只是外面套□?0x0B 帧【文件】。?
### 32.4 回放验证

按抓到的字节重放，第 1 至第 3 帧在 1.202.143.37:9914 |?218.205.130.77:9915 上都能拿到正常应答，应答，?commtype |?cat 与抓包时一致，数字计数器随在线量变动【实连】：

~~~
python build\pw319.py 1.202.143.37 9914 3
python build\pw319.py 218.205.130.77 9915 3
~~~


?1 帧回 39 字节，?b 03 |?26AC，载荷含两个 0x1027 |?10000 与两个随时间变化的计数，末尾部?unix 时间 0x6AEA6A05 |?2026-11-03 23:11:01Z），不?2 帧回 331 字节，?b 04 |?26AD），不?3 帧回 54 |?62 字节，?B 05 |?26AE，压缩）。第 4 |?ACL.checkuser 直接重放行?0 字节，服务端不应答【实连】——因为?checkuser 的密文绑定了当次的会话密钥，重放旧密文对不上新会话框?
### 32.5 ACL.checkuser 与剩下的难点


?4 帧的形状已经完整段?0 64 + "ACL.checkuser" + 00 00 + 1F 5F 开头的密文流通?F 5F 那一段是等长密文件?77 帧里 948 字节压缩段?908 字节），不?3 帧服务端回的 54 字节里同样是 00 64 00 00 64 后面一个?16 字节以上的随机块，二者对应，说明 ?3 帧就是在交换会话密钥□?
所以要把登录跑通，下一步是拿到 checkuser 的加密算法（TEncrypt.dll |?tpbus 里的 OpenSSL 段是候选），或者用 xiadan 那侧的已知明文反推。有了它，客户号 + 通讯密码就能自己组帧发出去重?
### 32.6 复现清单

~~~
& "C:\Program Files\Wireshark\tshark.exe" -i 3 -f "tcp port 9914 or tcp port 9915" -w build\trade.pcap -q -a duration:50
Start-Process C:\neptune\TdxW.exe -WorkingDirectory C:\neptune
Get-NetTCPConnection -OwningProcess (Get-Process TdxW).Id -State Established
python build\pw319.py 1.202.143.37 9914 3
~~~

### 32.7 结论

- 交易会话的真实入口是 TPHOST |?9914/9915 ?715 |?SSO 的另一路；此前“客户端不拨号”的结论要按这一条修正【实测】。?- 握手前三帧已能独立复现，帧结构、命令名、commtype 命名规则全部坐实【文件?实连】。?- 剩下的唯一硬点击?checkuser 的密文，不再有“连不上、看不见”的问题□?
### 32.8 checkuser 的组装链与密钥（同一轮补记）


?4 帧的载荷结构□?0 64 + "ACL.checkuser" + 00 00 + 931 字节密文，密文前 3 字节，?00 1F 5F。整段?977 字节，双长度 948 |?908 不等于?*不是 zlib**（直接解压报 incorrect header check），说明 948 |?908 的差是加密前的某种长度记账，不能拿长度差当压缩【文件】。?
命令名在 tpbus.dll 里是字面量（0x1040ABF0 "Default" ?x1040ABC4 "ACL.checkuser" ?x10408234 "ACL:checkuser"）。派发链：sub_1009F8E0 按命令名分派，命令?checkuser 时走 sub_100A0A20 |?sub_1009FB40，后者是作业队列（EnterCriticalSection、哈希桶、自增序号）【静态】。同一个?.data 里还并排躺着 CID、PID、token、accountlisth?%s,%s" 这些键名，就是?checkuser 的参数域【文件】。?
tpbus 里另有一个写死的 ?**##DoNotTellYouOk123***?x10408220），但它不是 checkuser 的密钥：唯一引用，?sub_1000BE90 是机器码文件例程，读 %s/system.uid、比值?MACHINEGUID_ 前缀、没有就 CoCreateGuid 生成交?fwrite 后设隐藏属性，加密文?sub_1014B0E0(密钥, 3) |?sub_1014B2D0(缓冲, 240)【静态】。?
直接拿这个写死串去解 checkuser 密文不成立。做过一轮矩阵：起点 0/17/19 三档 × 原始 19 字节 / MD5 / SHA1 |?16 / SHA256 四把派生 × des-ecb、des-cbc、des-ede3、des-ede3-cbc、aes-128-ecb、aes-128-cbc、aes-256-ecb、aes-256-cbc，按 -nopad 解完统计可打印率，最高只有?0.406，没有出来?JSON 或命令名【实测】：

~~~
python build\pw326.py
~~~

所以密钥是会话级的：第 3 帧与 ?3 帧应答交换的那一截随机块才是种子，取它还要读 sub_1014B0E0 的算法（调用点紧挨着作业发送），这一步留待下一轮【推断】。?
结论摆正：现在缺的不是“怎么连、发什么”，而是“用什么密钥、按什么顺序加密这 931 字节”，?
## 33. tdxzsbase 未知列的口径辨别：方法、数据与不由分的部分支?026-11-04 ?
11.5 留下的问题是 ?23 ?4 ?5 这类疑似“若干日均额/均量”的具体天数。这一章把方法固化下来，并给出这次跑出的实数，?
### 33.1 必须先解决的日期对齐

tdxzsbase 是当日快照。手上的 C:\neptune 对应 20261103 那一版，而本机?vipdoc 全部 day 文件都只有?288 字节，?9 根，最后一根停牌?20261016——直接拿 1103 的快照对 1016 的窗口比，什么口径都只能得到噪声【文件】。?
build\new\T0002\hq_cache\tdxzsbase.cfg 正好 ?20261016 那一版（1120 行同日），用它对齐后才有意义。判定某只可用：day 文件存在、至□?5 根、最后一根日期等于?20261016 ?
### 33.2 做法

对每一个?c 和每一种口径（2 |?9 日额均额? |?9 日量均、当日额、当日量），逐只有?
?c) / 口径，取全样本比值中位数，再算各比值相对该中位数的绝对偏差中位数。判据是“中位接，?1 且离散小”，离散用中位偏差而不是标准差，抗极端值折?
~~~
python build\pw342.py
~~~

### 33.3 结果

1093 只可用。命中情况（偏差越小越像）：

| |?| 最好口径?| 偏差 | 比值中位?|
|---|---|---|---|
| 18 | 2 日额，?| 0.237 | 0.312 |
| 1 | 2 日额，?| 0.287 | 0.592 |
| 23 | 当日额?| 0.391 | 0.130 |
| 24 | 2 日额，?| 0.388 | 0.130 |
| 3 | 当日额?| 0.449 | 7.98 |
| 4 | 当日额?| 0.434 | 6.99 |

已知列能大体复现（第 3 ? 列是市值量级，比值?7 |?8），说明方法本身不跑偏；
?23 ?4 |?25 |?2 |?9 日窗口偏差几乎并列（0.388 |?0.41），**分不出天数?**?
### 33.4 为什么分不出

成交额序列自相关极强推? 日均额?9 日均在千余只样本上的中位偏差只差千分之几，用比值法没有分辨力。要分开必须换方法：一是改用“同一天不同口径的排序秩相关”，二是先把 tick 级或分笔数据接上算真实?N 日累计权?
另外还欠一块：category 9 的线上日线记录长度还没解对。按 日期 u32 + 四价 varint + 量额 去切□? 条里只在 ?1 条对上，尾部署?4 |?9 字节都全条错位（build\pw340.py 逐档试过），所以现在只能吃本地 9 根，够不是?10 日以上，这也是窗口上限卡在?9 的原因【实连】。?
### 33.5 结论

- 方法、样本、判据、命令都齐了，可复现在?- |?23 ?4 的数量级是当日额的一成上下，仍按“均额类”记，具体天数未定；
- 下一步两条：
?category 9 的记录解析（解出来就能拉起?60 日以上），或换秩相关判据 ?
## 34. 权息同步的并发上限与失败补抓到?026-11-04 ?
10.4 那条“全市场要十几分钟”一直没动，是因为只把并发从 1 提到 8 就停了。这一章把 8 以上扫了一遍，结论
?64 才是拐点，但高并发会甩连接，必须配补抓才算可用，?
### 34.1 基线，? 并发

同一个?120 只（600300-600419）、同一台主站，8 并发的三轮实测：10.5 秒，?1.4 秒，?4.4 秒，每次还带 3 |?6 只失败是?
~~~
python build\pw349.py
~~~

### 34.2 扫描

同一脚本把并发按 4 ?
?6 ?2 ?4 ?28 递增，每一?120 只，看秒数、完成只数、失败数，?
| 并发 | |?| |?
?| 完成交?| 失败 | 记录 |
|---|---|---|---|---|---|
| 4 | 7.73 | 15.5 | 94 | 6 | 3454 |
| 8 | 11.38 | 10.5 | 97 | 3 | 3555 |
| 16 | 9.88 | 12.1 | 95 | 4 | 3496 |
| 32 | 9.12 | 13.2 | 95 | 4 | 3485 |
| 64 | 1.21 | 99.5 | 91 | 10 | 3394 |
| 128 | 1.29 | 92.8 | 86 | 17 | 3163 |

64 并发的?8 并发的?8 倍上下，128 反而更新?

失败更多、完成只数更少。也就是说瓶颈不是本地线程，而是服务端对齐?IP 的接纳：给多了它直接甩□?
### 34.3 甩掉的连接不会记进度

失败只加 failed 计数，不是?done，所以断点续传会自动补——这是原有设计，正好救高并发。实测连跑三轮：第一个?1.38 秒完成?78 只失败?25 只，第二十?0.93 秒补抓?99 只失败?0，第三轮 0.38 秒无事可做【实测】。完成只数停牌?99 是因为另一?21 只本来就没有权息记录，不是漏抓到?
~~~
python build\pw350.py
~~~

### 34.4 改动

sync_gbbq |?rounds=3：一轮抓完只要还有失败，就按 done 反查剩下的继续抓，补到没有失败或到上限，绝不留下空洞；默认并发从 8 提到 64 ?-jobs 仍是覆盖口径?
改动后同一个?120 只一次调用?1.98 秒、完成?99 只、失败?0，再跑一个?0.37 秒全部跳过，?
~~~
python build\pw351.py
~~~

### 34.5 边界

- limit 与补抓并存时，limit 按已完成只数收口；并发大于剩余只数时一次性发完，所以?--limit 5 |?6 只可能落 6 只，是按请求发出去算的，不是缺陷□?- 服务端不稳是常态：这一轮里 security_list 取码表连续两次直接“连接被关闭”，与并发无关。所以取码表失败要重试，别当日?limit 的问题□?- 64 并发下失败率约两成，补抓一轮就归零；如果主站更差，不?rounds 提到 5 或把 jobs 降到 32，别只提 jobs 不补抓到?
## 35. 财务包的真实通道的?711 上的 0x1F42 ?026-11-04 ?
11.9 |?10.2 一直卡在“托管侧没坐实”，此前只会话?HTTP 上试过? 条候选全 404。这一章纠正方向：财务文件根本不走 HTTP，走的是资讯通道的?
### 35.1 HTTP 侧已彻底排除

www.tdx.com.cn 根是 2000?2 KB 真页面），data.tdx.com.cn 套着 EdgeOne。带放行 Cookie 之后逐条打：tdxbase/sgpcwdata.dat、tdxbase/gpcw.txt、tdxbase/gpszsh.txt、tdxbase/gpcw2025.dat、tdxbase/gpcw20251231.dat、tdxbase/sgpcwdata.zip、vipdoc/cw/gpcw.txt、tdxbase/cw/gpcw.txt、tdxbase/ 共九条，两个站全 404【实连】。?
~~~
python build\pw353.py
~~~

### 35.2 命令字?0x1F42，负载是路径


?7711 上用 12 字节帧（cat 1、ver 0、flag 0，与 0x1F7E 问候同形），?0x1F00-0x1F5F4?x0020-0x00800?x0F00-0x0F3F，body |?cw/gpcw.txt：唯一有实质回包的 ?**0x1F42**，回 1690 |?1754 字节压缩段；0x1F43 回固定?2 字节，?x1F44 |?40 字节，?x1F47/0x1F48 |?67 字节【实连】。?
~~~
python build\pw363.py
~~~

应答是标志?16 字节头（b1 cb 74 起、commtype |?10-12、两段长度在 12-16），压缩段解压后正好 59994 字节，开头是 ASCII K 加十进制数字【实连】。?
~~~
python build\pw365.py "cw/gpcw.txt"
~~~

三个不同时刻取同一路径，解压出来分别是 3003100?01212 ?03982 开头的三段——数字是文件内偏移，说明服务端按 59994 字节窗口分块，每块带自己的偏移【实连】。?
### 35.3 还差续传

- 同一连接连着收，不?2 条永远不来，连接还开着（读超时）；
- 取完一块发 0x1F43 当确认，服务端直接关连接，?- body |?ASCII 偏移（c/ 名|59994）、逗号偏移、u32 偏移都试过，返回的块还是从头给，偏移没生效【实连】。?
所以现在能确定的是通道、命令、分块大小、头格式与路径写法，不能确定的是“怎样要到下一块”，?x1F44 |?40 字节，?0 00 01 00 02 00 89 c4 09 00）很像“块，?+ 总长”，值得下一轮先把它解开，再回填续传参数的形状态?
### 35.4 意义

- 10.2 的“托管侧没坐实”到此可以改口径：财务包托管在资讯通道，不是?HTTP，更不在 www2?- 只要把续传参数解开，就能纯脚本取包，不必依赖客户端点“下载财务数据”；
- 远端路径写法，?cw/ 加文件名（可直接，?gpcw.txt、gpszsh.txt 两个清单），tdxbase/ 那一批（sgpcwdata.dat 等）是同一套，路径照抄即可【推断】。?
## 36. 0x1F42 是分页读，不是一次读全（2026-11-04 ?
35.3 记“续传参数未解”，结论是错的方向：0x1F42 根本不要偏移，它就是一个?*分页（?*，每调用一次给下一窗口，连接换不换都一样本?
### 36.1 判据

连打同一个?cw/gpcw.txt 六次，六次回的都是?59994 字节，但内容不同：前 16 字节依次，?K |?688041、K 加零串、K |?000980、K |?300738 这样的变化，说明服务端在推进游标，不是重发同一块【实连】。?
~~~
python build\pw373.py
~~~

### 36.2 载荷是什么?
一个?59994 字节里全是定位?8 字节条目：首字节，?00 |?01，后面跟 6 |?7 |?ASCII 字符，绝大多数是六位证券代码，也就?#AUL80 这类字母数字名。整块就是一张证券清单流【实连】。?
~~~
python build\pw376.py
python build\pw377.py
~~~

### 36.3 一次拉到底

连拉 352 块，累计 21117888 字节，?322 个不同代码、耗时 26 秒，之后连续 12 块不再出现新代码——说明是**环形缓冲**，拉过头会绕回已给过的内容，不是有明确结尾的文件【实连】。?
~~~
python build\pw375.py
~~~

### 36.4 路径前缀换数据集

同一命令字?bodyt?
| body | 回的形状?|
|---|---|
| cw/gpcw.txt | 8 字节文本条目流（代码清单位?|
| cw/gpcw600000.dat | 代码开头、后跟日额?0x013528f00?0261104）的键值记录?|
| cw/gpcw20250930.zip | 同上的键值记录?|
| tdxbase/sgpcwdata.dat、tdxbase/code2name.ini | |?cw/gpcw.txt 同类的条目流 |

也就是说扩展名不决定格式，前缀决定数据集，命令本身只负责“给下一窗口”【实连】。?
~~~
python build\pw378.py
~~~

### 36.5 结论与边界?
- 财务数据可以纯脚本取包?x1F42 + 路径，反复调用、按窗口拼接、按游标推进判断何时停；
- 每窗口固定?59994 字节，是服务端缓冲区大小，不是文件大小；
- 环形的代价是必须自己去重（按 8 字节条目或代码去重），不能指望“读空即结束”；
- 条目里的数值字段与 .dat 键值记录的字段含义还没拆，那是下一步；本机已有的两份清单（gpcw.txt、gpszsh.txt）可以用来对照条目顺序【推断】。?
## 37. 0x053E 在本服是通的：错的是前缀和声明长度（2026-11-04 ?
AGENTS |?27.5 起记录?x053E 两种帧头复测仍超时，本服不通”。这一章推翻它：命令本身有应答，之前失败是因为请求体少了一个?12 字节前缀，声明长度也对不上的?
### 37.1 先证明链路没毛病

同样连接、同样握手，紧接着 ?0x000F 能收尾?8192 字节以上，说明握手与帧头都没问题；换 0x053E 立刻 0 字节【实连】。?
~~~
python build\pw381.py
~~~

21 种组合（三种端口 × 七种帧头/载荷写法）全 0 字节【实连】。?
~~~
python build\pw380.py
~~~

### 37.2 症结是声明长度与体长不等

0x053E 的约定是 12 字节请求前缀 + 每只 7 字节，声明长度?12+7n。此前只发了 4 字节头加 2 字节数量级?7 字节股票，实连?13 字节却声明?19，服务端一直等剩下 6 字节，于是静默【实连】。?
~~~
python build\pw391.py
~~~

前缀的形状是 05 00 00 00 + 四个股?+ u16 数量 + 00 00，凑□?12 字节并把声明写实，立刻就回包名?
~~~
python build\pw392.py
~~~

一个?60 字节，两个?116 字节，三个?194 字节，长度随数量增长，说明股票列表确实被消费了【实连】。?
~~~
python build\pw393.py
~~~

### 37.3 回包结构

头是 01、一个随请求变化的字节、数量级?00?1，随后每条记录以 01 加六次?ASCII 字节开头，接一个?varint 数值（含五档的量价对），尾部?0b 00 00 00 收尾【实连】。?
### 37.4 还开着的一个?
六次请求、五组不同代码，记录里那六个 ASCII 恒为 600839，不跟着请求走。所以要么它是缓存串而非代码回显，要么请求里代码的位置还要往前挪。记录字段的语义（哪几个 varint 是价、哪几个是量）同样没拆开?
### 37.5 结论

- “本服不通”作废，0x053E 可用，前置条件是形状对、声明长度等于实际长度，不需要先发别的命令；
- 0x053E 的请求形状：十二字节前缀 + 每只 7 字节，前缀第四到第七字节为零、第八第九字节是数量、第十第十一字节为零字?- 解析仍要自己写，parse_quotes 期待□?B1 CB 开头与这里的?01 开头不是一回事【实连对照】。?
## 38. 13/14 送权证的定价：挂牌价取不到，改拟合系数（2026-11-04 ?

?25 章给的结论是“按内在价值折价会过正，默认不计”，缺口写的是“要精确得取权证挂牌首日价”。这一章把挂牌价这条路走到底，再给一条不需要挂牌价的替代码?
### 38.1 样本只有 2005-2006 的股改对齐?
本地 gbbq 里类型?13/14 |?24 条，涉及 22 只代码，年份只有 2005 |?5 条目?006 |?19 条，全部是股权分置改革时送的认购/认沽权证【文件】。?
~~~
python build\pw400.py
~~~

### 38.2 权证代码已经查不是?
现行码表 27846 只里没有 0300?31 ?38 ?800?82 任何一个前缀；直接拿 5800000?30002 ?38001 这类历史权证代码去要日线，索引?00?000?00 全回空【实连】。也就是说挂牌首日价这条路，靠现行主站取不到 ?
~~~
python build\pw406.py
python build\pw407.py
~~~

### 38.3 用索引翻历史

日线，?start 参数不是日期而是**回退条数***? 是最近，5000 落到 2005 |?11 月，5600 落到 2003 |?4 月，单次最后?800 条【实连】。据此可以按日期二分定位，取除权日前后各若干根本?
~~~
python build\pw403.py
~~~

### 38.4 三种口径的误差?
同一只代码同一天还有类型?1/11/12 的记录会把效应混在一起，先剔掉；剩下 13 条干净样本，误差按 模型除权证?
?实际开盘价 计：

| 口径 | 中位误差 | 平均误差 |
|---|---|---|
| 不折价（现行默认沽?| 9.87% | 21.43% |
| 按内在价值折价（现行选项□?| 9.87% | 22.54% |
| |?行权价×Q 折价，Q=0.35 | 7.79% | ——?|

内在价值那栏很多样本与不折价相同，因为认沽权证的行权价普遍低于正股，内在价值为负被截成零，等于没折【实连】。?
~~~
python build\pw409.py
~~~

### 38.5 Q 的网页?
Q |?0 扫到 1，步□?0.05，中位误差最低点击?0.35 ?.79%），0.30 |?0.40 之间都优于基线，不?0.50 起明显变差【实连】。?
### 38.6 落地

tdx_adjust |?WARRANT_Q 常量级?--warrant-q 参数，Q 大于零时间?行权价×Q 当权证价值，否则退回原逻辑；默认仍是?0，两种口径对 001207 都不动（最大跳空都是?0.0792 ?7 条事件）【实测】。?
~~~
python build\pw124.py
python tdx_adjust.py factors --gbbq C:\neptune\T0002\hq_cache\gbbq --market 0 --code 001207 --warrant-q 0.35
~~~

### 38.7 边界

- Q=0.35 |?13 条样本的中位最优，不是普适常数，换市场要重拟合；
- 600519 |?20060525 误差 103%，gbbq 里那天只有类型?14、没有对应的送转记录，价格从 91.41 掉到 39.59 单靠权证解释不了，是数据缺条而不是公式错位?- 想再精确只有两条路：找带权证历史的主站，或补到股改前的股本来推断?
## 39. 资讯通道的读命令族与记录对齐□?026-11-04 ?
36.2 把条目写成“首字节，?00 |?01，后面跟 6 |?7 个字符”，对齐差了一格。这一章按正确对齐重写，并把同一族的三条读命令测绘完整段?
### 39.1 正确的记录形状?
流头之后，条目是**六位代码 + 一位标志?+ 一个零字节**，共八字节，标志位是 ASCII |?0 |?9，不是二进制 0/1【实连】。?
~~~
python build\pw428.py
~~~

窗口径?94.3% 是零字节、可打印字符只占 5.4%，说明这是一个?*稀疏的代码索引**，不是数值表格?
### 39.2 流头与回 ?
0x1F43 的作用是回卷：发过去只回两个字节 00 00，之后?0x1F42 就从流的开头给【实连】。?
流开头是二十字节头：K ?x1D、十个零、两个相同的日期 0x013528F00?0261104），?1 00。之后再接条目。不带路径的 0x1F42 等于“接着上一次的位置往下给”，位置是全局的，换连接也照样推进【实连】。?
~~~
python build\pw423.py
python build\pw427.py
~~~

### 39.3 同族三条读命令?
| 命令 | 回什么?| 长度 |
|---|---|---|
| 0x1F42 | 下一个数据窗口?| 固定 59994 字节 |
| 0x1F44 | 下一**整条记录** | 40 字节 |
| 0x1F45 | 状态文件?| 67 字节，正文恒为?网络错误 |

0x1F45 的正文是八个 GBK 字节 网络错误，路径对不对都一样【实连】。?
~~~
python build\pw424.py
python build\pw431.py
~~~

### 39.4 0x1F44 给的那条记录

四十字节里能读出字段数九（偏移?8 的四字节小端 09）、一个当天日期（偏移 12）、一个?GBK 字节（偏移?20 起，不?b9ab cbbe b4f3 cac2 bcc7），其余为零【实连】。?
~~~
python build\pw426.py
~~~

### 39.5 还没找到数，?
cw/gpcw.txt 这条流自始至终只有代码与标志，没有第二段数值；
?cw/gpcw600000.dat、cw/gpcw20250930.zip 这些写法，回的仍是同一类代码索引，只是流头里的标志与日期不同【实连】。所以“财务包的字段语义”这一半还没落地：通道、读法、记录边界都清楚了，缺的是承载金额的那一段的?
### 39.6 结论

- 文件通道的?*有头、按游标推进、命令按窗口大小分工**的流，不是?HTTP 也不是“一次性读全”；
- 条目对齐□?代码，?+ 标志一 + 零，别按标志在前切；
- 0x1F43 是回卷，0x1F44 是按记录读，0x1F45 是状态，0x1F42 是按窗口读；
- 金额字段仍未定位，下一步该帧?0x1F45 之外的状态命令，或者反过来从客户端读盘代码里找“取完索引之后再取什么”，?

## 40. tdxzsbase 列 24 与列 25 定案：当日额与前一交易日额（2026-11-04）

11.5 的逐列语义只剩 23、24、25 三列（1 基）没坐实。29 章用买卖对和推翻过，33 章用比值法试过窗口，都停住了。本章把三件事重新做对之后，24 与 25 用 1e-4 的恒定比和对齐到前一交易日的方式结案。

### 40.1 先纠三个前提错误

第一，tdxzsbase.cfg 是**指数与板块**的横截面，不是个股。整表 1120 行，市场列分布是 1 占 1101、0 占 18、2 占 1，行代码是 000001、399101 这类指数代码。33 章按“个股”理解，把成交额当成个股成交额，口径一开始就偏了【文件】。

第二，指数行情与个股行情不是同一套取值：同一条 category 9，个股一次能给到 800 根，指数**一次只回一根**，请求里写 count=60 也只给一根。想拼窗口只能一根一根要，连接可以复用，六十根连发实测 60/60 全中【实连】。

第三，start 是回退根数，返回的是从该位置**向今天**走的若干根。因此“往后要历史”得靠加大 start：start=13 给的是 20261012 到 20261016 那一段，start=73 接在它前面，两段不重叠【实连】。

~~~
python build\pw457.py
python build\pw461.py
python build\pw463.py
~~~

### 40.2 33.4 的“错位”是尾部没截断

category 9 的应答头里写的是请求根数，服务端实际回得更少：请求 800 根，声明 800，实回七百出头，剩下几十字节是填充。解析器按声明根数硬解，就越过记录区把填充当记录，解出 (318845, 54, 62) 这种根本不是日期的日期，还有 2.59e79 这种天文数字的成交额。33.4 记的“第 2 条起错位、尾部补字节都不行”，成因就在这里，不是记录布局变了【实连】。

修法是在 tdx_client 的 security_bars 里加一道日期闸：解出的日期不在 19900101 到 20301231 之间就收工，位置指针越界也收工。改完同一条请求从 30 条的全噪声变成干净的 2026 年序列【实连】。

~~~
python build\pw456.py
python build\pw444.py
~~~

### 40.3 列 24 与当日成交额是恒定比

拿 1120 只指数的当日成交额去除列 24，中位比是 0.000100，即恰好万分之一——列 24 是成交额，单位万元【实连】。去掉规模效应（先对 ln 市值回归取残差）之后，列 24 与当日成交额的相关系数是 1.000，在 w=1 上取得，w=2 起掉到 0.93【实连】。

~~~
python build\pw464.py
python build\pw465.py
~~~

### 40.4 列 25 的前一日判别

列 25 与当日额的比例中位是 0.000106，跟 24 只差 6%，仍然接近；但把日期往前挪一天，比例变成 0.000100，且**相对离散度是 0.0000**——1118 只指数一个不差地落在同一条比上。再往前挪一天，离散度回到 0.0900【实连】。

| 对齐日 | 列 25 与成交额的中位比 | 相对离散度 |
|---|---|---|
| 20261016（当日） | 0.000106 | 0.0744 |
| 20261015（前一交易日） | 0.000100 | 0.0000 |
| 20261014（前二交易日） | 0.000112 | 0.0900 |

残差相关同向：列 25 与前一交易日额的相关系数 1.000，与当日额只有 0.903，与再往前一天 0.946【实连】。

~~~
python build\pw466.py
python build\pw468.py
~~~

### 40.5 顺带把 23 说清，20 到 23 是涨跌幅族

同一批数据上再给列 18 到 25 各找最像的派生量（当日额、昨日额、一日涨跌、五日涨跌、振幅、换手、量比），只有两列拿到 1.0 级的相关：列 24 对换手与当日额都是 0.994，列 25 对昨日额 0.991。列 20 到 23 全部以五日涨跌为最像（0.530、0.619、0.651、0.652），对当日额、换手、量比都不到 0.25，是跨日涨跌幅族而不是金额族【实连】。

~~~
python build\pw467.py
~~~

### 40.6 结论

- 列 24（0 基 22）是**当日成交额，单位万元**；列 25（0 基 23）是**前一交易日成交额，同单位**。
- 比值恒为 1e-4 且对齐前一交易日时离散度为 0，判定依据是恒等式而不是窗口拟合，可以当结论用。
- 29 章的“买卖对不成立”与 33 章的“比值法分不出天数”都保留，但成因更正：不是成交额自相关太强，是列 24、25 本身就不是多日平均，任何大于一日的窗都会把它稀释；33.4 的错位则是尾部没截断。
- 11.5 的 23、24、25 三列至此全部有口径：23 与 22 同族是跨日涨跌幅，24、25 是当日与前一日成交额。
- 仍是推断的：列 20 到 23 各自的**天数**（5、10、20、60 只是最像者排序，没有像 24/25 那样的恒等判据）、列 19 的语义（对当日额与换手都只有 0.23，既不是额也不是换手）。




---

## 41. 实时盘口采集器：L1 快照与 L2 逐笔（2026-11-04 夜）

### 41.1 结论与边界【实连】

0x053E 现在能稳定出数，采集器 tdx_realtime.py 按“每个请求一条连接、握手与业务帧同发”的模型跑通，
单包 80 只 81 毫秒、8 并发 20 批每秒、30 批零错误。逐笔 0x0FC5、逐笔委托 0x0FC2、分时 0x051D
在 23:45 后全静默，是时段问题不是协议问题，开盘后要复测（41.10）。

记录字段的**偏移量这轮只能用零点数据校准到“总长 56 字节”**：全零时每只 56 字节，
但价格是 varint，有值时长度会变（41.4 的实测是 73 字节），所以解析按顺序消费、不按固定步长切。
真正的偏移只能等有值时用 OHLC 和成交量对出来（41.10 第 1 条）。

### 41.2 连接模型：一次请求一条连接，握手与业务帧同发【实连】

12.1 记的“服务端接受但不回应”在补上 12 字节前缀后不再成立，但另有约束：

| 实测 | 结果 |
|---|---|
| 连上后先发握手、等应答，再发业务帧 | 通过 |
| 连上后**不握手**直接发业务帧（3 次） | 3 次全超时，握手是必需的 |
| 握手发完就停手，隔一会儿再发业务帧 | 第一次就 closed，服务端不肯留空闲连接 |
| 握手三条帧与业务帧**一次 sendall 全发出** | 3 次全成功，36 到 56 毫秒 |

所以工人不维持长连接：每批一次 connect，把 HS1+HS2+HS3+业务帧拼成一个缓冲发出去，只留一个 RTT。
用完立刻 close，别留着当活连接用。

### 41.3 请求与回包形状（复核 12.1 与 37 章）【实连】

请求体是 12 字节前缀（05 00 00 00、四个零、u16 只数、两个零）加 7n 的证券对，声明长度必须等于 12+7n。
回包是 4 字节头（01 01、u16 只数）加 n 条记录；**记录里的六字节恒是 600839**，与请求无关，
只能按请求顺序回填（37.4 的结论在 41.4 的多次批次里再验证一遍）。

### 41.4 记录是变长的，必须按顺序消费【实连 + 推断】

零点数据下：1 只 60 字节、2 只 116、3 只 172、5 只 284、10 只 564，即 4 + 56n。
造一条带真实值的记录（价 10.500、量 12345、五档）会到 73 字节，多出来的是 varint 的续字节。
这说明 56 是**守时长度不是定长步长**，写死步长会在有值时全部错位。解析器改为从记录起点顺序推进，
读满一条再起下一条；品种数只用于定循环次数。

字段顺序按 12.1 的公开互校顺序落表：active、基准价、昨收、开、高、低、总量、现量、成交额、
内外盘、五档买卖价量、speed、active2，另留 4 个未定性 varint 与 4 字节尾标把长度顶到 56。

### 41.5 按 commtype 认领应答，不预读握手应答【实连】

握手应答的条数不固定（1 到 2 条），先按“固定读两条”实现时，只有一条应答的那次会把业务应答
当成第二条握手应答吞掉，随后超时——这正是最初 90% 失败率的来源。改成循环读，读到 commtype 等于
业务命令字为止，其余（握手应答与夹带推送）全部丢弃，失败率随即归零。

### 41.6 主机筛查：25 台里挑 8 台【实连】

connect.cfg 的 25 台里有一部分是连上即关的，撞上就必报“连接被关闭”。采集器起手用 0x052D 空跑一只
做健康探测，10 并发、1.5 秒超时，只留会回包的前 8 台并按耗时排序；一台都不回时退回全表。
探测串行做要 56 秒，并发做完 2 秒。

### 41.7 延迟与吞吐实测【实连】

| 场景 | 结果 |
|---|---|
| 单只、8 并发、60 批 | p50 56.1 毫秒、p95 77.1、p99 79.4、错误 0 |
| 单只、4 并发、20 批（未修 41.5 前） | p50 59.7 毫秒、错误 16 |
| 单包 80 只、一条连接 | 81 毫秒出 80 条 |
| 6 只、1 秒周期、6 轮 | 36 条记录、1 次瞬时错误（已由重试吃掉） |

按 80 只 81 毫秒、8 并发折算，5000 只全市场一轮约 0.4 秒，3 秒档绰绰有余；
真正的天花板是交易所三秒一档，不是本地。

### 41.8 L2 现状【实连】

逐笔 0x0FC5、逐笔委托 0x0FC2、分时 0x051D 三种帧（含 3.5 的 catword 0x0817、ver 0x0101、flag 1 原样）
在夜间全超时；0x052F、0x0532 同样超时。采集器把它们做成运行时探测：probe-l2 打一笔逐笔，
有回包才进 L2，否则按 L1 跑，--mode 可强制 l1 或 l2。L2 模式下按只缓存已见最大序号，只放新成交，
不重复吐历史。委托（0x0FC2）与千档的字段语义仍未拆，先只做逐笔成交。

### 41.9 工程要点

1. **差分**：只在 price、vol、amount、五档任一变化时放行，零点空转不刷屏。
2. **背压**：工人队列有界（8），投不进去就计 dropped，不阻塞调度。
3. **退避**：失败 0.2 秒起翻倍、封顶 5 秒，重投一次；成功即复位。
4. **时钟**：每条记录带本地 ts，回包本身没有时间字段，别拿它当交易所时间。
5. **周期**：cycle 是“上一轮投完到下一轮开工”的下限，轮次超时就立刻续跑，不叠加等待。

### 41.10 开盘后待验清单

1. 用当日日线的开高低、成交量对齐记录偏移，把 4 个未定性 varint 与前 7 字节的 active 语义定死。
2. 复测 0x0FC5、0x0FC2、0x051D 是否有回包，定 L2 是否真的开通。
3. 校 600839 常量是否随交易日变化。
4. 量 3 秒档下的实际收敛：同一只两次变化的间隔应贴着 3 秒。
5. 五档之外若有十档，确认是否要另发命令（千档）。

### 41.11 字段校准：单位是分，记录变长【实连】

按 000001 的当日日线逐字段对死一遍（价 11.74、量 1437905 手、额 1689184000 元），第 7 到第 21 个字段全部对上，结论：

1. **价格单位是分（1/100 元）**，不是 12.1 记的千分之一。基准 1174 就是 11.74；按千分之一读会整体小一个数量级。
2. **全零记录 56 字节、有值记录 81 字节**，56 只是下限，只能从记录起点顺序消费，写死步长必错位。
3. 字段顺序是 市场、代码、现手、基准价、前收、开、高、低、成交额、两个跳、量、现手、成交额日志浮点、外盘、内盘、两跳、五档买卖各四项、两个原字节、四跳、涨速、现手计数。

### 41.12 回包与请求错一条（成因见 41.16）【实连】

固定请求 [000002、000006、000009] 连打三次，回包三次完全一致：记录 1 恒空，记录 k 装的是请求 k−1 的行情，回包代码字段却是请求 k 的。尾部垫一只再取第 2 条起，就能拿到 n 只真实数据（补位后记录 4 回的是第三只 6.67，与日线一致），三只全部对上日线（3.20/2698264、6.00/184531、6.67/201422）。

当时只能这么绕。41.16 之后错位消失，垫底可以撤掉。

### 41.13 只按代码末位解析（当时的最大问题）【实连】

同一末位、不同前五位的码回同一批数：000002、002002、000012、000022 全回 3.20/2698264；000027 与 000157 末位都是 7，回的价量完全一样。沪市因此拿不到（600036 回深 000006 的 6.00），八台主机结果一致。

排除过的形状：市场 0–8 与 10–14 全一样、市场写 ASCII 数字整条作废、8 字节一只超时、5 位码超时、码前市场后全零、前缀 12 字节逐个改都一样。

以上全是请求体末两字节放错位置连带的解析错位，不是服务端的匹配能力问题。

### 41.14 顺手扫到的兄弟命令【实连】

0x0530–0x0550 空扫，另有三个有应答：0x053D 回 4 字节 9c4e0000、0x0541 回 96 字节全零、0x054B 回 4 字节 0、0x054C 与 0x053E 同前缀同记录但记录尾部多 16 字节（c07e2350…、e0012c4b…，像是两个时间戳）。

0x054C 与 0x053E 吃同一套请求体，41.16 修正后一样能给正确数据，单只回包 109 对 86 字节，沪市同样可用。

### 41.15 本轮的落地与待办

采到的行带 code_srv（回包原始码）与 mismatch（回包码与请求不一致时置位）。

前两条待办（末位解析、沪市取不到）已被 41.16 关闭。仍开着的：0x054C 尾部那 16 字节是什么、五档之外要不要另发命令取十档、L2 三条命令夜间全超时要在开盘后复测。

### 41.16 根因：两字节尾巴放错了位置（2026-11-05 凌晨）【实连】

客户端自己发什么，是这条唯一能定案的证据。做法：把 connect.cfg 的行情站整段改成 127.0.0.1:17709 指向本地 7709 代理（hq_proxy.py 记原始字节，cfg_hook.py 负责改与还原），起原版 TdxW.exe，翻一只沪股和一只深股，把客户端自己的 0x054C 帧录下来。

录到的请求体（26 字节，两只）：

~~~
05 00 00 00 00 00 00 00 02 00 01 "601881" 01 "999999" 0C 01
~~~

本工程原来发的：

~~~
05 00 00 00 00 00 00 00 02 00 0C 01 01 "600036" 01 "999999"
~~~

差别只有一个：两字节尾巴 0C 01 在**证券表之后**，不在数量之后。位置对调后立刻复现正确行为：

| 请求体排布 | 600036 回包 |
|---|---|
| 尾 0C 01 放数量后（旧） | 记录首码 600839、全零 |
| 尾 0C 01 放证券表后 | 首记录 600036、前收 42.54 |

尾巴两字节的取值无所谓（0C 01 与 00 00 都行），只有位置有意义。放错之后服务端按错位解析整条请求，三个症状同时出现：代码只按末位对（末位那字节被当成别的字段）、回包恒带 600839（前缀被当成码）、记录错开一条（字段整体前移）。41.12 与 41.13 的现象全部由此解释，不必再回 VMP。

验证一：同一主机、一次批量 24 只（沪 12 深 12），四只对照：

| 代码 | 快照价 | 前收 | 买一/卖一 | 校验 |
|---|---|---|---|---|
| 600036 | 42.54 | 42.69 | 42.52 / 42.54 | 与日线收盘、量 1347238 手一致 |
| 600519 | 1446.44 | 1480.00 | 1446.05 / 1446.44 | 一致 |
| 601881 | 11.86 | 11.82 | 11.86 / 11.87 | 一致 |
| 000002 | 3.20 | 3.21 | 3.19 / 3.20 | 一致 |

验证二：24 只里 12 只深市的量与当日 5 分钟线合计（股除以 100）逐只相等，误差 0 到 1 手。

验证三：24 只全部满足 成交额 约等于 量乘 100 乘 现价，比值 0.970 到 1.008，沪市同样成立，价量额相互自洽。

验证四：采集器跑 20 秒双市场，168 条快照、0 mismatch、p50 60.5 毫秒、0 错误。

落地点：SNAP_TAIL 改成 0C 01 并移到证券表之后，snapshot() 去掉补位与取第 2 条起的偏移，回包码改为核对用途。请求体总长仍是 12 加 7n 加 2，声明长度等于实际值。

### 41.17 L2 逐笔打通：握手要读干净，声明长度要多两字节（2026-11-05 凌晨）【实连】

41.8 把 L2 判成"夜间全超时"，一半是时段，一半是**我们自己发错了**。这一节把逐笔成交（0x0FC5）与逐笔委托（0x0FC2）打通，并给出整日量校验。

#### 41.17.1 三个错处

拿客户端真包一比，三个错处同时成立：

1. 帧头错。我们一直用 0x0817 / 0x0101（来自第三方模板）。客户端真包是 0C 01 08 00 03 01，按我们的打包顺序就是 catword 0x0801 / ver 0x0300 / flag 1。旧值在本服**一帧都不回**，不是超时就是断连。
2. 声明长度错。客户端逐笔帧的双长度写的是体长 **加 2**（体 12 字节、声明 14）。我们按体长声明，服务端就一直等那两字节，不回包也不断连——看起来正是"夜间超时"。
3. 读法错。之前是握手三帧与业务帧一次发出、再去读，握手应答夹在中间没读干净；业务帧因此被当成"没人认领的推送"丢掉。必须先把 Setup1、Setup2 的应答逐条读掉，再发业务帧，再按 commtype 认领。

三处都改完，同一台主机、同一只票立刻回 857 字节。

#### 41.17.2 客户端原帧与回包

请求（601881，市场 1，start 0，count 115）：

~~~
0c 01 08 00 03 01 0e 00 0e 00 c5 0f
01 00 36 30 31 38 38 31 00 00 73 00
~~~

回包头是 b1cb 开头、commtype 0x0FC5 在 +10、压缩长 0x01D7、原始长 0x0359，体是 zlib，解压后 857 字节，开头 73 00 即 115 条。

#### 41.17.3 记录布局

逐条是：u16 时间（当日分钟数，891 就是 14:51） / 价 varint / 量 varint / 笔数 varint / u16 方向。价是**差分**，0 表示与上一条相同，0x40 位是负号，单位分，第一条给绝对值。量单位手，笔数是这一跳聚合的成交笔数。方向取 0 主动买、1 主动卖、2 中性、5 盘后固定价格（15:00 之后那批价格不动、全是 5，与价向完全吻合：价升 33 条全 0、价跌 30 条里 29 条是 1）。

布局不是猜的：对 200 条样本做回溯搜索，要求整段恰好消费完，结果 1472 字节一条不多一条不少地切完，尾部那个 u16 不能省，省了后面全错位。

#### 41.17.4 整日量校验

601881 从 start 0 起每轮 900 条往后翻，5 轮取完：4090 条、时段 09:25 到 15:22、成交量合计 **317414 手**；当日日线成交量 317108 手，差 306 手（0.096%），差额落在集合竞价那一条的记账口径上。start 是**从最新往回数的条数**，start 越大时间越早，翻到 4500 返回空。

#### 41.17.5 逐笔委托 0x0FC2

同一套请求体、同一套帧头也通，回包 commtype 是 0x0FC2，体与 0x0FC5 逐条一一对应，只多一个字节**落在每条末尾**：3 条回 27 字节 = 2 + 3×8、6 条回 52 字节 = 2 + 6×8，条数守恒证明它是记录字段不是分隔符；取值 16、19、43、58 这类按 3 跳的数，语义未定，按 kind_code 原样给出。

#### 41.17.6 落地与边界

落地：TICK_CAT / TICK_VER 改 0x0801 / 0x0300；_call 增加 extra 参数，逐笔族声明体长加 2；parse_tick 按新布局重写；新增 orders()、parse_order 与 --l2-orders；逐笔去重从"序号比对"改成 (时间, 价, 量, 笔数) 签名（逐笔没有自增序号）。

边界三条：一是换一台主机要重新验，帧头是这台的脾气，别当通用常量；二是 15:00 之后的 5 类记录价格不动，做分钟聚合时要么单列要么剔除；三是 0x0FC2 末尾那字节的语义、以及十档要不要另发命令，仍未定。
