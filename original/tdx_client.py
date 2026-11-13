#!/usr/bin/env python3
# -*- coding: utf-8 -*-
r"""通达信 7709 行情客户端（协议依据：TDX_接口逆向分析报告.md 第 3 章）

只用标准库。默认从安装目录的 connect.cfg 主机表选优连接（TDX_CFG 可覆盖）。

- 帧头 10 字节：0x0C | 类别(u16) | 子版本(u16) | 1 | len(u16) | len(u16) | 载荷
- 载荷首字段是 commtype(u16)
- 响应：16 字节头 <IIIHH>，末两字段为 zipsize/unzipsize，不等则 zlib 解压
- 实测该服务端每连接只服务一条业务请求，故失败即自动重连重试

用法：
    python tdx_client.py smoke
    python tdx_client.py count    --market 1
    python tdx_client.py codes    --market 1 --start 0 --count 100
    python tdx_client.py bars     --market 1 --code 600000 --category 9 --count 100 --out x.csv
    python tdx_client.py minute   --market 1 --code 600000
    python tdx_client.py tick     --market 1 --code 600000 --start 0 --count 50
    python tdx_client.py local    --market 1 --code 600000 --out x.csv
    python tdx_client.py info-hello           # 资讯口问候帧（INFOHOST，不吃登录态）
"""
import argparse, collections, configparser, csv, json, os, re, socket, struct, sys, threading, time, zlib

BASE_DIR = os.path.dirname(os.path.abspath(__file__))
# 安装目录用相对路径推导，TDX_ROOT 或 TDX_CFG 可覆盖
NP_DEFAULT = os.path.abspath(os.environ.get("TDX_ROOT") or BASE_DIR)
CFG_DEFAULT = os.environ.get("TDX_CFG") or os.path.join(NP_DEFAULT, "connect.cfg")
VIPDOC_DEFAULT = os.path.join(NP_DEFAULT, "vipdoc")

MARKET_DIR = {0: "sz", 1: "sh", 2: "bj"}
PREFIX = {0: "sz", 1: "sh", 2: "bj"}

CATEGORY = {
    0: "5m", 1: "15m", 2: "30m", 3: "60m", 4: "day", 5: "week",
    6: "month", 7: "1m", 8: "1m", 9: "day", 10: "quarter", 11: "year", 12: "xrday",
}

CT_BARS, CT_MINUTE, CT_TICK = 0x052D, 0x051D, 0x0FC5
CT_SNAPSHOT, CT_ORDER = 0x053E, 0x0FC2        # L1 快照 / 带秒逐笔
SNAP_CAT, SNAP_VER, SNAP_FLAG = 0x0218, 0x0093, 0   # 快照帧头（37 章实测）
TICK_CAT, TICK_VER, TICK_FLAG = 0x0801, 0x0300, 1   # 逐笔帧头（客户端真包同值）
SNAP_PREFIX = b"\x05\x00\x00\x00" + b"\x00\x00\x00\x00"
SNAP_TAIL = b"\x0c\x01"      # 必须排证券表之后，放数量后面服务端会错位解析
SNAP_REC = 56
HS1 = bytes.fromhex("0c0218930001030003000d0001")
HS2 = bytes.fromhex("0c0218940001030003000d0002")
HS3 = bytes.fromhex("0c0318990001200020000fdbd5d0c9ccd6a4a8af0000008fc22540130000d500c9ccbdf0d7ea00000002")
CAT_BARS, CAT_MINUTE, CAT_TICK = 0x01, 0x1B, 0x17
VER_BARS, VER_MINUTE, VER_TICK = 0x0164, 0x0100, 0x0101


# 文件读法：常量 → 编码函数（价格/成交量/日期）→ 帧与连接类 → 本地读取 → CLI。
# 改协议常量前先看第 3 章的表，CAT_ 与 VER_ 与 CT_ 是三组必须成对改的东西。

# 文件读法：常量 → 编码函数（价格/成交量/日期）→ 帧与连接类 → 本地读取 → CLI。
# 改协议常量前先看第 3 章的表，CAT_ 与 VER_ 与 CT_ 是三组必须成对改的东西。
MAX_INFLIGHT = 5


## 价格编码（服务端所有价格字段都走这里）
# 变长整数：首字节低 6 位是最低位，bit6 是符号，bit7 是续位标记；
# 后续字节每多一个左移 7 位。返回值已乘 1000，调用方再决定是否 ÷100。
def get_price(buf, pos):
    """可变长带符号整数：bit6 符号位，bit7 续位，低 6 位起算，7bit 步进。"""
    n = buf[pos]
    val = n & 0x3F
    neg = bool(n & 0x40)
    # 续位为 1 就继续读，位移每次加 7；越界由外层保证不越界。
    shift = 6
    while n & 0x80:
        pos += 1
        n = buf[pos]
        val += (n & 0x7F) << shift
        shift += 7
    pos += 1
    return (-val if neg else val), pos


## 成交量编码
# 4 字节对数浮点：高字节是指数（带 -0x7F 偏置），低三字节拆成三段尾数，
# 按 2 的幂逐段加权后相加。顺序与还原出的汇编一一对应，别随手重排。
def get_volume(raw):
    """4 字节对数浮点 -> double（快照行情成交量用）。"""
    logpoint = raw >> 24
    hheax, hleax, lheax, lleax = (raw >> 24) & 0xFF, (raw >> 16) & 0xFF, (raw >> 8) & 0xFF, raw & 0xFF
    dwEcx, dwEdx, dwEsi, dwEax = logpoint * 2 - 0x7F, logpoint * 2 - 0x86, logpoint * 2 - 0x8E, logpoint * 2 - 0x96
    tmp = -dwEcx if dwEcx < 0 else dwEcx
    base = 2.0 ** tmp
    if dwEcx < 0:
        base = 1.0 / base
    if hleax > 0x80:
        v4 = (2.0 ** (dwEdx + 1)) * (hleax & 0x7F) + (2.0 ** dwEdx) * 128.0
    else:
        v4 = (2.0 ** dwEdx) * hleax if dwEdx >= 0 else (1.0 / (2.0 ** dwEdx)) * hleax
    v3 = (2.0 ** dwEsi) * lheax
    v1 = (2.0 ** dwEax) * lleax
    if hleax & 0x80:
        v3 *= 2.0
        v1 *= 2.0
    return base + v4 + v3 + v1


def parse_quotes(body):
    """解析 0x053E 快照应答：'B1 CB' + u16 n + 每只 <B6sH> 基准价，再按差分还原五档。"""
    # 应答以 'B1 CB' 两字节开头，随后是 u16 只数；每只先给基准价，其余字段都是相对基准的差分。
    pos = 2
    num = struct.unpack_from("<H", body, pos)[0]
    pos += 2
    out = []
    for _ in range(num):
        market, code, _active1 = struct.unpack_from("<B6sH", body, pos)
        pos += 9
        base, pos = get_price(body, pos)
        rec = {"market": market, "code": code.decode("ascii", "replace")}
        last_close, pos = get_price(body, pos)
        rec["last_close"] = (base + last_close) / 100.0
        # 开高低是相对基准价的差分；加上基准再除 1000 才是真实价。
        for name in ("open", "high", "low"):
            diff, pos = get_price(body, pos)
            rec[name] = (base + diff) / 100.0
        for _skip in range(3):
            _, pos = get_price(body, pos)
        rec["vol"], pos = get_price(body, pos)
        rec["cur_vol"], pos = get_price(body, pos)
        rec["amount"] = get_volume(struct.unpack_from("<I", body, pos)[0])
        pos += 4
        rec["s_vol"], pos = get_price(body, pos)
        rec["b_vol"], pos = get_price(body, pos)
        for _skip in range(2):
            _, pos = get_price(body, pos)
        # 五档：买价、卖价、买量、卖量成对出现，同样是差分价。
        for lvl in range(1, 6):
            bid, pos = get_price(body, pos)
            ask, pos = get_price(body, pos)
            bv, pos = get_price(body, pos)
            av, pos = get_price(body, pos)
            rec["bid%d" % lvl] = (base + bid) / 100.0
            rec["ask%d" % lvl] = (base + ask) / 100.0
            rec["bid_vol%d" % lvl] = bv
            rec["ask_vol%d" % lvl] = av
        pos += 2
        for _skip in range(4):
            _, pos = get_price(body, pos)
        # 末尾两字节是有符号涨速（百分之一）与活跃度标志。
        _, speed_raw, active = struct.unpack_from("<hH", body, pos)
        rec["speed"] = speed_raw / 100.0
        rec["active2"] = active
        rec["price"] = base / 100.0
        out.append(rec)
    return out


def unpack_date(category, buf, pos):
    """日线及以上只有 u32 YYYYMMDD；分钟级是 u16 打包日 + u16 分钟数。"""
    # 分钟级：u16 打包日（bit11 起是年，减 2004；低 11 位是月日）加 u16 分钟数。
    if category < 4 or category in (7, 8):
        zipday, minutes = struct.unpack_from("<HH", buf, pos)
        y = (zipday >> 11) + 2004
        mo = (zipday % 2048) // 100
        d = (zipday % 2048) % 100
        hh, mm = divmod(minutes, 60)
        return (y, mo, d, hh, mm), pos + 4
    ymd = struct.unpack_from("<I", buf, pos)[0]
    return (ymd // 10000, (ymd % 10000) // 100, ymd % 100, 0, 0), pos + 4


class TdxError(RuntimeError):
    pass


def parse_ticks(body, scale=100.0):
    """解析 0x0FC5 逐笔：u16 条数，随后每条 分钟 u16 + 价差分 varint + 量 varint + 笔数 varint + 方向 u16。"""
    if len(body) < 2:
        return []
    num = struct.unpack_from("<H", body, 0)[0]
    pos, px, out = 2, 0, []
    for _ in range(num):
        if pos + 2 > len(body):
            break
        tmin = struct.unpack_from("<H", body, pos)[0]
        dp, pos = get_price(body, pos + 2)
        vol, pos = get_price(body, pos)
        cnt, pos = get_price(body, pos)
        side = struct.unpack_from("<H", body, pos)[0] if pos + 2 <= len(body) else 2
        pos += 2
        px += dp
        out.append({"t": "%02d:%02d" % (tmin // 60, tmin % 60), "price": px / scale,
                    "vol": vol, "trades": cnt, "side": side})
    return out


# ---------------------------------------------------------------- 行情记录解码

# ---------------------------------------------------------------- 价格与成交量解码

def read_varint(buf, pos):
    """带符号可变长整数，第一字节低 6 位起算：0x40 是符号位、0x80 是续位，随后每字节 7 位。

    与 tdx_client.get_price 同一套编码（首字节只给 6 位），别按常见的 7 位 LEB128 实现。
    """
    n = buf[pos]
    val = n & 0x3F
    neg = bool(n & 0x40)
    shift = 6
    while n & 0x80:
        pos += 1
        n = buf[pos]
        val += (n & 0x7F) << shift
        shift += 7
    pos += 1
    return (-val if neg else val), pos


def read_float4(buf, pos):
    """4 字节对数浮点（成交量族）：与 K 线同一套编码。"""
    raw = struct.unpack_from("<I", buf, pos)[0]
    if raw == 0:                                          # 零点数据别解成 5.9e-39
        return 0.0, pos + 4
    return get_volume(raw), pos + 4


# ---------------------------------------------------------------- 价格精度

# 053E 的价是 价格×10^小数位 的整数，股票 2 位、ETF 3 位、转债 4 位都一样；
# 固定按分（1/100）解会把 ETF 放大 10 倍、转债放大 100 倍。小数位在码表记录偏移 20。
# 052D 的 K 线反过来一律是 1/1000，不分证券，所以那边不用这张表。
_PREC = {}
_PREC_READY = set()
_PREC_LOCK = threading.Lock()


def price_scale(market, code):
    """该证券的价格分母（10 的 小数位 次方）；查不到按 100 兜底。"""
    dp = _PREC.get((market, code))
    if dp is None:
        dp = 2
    return float(10 ** dp)


def tick_scale(market, code):
    """逐笔的价格分母：股票 100，ETF 与转债 1000（实测转债逐笔比快照少一个 0）。

    K 线又是另一套，一律 1/1000，不分证券；三个口径互不相同，改哪边都要重新对一次价。
    """
    dp = _PREC.get((market, code), 2)
    return float(10 ** max(2, min(3, dp)))


PREC_CACHE = os.path.join(BASE_DIR, "db", "precision.json")
PREC_TTL = 7 * 86400        # 小数位基本不变，缓存一周，免得每次启动都翻 28 页码表


def _prec_cache_load():
    """从磁盘缓存恢复整张小数位表，过期或缺失就返回空。"""
    try:
        if time.time() - os.path.getmtime(PREC_CACHE) > PREC_TTL:
            return {}
        with open(PREC_CACHE, "r", encoding="utf-8") as fh:
            raw = json.load(fh)
    except (OSError, ValueError):
        return {}
    with _PREC_LOCK:
        for key, dp in raw.items():
            mkt, code = key.split(":", 1)
            _PREC[(int(mkt), code)] = int(dp)
    return raw


def _prec_cache_save():
    """把内存里的表写回磁盘；失败了不影响本次运行。"""
    os.makedirs(os.path.dirname(PREC_CACHE), exist_ok=True)
    with _PREC_LOCK:
        raw = {"%d:%s" % k: v for k, v in _PREC.items()}
    try:
        with open(PREC_CACHE, "w", encoding="utf-8") as fh:
            json.dump(raw, fh, ensure_ascii=False)
    except OSError:
        pass


def load_precision(call, market):
    """把某个市场的 代码 -> 小数位 读进缓存，一次一页 1000 条。call(market, start) 是取一页的函数。

    先看磁盘缓存：命中就直接返回，不再发请求；没命中才翻码表，翻完写回。
    """
    cached = _prec_cache_load()
    if any(k.startswith("%d:" % market) for k in cached):
        return len(cached)
    start = 0
    while start < 40000:
        try:
            body = call(market, start)
        except Exception as exc:                       # noqa: BLE001
            print("码表第 %d 页取不到（%r），小数位按 2 兜底" % (start, exc)[:120], file=sys.stderr)
            break
        num = struct.unpack_from("<H", body, 0)[0]
        if not num:
            break
        pos = 2
        with _PREC_LOCK:
            for _ in range(num):
                code = body[pos:pos + 6].decode("ascii", "ignore")
                _PREC[(market, code)] = body[pos + 20]
                pos += 29
        if num < 1000:
            break
        start += num
        time.sleep(0.05)                     # 连打太快会被主站关连接，稍微让一下
    _prec_cache_save()
    return start


# ---------------------------------------------------------------- 解析：快照

# 记录字段顺序（用真实快照对齐过：基准价 1174 分 = 11.74 元、最高 1186、最低 1168、量 1437905 手、
# 额 1689184000 元，与当日日线逐项吻合）。价格是相对基准价的差分，单位是**分**（1/100 元），不是千分之一。
# 第 2 个 varint（amount_cents 之后的 skip1）语义未定，先留着；记录尾部还有 2 个原始字节与 4 个跳过位。
SNAP_FIELDS = (
    ("active", "u16"),
    ("price", "price"),
    ("last_close", "price"),
    ("open", "price"),
    ("high", "price"),
    ("low", "price"),
    ("amount_hundred", "varint"),
    ("skip1", "varint"),
    ("vol", "varint"),
    ("cur_vol", "varint"),
    ("amount", "float4"),
    ("s_vol", "varint"),
    ("b_vol", "varint"),
    ("skip2", "varint"),
    ("skip3", "varint"),
)
for _lvl in range(1, 6):
    SNAP_FIELDS += (("bid%d" % _lvl, "price"), ("ask%d" % _lvl, "price"),
                    ("bid_vol%d" % _lvl, "varint"), ("ask_vol%d" % _lvl, "varint"))
SNAP_FIELDS += (("raw2", "raw2"), ("skip4", "varint"), ("skip5", "varint"),
                ("skip6", "varint"), ("skip7", "varint"),
                ("speed", "i16"), ("active2", "u16"))

PRICE_SCALE = 100.0     # 快照价单位是分（股票的小数位 2；ETF 3 位、转债 4 位按码表缩放）
CT_LIST, CAT_LIST, VER_LIST, FLAG_LIST = 0x0450, 0x1801, 0x0164, 0x01   # 码表，取小数位用


def parse_snapshot(body, want, scale=None):
    """解析 0x053E 应答。

    body: 4 字节头（01 01 / u16 只数），随后每只一条记录：市场 u8 + 代码 6s + 字段。
    want: 请求里的 [(market, code)]，顺序即回包顺序。头部之后第 k 条就是请求第 k 只，
          回包的码也确实是它（实测 24 只双市场全对），want 只用来兜底改写。

    scale 是每只的价格分母（10 的 小数位 次方）：股票 100、ETF 1000、转债 10000，
    固定按分算会把 ETF 放大 10 倍、转债放大 100 倍。给了就按只套，没给按 100。
    记录是变长的（价量都是 varint，全零时 56 字节、带真实值 81 字节），所以从记录起点顺序推进。
    单条坏了不影响后面的：捕获异常后整条标记 _truncated，位置由调用方按消费到的位置续。
    """
    if len(body) < 4:
        return []
    num = struct.unpack_from("<H", body, 2)[0]
    out = []
    pos = 4
    for i in range(num):
        start = pos
        mkt = body[start]
        code = body[start + 1:start + 7].decode("ascii", "replace")
        if i < len(want):                                  # 按请求顺序回填，回包码只作核对
            mkt, code = want[i][0], want[i][1].decode("ascii", "replace")
        rec = {"market": mkt, "code": code}
        pos = start + 7
        base = 0
        try:
            for name, kind in SNAP_FIELDS:
                if kind == "u16":
                    rec[name] = struct.unpack_from("<H", body, pos)[0]
                    pos += 2
                elif kind == "i16":
                    rec[name] = struct.unpack_from("<h", body, pos)[0] / 100.0
                    pos += 2
                elif kind == "raw2":
                    rec[name] = body[pos:pos + 2].hex()
                    pos += 2
                elif kind == "float4":
                    rec[name], pos = read_float4(body, pos)
                elif kind == "varint":
                    rec[name], pos = read_varint(body, pos)
                else:
                    delta, pos = read_varint(body, pos)
                    k = scale[i] if scale else PRICE_SCALE
                    if name == "price":
                        base = delta                           # 基准价自身
                        rec[name] = delta / k
                    else:
                        rec[name] = (base + delta) / k
            rec["ts"] = time.time()
        except (IndexError, struct.error):
            rec["_truncated"] = True
        out.append(rec)
    return out


# ---------------------------------------------------------------- 解析：逐笔

def parse_tick(body, scale=PRICE_SCALE):
    """解析 0x0FC5 逐笔成交。体是 u16 条数，随后每条：

        u16 时间（当日分钟数） / 价 varint / 量 varint / 笔数 varint / u16 方向

    价是**差分**：0 表示与上一条相同，0x40 位是负号，单位分；第一条是绝对值。
    量单位手，笔数是这一跳聚合的成交笔数，方向 u16 取 0 主动买 / 1 主动卖 /
    2 中性 / 5 盘后固定价格（15:00 之后那批价格不动、全是 5）。
    布局是用 200 条样本回溯出来的：整段恰好消费完 1472 字节，一条不多一条不少，
    所以尾部那个 u16 不能省，省了后面全错位。
    """
    if len(body) < 2:
        return []
    num = struct.unpack_from("<H", body, 0)[0]
    out = []
    pos = 2
    px = 0
    for _ in range(num):
        if pos + 4 > len(body):
            break
        tmin = struct.unpack_from("<H", body, pos)[0]
        dp, pos2 = read_varint(body, pos + 2)
        px += dp
        vol, pos3 = read_varint(body, pos2)
        cnt, pos4 = read_varint(body, pos3)
        side = struct.unpack_from("<H", body, pos4)[0] if pos4 + 2 <= len(body) else 2
        out.append({"t": "%02d:%02d:%02d" % (tmin // 60, tmin % 60, 0),
                    "price": px / scale, "vol": vol, "trades": cnt, "side": side})
        pos = pos4 + 2
    return out


def parse_order(body, scale=PRICE_SCALE):
    """解析 0x0FC2。它是 0FC5 同一条成交流的细时标版：每条**末尾**多一个字节，那字节是秒。

        u16 时间(分) / 价 varint（同上，0 与上一条相同） / 量 varint / 笔数 varint / u16 方向 / u8 秒

    证据是条数守恒：3 条回 27 字节 = 2 + 3×8，6 条回 52 = 2 + 6×8，多出来那一字节落在每条末尾。
    该字节是 0 到 59、相邻恒差 3 的秒（3600 条里 0 条越界，97% 以上相邻差正好 3，跨 60 回卷），
    即时间是 3 秒粒度：t 输出到秒，sec 另给一份原值。与 0FC5 逐条比 1200 条，价格/量/方向完全一致。
    """
    if len(body) < 2:
        return []
    num = struct.unpack_from("<H", body, 0)[0]
    out = []
    pos = 2
    px = 0
    for _ in range(num):
        if pos + 5 > len(body):
            break
        tmin = struct.unpack_from("<H", body, pos)[0]
        dp, pos2 = read_varint(body, pos + 2)
        px += dp
        vol, pos3 = read_varint(body, pos2)
        cnt, pos4 = read_varint(body, pos3)
        side = struct.unpack_from("<H", body, pos4)[0] if pos4 + 2 <= len(body) else 2
        sec = body[pos4 + 2] if pos4 + 2 < len(body) else 0
        out.append({"t": "%02d:%02d:%02d" % (tmin // 60, tmin % 60, sec),
                    "price": px / scale, "vol": vol, "trades": cnt,
                    "side": side, "sec": sec, "kind_code": sec})
        pos = pos4 + 3
    return out

class TdxConnection:
    def __init__(self, cfg_path=CFG_DEFAULT, timeout=8.0, verbose=False, hosts=None, on_ban=None):
        self.cfg_path = cfg_path or CFG_DEFAULT   # None 会让 configparser 抛 TypeError
        self.timeout = timeout
        self.verbose = verbose
        self.sock = None
        self.host = None
        self.port = None
        self._lock = threading.Lock()
        self._inflight = 0
        self._sem = threading.Semaphore(MAX_INFLIGHT)
        self._banned = []          # 这轮撞过"连上即关"的主机，重连时先跳过
        self._hosts = list(hosts) if hosts else None   # 给定就只用这张表（池子用它）
        self.on_ban = on_ban       # 主机被拉黑时的回调，池子用它挪队尾

    # ---------- 配置与连接 ----------
    @staticmethod
    def _split_host(item):
        item = item.strip()
        if not item:
            return None
        if item.startswith("[") and "]" in item:            # [IPv6]:port
            host, _, port = item[1:].partition("]")
            return host, int(port.lstrip(":") or 7709)
        if item.count(":") == 1:
            host, port = item.split(":")
            try:
                return host, int(port)
            except ValueError:
                return host, 7709
        return item, 7709                                  # 裸 IPv6 / 裸主机

    def _hosts_from(self, path):
        """从一个 ini 风格文件里取 (权重, IP, 端口)；只认 IPAddressNN 这类编号键。"""
        cp = configparser.ConfigParser(strict=False)
        cp.read(path, encoding="gbk")
        got = []
        for section in getattr(self, "sections", ("HQHOST", "TRYHQHOST")):
            if not cp.has_section(section):
                continue
            for m in __import__("re").finditer(r"^ipaddress(\d+)$", "\n".join(cp.options(section)),
                                               __import__("re").M):
                idx = m.group(1)
                ip = cp.get(section, "IPAddress" + idx, fallback="").strip()
                if not ip:
                    continue
                port = cp.getint(section, "Port" + idx, fallback=7709)
                got.append((cp.getint(section, "Weight" + idx, fallback=0), ip, port))
        return got

    def load_hosts(self):
        if self._hosts:
            return list(self._hosts)
        """主机来源：connect.cfg 优先；没有站点时退到客户端维护的 newhost.lst。

        两个地址都可被 TDX_CFG 覆盖；newhost.lst 由官方更新器写入，
        路径是 <安装根>/T0002/newhost.lst 或 <安装根>/newhost.lst。
        """
        cfg = self.cfg_path or CFG_DEFAULT
        root = os.path.dirname(os.path.abspath(cfg))
        hosts = self._hosts_from(cfg)
        if not hosts:
            for cand in (os.path.join(root, "T0002", "newhost.lst"),
                         os.path.join(root, "newhost.lst")):
                if os.path.isfile(cand):
                    hosts = self._hosts_from(cand)
                    if hosts:
                        break
        if not hosts:
            raise TdxError("connect.cfg 与 newhost.lst 都没有可用主机")
        hosts.sort(key=lambda x: -x[0])
        return [(ip, port) for _w, ip, port in hosts]

    def connect(self):
        last = None
        import random, copy
        hosts = copy.deepcopy(self.load_hosts())
        random.shuffle(hosts)
        for host, port in hosts:
            try:
                s = socket.create_connection((host, port), timeout=self.timeout)
                s.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
                self.sock = s
                if getattr(self, "handshake", True):
                    self._handshake()
                self.host, self.port = host, port
                print(f'Use host-{self.host}:{self.port}')
                return host, port
            except Exception as exc:                        # noqa: BLE001
                last = exc
                try:
                    if self.sock:
                        self.sock.close()
                except Exception:                           # noqa: BLE001
                    pass
                self.sock = None
                print(f'Skip host-{host}:{port}')
        raise TdxError("全部主机连接失败: %r" % (last,))

    def _close(self):
        if self.sock is not None:
            try:
                self.sock.close()
            except OSError:
                pass
            self.sock = None

    def _send_raw(self, data):
        self.sock.sendall(data)

    def _recv_exact(self, n):
        buf = b""
        while len(buf) < n:
            chunk = self.sock.recv(n - len(buf))
            if not chunk:
                raise TdxError("连接被关闭")
            buf += chunk
        return buf

    ## 请求帧
    # 帧结构：0C 魔数 + u16(hi<<8|类别) + u16 版本 + 1 字节标志 + u16 声明长度 ×2 + u16 命令字 + 载荷。
    # 服务端按命令字路由，所以 commtype 必须与业务成对出现，写错只会拿到别的推送。
    def _request(self, payload, category_byte, version, commtype, hi=0x08, flag5=0x01):
        body = struct.pack("<H", commtype) + payload
        length = len(body)
        frame = b"\x0c" + struct.pack("<H", (hi << 8) | category_byte)
        frame += struct.pack("<H", version) + bytes([flag5])
        frame += struct.pack("<HH", length, length) + body
        with self._sem:
            with self._lock:
                last_exc = None
                # 该服务端一条连接只服务一次业务请求，失败即弃连接重连，最多三次。
                for _attempt in range(3):
                    try:
                        if self.sock is None:
                            self.connect()
                        self._send_raw(frame)
                        # 应答头 +10 回显 commtype；中间可能夹带推送，逐条丢弃直到对上。
                        for _ in range(8):
                            body = self._read_message()
                            if self._last_commtype == commtype:
                                return body
                        last_exc = TdxError("应答 commtype 不匹配（期望 0x%04X）" % commtype)
                    except (TdxError, OSError) as exc:
                        last_exc = exc
                        if self.host and self.host not in self._banned:
                            self._banned.append(self.host)   # 换一台再试
                    # 实测该服务端每连接只服务一次业务请求，失败后重连再试。
                    self._close()
                raise TdxError("请求失败: %r" % (last_exc,))

    ## 帧调用
    # 一次调用 = 一条连接：连接 -> 握手三帧与业务帧同发 -> 按 commtype 认领应答。
    # 声明长度可以由 extra 抬高（逐笔族要比实际体多 2），主机撞上"连上即关"就换一台重试。
    def call(self, commtype, payload, catword, ver, flag, extra=0, tries=3):
        last = None
        for i in range(tries):
            try:
                return self._call_once(commtype, payload, catword, ver, flag, extra)
            except (TdxError, OSError) as exc:
                last = exc
                self._close()
                self._ban_current()
                if i + 1 < tries:
                    time.sleep(0.15 * (i + 1))
        raise last

    def _ban_current(self):
        """把刚失败的主机记下来并回调，重连时先跳过它。"""
        if self.host and self.host not in self._banned:
            self._banned.append(self.host)
            if self.on_ban:
                self.on_ban(self.host)

    def _call_once(self, commtype, payload, catword, ver, flag, extra=0):
        if self.sock is None:
            self.connect()
        declared = len(payload) + extra
        frame = (bytes([0x0C]) + struct.pack("<HHBHH", catword, ver, flag, declared, declared)
                 + struct.pack("<H", commtype) + payload)
        self.sock.sendall(HS1 + HS2 + HS3 + frame)
        # 握手应答条数不固定（实测 1 到 2 条），所以不预读，统一按 commtype 认领业务应答。
        for _ in range(8):
            got = self._read_message()
            if self._last_commtype == commtype:
                return got
        raise TdxError("commtype 不匹配（期望 0x%04X）" % commtype)

    ## 码表分页
    # 这族要握住手、三条握手一条一条发，声明长度还要把 2 字节 commtype 算进去；
    # 照快照那套一次灌进去或者少算长度，服务端会一直不理。
    def list_page(self, market, start, count=1000):
        last = None
        for attempt in range(5):
            try:
                return self._list_page_once(market, start, count)
            except (TdxError, OSError) as exc:
                last = exc
                self._close()
                self._ban_current()
                time.sleep(0.15 * (attempt + 1))
        raise last

    def _list_page_once(self, market, start, count=1000):
        if self.sock is None:
            self.connect()
        for pkt in (HS1, HS2):                     # Setup1/2 各回一条，必须读干净
            self.sock.sendall(pkt)
            self._read_message()
        self.sock.sendall(HS3)
        body = struct.pack("<H", CT_LIST) + struct.pack("<HH", market, start)
        frame = (bytes([0x0C]) + struct.pack("<HHBHH", CAT_LIST, VER_LIST, FLAG_LIST, len(body), len(body))
                 + body)
        self.sock.sendall(frame)
        for _ in range(8):
            got = self._read_message()
            if self._last_commtype == CT_LIST:
                return got
        raise TdxError("码表 commtype 不匹配")

    ## 业务取数
    def _scales(self, stocks):
        """这批证券的价格分母；表没加载过先加载该市场，查不到按 100 兜底。"""
        for mkt, _code in stocks:
            if mkt not in _PREC_READY:
                try:
                    load_precision(self.list_page, mkt)
                except Exception as exc:               # noqa: BLE001
                    print("小数位表取不到（%r），按 100 兜底" % (exc,), file=sys.stderr)
                _PREC_READY.add(mkt)
                self._close()              # 码表把连接用掉了，别留给业务帧
        return [price_scale(m, c.decode("ascii", "replace")) for m, c in stocks]

    def snapshot(self, stocks):
        """五档快照。请求体：前缀 + 数量 + 证券表 + 0C 01。

        0C 01 必须排在证券表之后（客户端原包如此）。放到数量后面也能回包，但服务端会按错位解析：
        代码只认末位、回包恒带 600839、数据错开一条，三个老毛病都出自这一个字节位。
        """
        ask = list(stocks)
        scales = self._scales(ask)
        payload = (SNAP_PREFIX + struct.pack("<H", len(ask))
                   + b"".join(struct.pack("<B6s", m, c) for m, c in ask) + SNAP_TAIL)
        body = self.call(CT_SNAPSHOT, payload, SNAP_CAT, SNAP_VER, SNAP_FLAG)
        recs = parse_snapshot(body, ask, scales)
        if len(recs) < len(ask):
            recs += [{}] * (len(ask) - len(recs))
        out = recs[:len(ask)]
        for rec, (mkt, code) in zip(out, ask):
            srv = rec.get("code", "")
            rec["market"] = mkt
            rec["code"] = code.decode("ascii", "replace")
            rec["code_srv"] = srv
            if srv and srv != rec["code"]:
                rec["mismatch"] = srv
        return out

    def ticks(self, market, code, start=0, count=30):
        """逐笔成交（0FC5 分钟版）。"""
        self._scales([(market, code)])
        scale = tick_scale(market, code.decode("ascii", "replace"))
        payload = struct.pack("<H", market) + code.ljust(6, b"\x00") + struct.pack("<HH", start, count)
        body = self.call(CT_TICK, payload, TICK_CAT, TICK_VER, TICK_FLAG, extra=2)
        return parse_tick(body, scale)

    def orders(self, market, code, start=0, count=30):
        """逐笔成交带秒（0FC2），同一路流水，每条尾多一个 3 秒粒度的秒字节。"""
        self._scales([(market, code)])
        scale = tick_scale(market, code.decode("ascii", "replace"))
        payload = struct.pack("<H", market) + code.ljust(6, b"\x00") + struct.pack("<HH", start, count)
        body = self.call(CT_ORDER, payload, TICK_CAT, TICK_VER, TICK_FLAG, extra=2)
        return parse_order(body, scale)

    def probe(self):
        """健康探测：发一条日线请求，能回包就算活。"""
        payload = (b"\x00" + struct.pack("<H", 9) + struct.pack("<B6s", 1, b"600000")
                   + struct.pack("<HH", 0, 1))
        return self.call(CT_BARS, payload, (0x08 << 8) | CAT_BARS, VER_BARS, 0, tries=2)

    ## 应答帧
    # 16 字节头：前 10 字节是回显，10..12 是命令字（用来丢弃夹带的推送），12..16 是压缩后与解压后长度。
    def _read_message(self):
        head = self._recv_exact(16)
        self._last_commtype = struct.unpack_from("<H", head, 10)[0]
        _, _, _, zipsize, unzipsize = struct.unpack("<IIIHH", head)
        if zipsize > 0x200000 or unzipsize > 0x200000:
            raise TdxError("异常长度 zipsize=%d unzipsize=%d" % (zipsize, unzipsize))
        raw = self._recv_exact(zipsize) if zipsize else b""
        if zipsize != unzipsize:
            raw = zlib.decompress(raw)
        return raw

    ## 握手
    # Setup1/Setup2 服务端会回应答，必须读干净，否则后续报文错位；Setup3 无应答，发出后直接等业务帧。
    def _handshake(self):
        # Setup1/Setup2 有应答必须读干净，否则后续报文错位；Setup3 无应答，发了就等业务帧。
        for pkt in (
            bytes.fromhex("0c0218930001030003000d0001"),
            bytes.fromhex("0c0218940001030003000d0002"),
        ):
            self._send_raw(pkt)
            self._read_message()
        self._send_raw(bytes.fromhex(
            "0c0318990001200020000fdbd5d0c9ccd6a4a8af0000008fc22540130000d500c9ccbdf0d7ea00000002"))

    ## 通用裸帧
    # 给不走 _request（它把 commtype 塞进 body）的那些命令用：commtype 在头 10..12，body 原样，
    # 两段长度相等即不压缩。应答同样按 commtype 对齐，最多丢弃 8 条夹带推送。
    def request_raw(self, commtype, payload=b"", catword=1, ver=0, flag=0):
        body = payload
        frame = b"\x0c" + struct.pack("<HHBHH", catword, ver, flag, len(body), len(body))
        frame += struct.pack("<H", commtype) + body
        with self._sem:
            with self._lock:
                last_exc = None
                for _attempt in range(3):
                    try:
                        if self.sock is None:
                            self.connect()
                        self._send_raw(frame)
                        for _ in range(8):
                            got = self._read_message()
                            if self._last_commtype == commtype:
                                return got
                        last_exc = TdxError("应答 commtype 不匹配（期望 0x%04X）" % commtype)
                    except (TdxError, OSError) as exc:
                        last_exc = exc
                    self._close()
                raise TdxError("请求失败: %r" % (last_exc,))

    ## 资讯服务器（INFOHOST 7711）的问候帧
    # 0x1F7E 是客户端连上资讯口后的第一条命令，body 固定两个零字节；应答是明文段里的问候，
    # 带服务端时间、版本串、路径前缀 /tdx/infohost 和 F10/表格两个能力标志。不吃登录态。
    def info_hello(self):
        body = self.request_raw(0x1F7E, b"\x00\x00")
        out = {"raw": body}
        stamp = body[9:40].split(b"\x00")[0]
        out["time"] = stamp.decode("gbk", "ignore")
        m = re.search(rb"[0-9][0-9.]*\s*build[^\x00]*", body)
        out["build"] = m.group(0).decode("ascii", "ignore") if m else ""
        at = body.find(b"/tdx/")
        out["path"] = body[at:body.find(b"\x00", at)].decode("ascii", "ignore") if at >= 0 else ""
        out["f10"] = b"F10: Y" in body
        out["table"] = b"Table: Y" in body
        return out

    def heartbeat(self):
        self._send_raw(bytes.fromhex("0c0108000101000000000000"))

    # ---------- 码表 ----------
    def security_quotes(self, stocks):
        """快照行情（commtype 0x053E，五档）。stocks 为 [(market, b"code"), ...]。

        注意：本机对银河 v11.68 主站实测该命令只被接受、不回包（疑被 L2 门控），
        代码留作可用主站的备选；批量截面优先用 BDHQ。
        """
        n = len(stocks)
        payload = b"\x05\x00\x00\x00" + struct.pack("<H", n)
        payload += b"".join(struct.pack("<B6s", m, c) for m, c in stocks)
        # 0x053E 的声明长度固定为 每只 7 字节 + 12 字节头，先算长度再拼帧。
        decl = 7 * n + 12
        frame = (b"\x0c" + b"\x01\x20\x63\x00\x02" + struct.pack("<HH", decl, decl)
                 + struct.pack("<H", 0x053E) + payload)
        with self._sem:
            with self._lock:
                if self.sock is None:
                    self.connect()
                self._send_raw(frame)
                return parse_quotes(self._read_message())

    def security_count(self, market):
        payload = struct.pack("<H", market) + bytes.fromhex("75c73301")
        body = self._request(payload, 0x0C, 0x016C, 0x044E, hi=0x18, flag5=0x00)
        return struct.unpack_from("<H", body, 0)[0]

    def security_list(self, market, start, count=1000):
        body = self._request(struct.pack("<HH", market, start), 0x01, 0x0164, 0x0450, hi=0x18)
        num = struct.unpack_from("<H", body, 0)[0]
        pos, out = 2, []
        for _ in range(num):
            code, _unit, name_b, _r1, _dp, _pc, _r2 = struct.unpack_from("<6sH8s4sB4s4s", body, pos)
            pos += 29
            out.append((market, code.decode("ascii", "ignore"),
                        name_b.split(b"\0")[0].decode("gbk", "ignore")))
        return out

    # 应答体：9 字节前缀 + u16 条数，每条 8 字节身份 + 日期 + 类别 + 16 字节浮点区；
    # 只有类别 1 用满四个浮点，11/12 取第 3 个，13/14 取第 1、3 个，其余类别只留日期。
    def xdxr_info(self, market, code):
        """除权除息明细（服务端权限内的 gbbq 子集），逐条返回事件。"""
        payload = struct.pack("<H", 1) + struct.pack("<B6s", market, code.encode())
        body = self._request(payload, 0x1F, 0x0076, 0x000F, hi=0x18)
        if len(body) < 11:
            return []
        pos = 9
        num = struct.unpack_from("<H", body, pos)[0]
        pos += 2
        out = []
        for _ in range(num):
            mkt, codeb = struct.unpack_from("<B6s", body, pos)
            pos += 8                      # 7 字节身份 + 1 字节保留
            ymd, pos = unpack_date(9, body, pos)
            cat = body[pos]
            pos += 1
            raw = body[pos:pos + 16]
            pos += 16
            ev = {"market": mkt, "code": codeb.decode("ascii", "ignore"),
                  "date": ymd[0] * 10000 + ymd[1] * 100 + ymd[2], "category": cat}
            if cat == 1:
                ev["dividend"], ev["rights_price"], ev["bonus"], ev["rights"] = \
                    struct.unpack("<ffff", raw)
            elif cat in (11, 12):
                ev["shrink"] = struct.unpack("<f", raw[8:12])[0]
            elif cat in (13, 14):
                ev["warrant_price"], ev["warrant_ratio"] = \
                    struct.unpack("<f", raw[0:4])[0], struct.unpack("<f", raw[8:12])[0]
            else:
                ev["shares_before"], ev["shares_after"] = \
                    struct.unpack("<I", raw[0:4])[0], struct.unpack("<I", raw[12:16])[0]
            out.append(ev)
        return out

    # ---------- 行情 ----------
    def security_bars(self, category, market, code, start=0, count=100):
        if count > 800:
            raise ValueError("单次最多 800 条")
        payload = struct.pack("<H6sHHHHIIH", market, code.encode(),
                              category, 1, start, count, 0, 0, 0)
        body = self._request(payload, CAT_BARS, VER_BARS, CT_BARS)
        num = struct.unpack_from("<H", body, 0)[0]
        pos, base, out = 2, 0, []
        # 服务端声明条数常大于实际，尾部会剩碎字节；日期不像日期就收工，别硬解出天文数字。
        for _ in range(num):
            if pos + 4 > len(body):
                break
            dt, pos = unpack_date(category, body, pos)
            stamp = dt[0] * 10000 + dt[1] * 100 + dt[2]
            if not (19900101 <= stamp <= 20301231):
                break
            d_open, pos = get_price(body, pos)
            d_close, pos = get_price(body, pos)
            d_high, pos = get_price(body, pos)
            d_low, pos = get_price(body, pos)
            vol_f, pos = struct.unpack_from("<I", body, pos)[0], pos + 4
            amt_f, pos = struct.unpack_from("<I", body, pos)[0], pos + 4
            o = base + d_open
            c = o + d_close
            h = o + d_high
            low = o + d_low
            base = o + d_close
            out.append({"dt": dt, "open": o / 1000.0, "close": c / 1000.0,
                        "high": h / 1000.0, "low": low / 1000.0,
                        "vol": get_volume(vol_f), "amount": get_volume(amt_f)})
        return out

    def minute_time_data(self, market, code):
        """当日分时 240 点，返回 [{t, price, vol}]；价格是逐点差分累加。"""
        self._scales([(market, code.encode())])
        scale = price_scale(market, code)
        body = self.call(CT_MINUTE, struct.pack("<H6sI", market, code.encode(), 0),
                         CAT_MINUTE, VER_MINUTE, 0, extra=2)
        # 应答头：条数(u16) + 0000 + 01 + 代码(6 字节) + 2 字节填充，记录自偏移 13 起。
        num = struct.unpack_from("<H", body, 0)[0]
        pos, px, out = 13, 0, []
        for i in range(num):
            diff, pos = get_price(body, pos)
            vol, pos = get_price(body, pos)
            px += diff
            out.append({"t": "%02d:%02d" % (i // 60 + 9, i % 60), "price": px / scale, "vol": vol})
        return out

    def minute(self, market, code):
        """当日分时 240 点。"""
        return self.minute_time_data(market, code)

    def tick(self, market, code, start=0, count=50):
        """逐笔成交（0x0FC5 分钟版）。要秒级时标用 orders()。"""
        self._scales([(market, code.encode())])
        scale = tick_scale(market, code)
        payload = struct.pack("<H6sHH", market, code.encode(), start, count)
        body = self.call(CT_TICK, payload, TICK_CAT, TICK_VER, TICK_FLAG, extra=2)
        return parse_ticks(body, scale)


# ---------------------------------------------------------------- 分钟记录与文件

def normalize_codes(codes):
    """把代码补齐六位：PowerShell 会把 000016,301139 当数字数组传，前导零会丢。"""
    return [str(c).strip().zfill(6) for c in codes if str(c).strip()]


# 分钟族的落点：kind -> (category, 子目录, 扩展名)。1 分钟是 cat 7，5 分钟是 cat 0。
# 15/30/60 分钟客户端本地没有独立目录，落在 fzline 下用 .lc15/.lc30/.lc60 区分（记录同为 32 字节）
KIND_MAP = {"day": (9, "lday", ".day"), "min": (7, "minline", ".lc1"), "5min": (0, "fzline", ".lc5"),
            "15min": (1, "fzline", ".lc15"), "30min": (2, "fzline", ".lc30"), "60min": (3, "fzline", ".lc60")}
# 分钟记录 32 字节：u16 打包日 + u16 分钟 + 五浮点 + u32 量 + u32 保留。
# 打包日把年放在 bit11 以上（减 2004），低 11 位是 月*100+日。
MIN_REC = 32


def pack_min(y, mo, d, hh, mm, o, h, lo, c, amt, vol):
    """把一条分钟记录打成 32 字节，时间用 TDX 的打包日加分钟数。"""
    zipday = ((y - 2004) << 11) | (mo * 100 + d)
    return struct.pack("<HH5fII", zipday, hh * 60 + mm, o, h, lo, c, amt, vol, 0)


def pack_day(ymd, o, h, lo, c, amt, vol):
    """把一条日线打成 32 字节：u32 日期 + 四价（分）+ 额 + 量 + 保留。"""
    return struct.pack("<IIIIIfII", ymd, int(round(o * 100)), int(round(h * 100)),
                       int(round(lo * 100)), int(round(c * 100)), float(amt), int(vol), 0)


def unpack_min(raw, off):
    """解一条分钟记录，返回 ((年,月,日,时,分), 开,高,低,收,额,量)。"""
    zipday, minutes = struct.unpack_from("<HH", raw, off)
    y = (zipday >> 11) + 2004
    mo, d = divmod(zipday % 2048, 100)
    hh, mm = divmod(minutes, 60)
    o, h, lo, c, amt = struct.unpack_from("<5f", raw, off + 4)
    vol = struct.unpack_from("<I", raw, off + 24)[0]
    return (y, mo, d, hh, mm), o, h, lo, c, amt, vol


def read_ticks_file(path):
    """按 kind 读出已有文件，统一成 {时间键: 载荷} 的字典，供合并写用。"""
    out = {}
    if not os.path.isfile(path):
        return out
    raw = open(path, "rb").read()
    if path.endswith(".day"):
        for off in range(0, len(raw) - 31, 32):
            out[struct.unpack_from("<I", raw, off)[0]] = raw[off:off + 32]
        return out
    for off in range(0, len(raw) - MIN_REC + 1, MIN_REC):
        key = unpack_min(raw, off)[0]
        out[key] = raw[off:off + MIN_REC]
    return out


def write_all(path, store):
    """把合并后的字典按时间升序整体重写；日线的键是数字日期，分钟是元组。"""
    with open(path, "wb") as fh:
        for k in sorted(store):
            fh.write(store[k])


# ---------------------------------------------------------------- 本地读取

def read_day(path):
    """读本地 .day：32 字节记录（日期 + 四价 + 额 + 量 + 保留），价格除 100。"""
    out = []
    raw = open(path, "rb").read()
    for off in range(0, len(raw) - 31, 32):
        ymd, o, h, lo, c, amt, vol, _ = struct.unpack_from("<IIIIIfII", raw, off)
        out.append({"date": ymd, "dt": (ymd // 10000, (ymd % 10000) // 100, ymd % 100),
                    "open": o / 100.0, "high": h / 100.0, "low": lo / 100.0,
                    "close": c / 100.0, "amount": amt, "vol": vol})
    return out


def read_lc1(path):
    """读本地分钟线（.lc1/.lc5 同布局）：u16 打包日 + u16 分钟 + 五浮点 + u32 量 + 保留。"""
    out = []
    raw = open(path, "rb").read()
    for off in range(0, len(raw) - 31, 32):
        zipday, minutes = struct.unpack_from("<HH", raw, off)
        o, h, lo, c, amt = struct.unpack_from("<5f", raw, off + 4)
        vol = struct.unpack_from("<I", raw, off + 24)[0]
        out.append({"dt": ((zipday >> 11) + 2004, (zipday % 2048) // 100, (zipday % 2048) % 100,
                           minutes // 60, minutes % 60),
                    "open": o, "high": h, "low": lo, "close": c, "amount": amt, "vol": vol})
    return out


def vipdoc_path(root, market, code, kind="lday"):
    """拼本地行情文件路径。"""
    ext = {"lday": ".day", "minline": ".lc1", "fzline": ".lc5"}.get(kind, "." + kind)
    return os.path.join(root, "vipdoc", MARKET_DIR[market], kind,
                        "%s%s%s" % (PREFIX[market], code, ext))


def _print_rows(rows, out=None, limit=10):
    if out:
        with open(out, "w", encoding="utf-8", newline="") as fh:
            w = csv.writer(fh)
            w.writerow(["date", "open", "high", "low", "close", "amount", "volume"])
            for r in rows:
                d = r.get("dt", (0, 0, 0))
                w.writerow(["%04d%02d%02d" % (d[0], d[1], d[2]), r["open"], r["high"],
                            r["low"], r["close"], r.get("amount", 0), r.get("vol", 0)])
        print("已写 %d 条到 %s" % (len(rows), out))
        return
    for r in rows[:limit]:
        d = r.get("dt", (0, 0, 0))
        print("%04d%02d%02d %8.2f %8.2f %8.2f %8.2f %12d" % (
            d[0], d[1], d[2], r["open"], r["high"], r["low"], r["close"], r.get("vol", 0)))
    if len(rows) > limit:
        print("... 共 %d 条" % len(rows))


def _gbbq_of(vipdoc):
    return os.path.join(os.path.dirname(os.path.abspath(vipdoc)), "T0002", "hq_cache", "gbbq")


def _maybe_adjust(rows, args, market, code):
    """按 --adjust 调 tdx_adjust；缺文件或无事件时提示并按不复权返回。"""
    if getattr(args, "adjust", "none") == "none" or not rows:
        return rows
    path = getattr(args, "gbbq", None) or _gbbq_of(getattr(args, "vipdoc", None) or VIPDOC_DEFAULT)
    if not os.path.isfile(path):
        print("缺 gbbq（%s），按不复权输出" % path, file=sys.stderr)
        return rows
    sys.path.insert(0, BASE_DIR)
    import tdx_adjust
    events = tdx_adjust.load_events(path, market, code)
    if not events:
        print("%s 无复权事件，按不复权输出" % code, file=sys.stderr)
        return rows
    return tdx_adjust.adjust_records(rows, events, args.adjust)


# ---------------------------------------------------------------- 退市清单

def list_delisted(src, markets=(0, 1), out=None):
    """从现成的 vipdoc 里找退市股：本地有文件、主站码表里没有的代码就是。

    主站只服务在册证券（25 台全试过，300431、300104 这类退市票一律 0 条），
    但客户端安装目录或旧备份里往往还留着它们的 .day。这份差集就是该保住的东西。
    """
    conn = TdxConnection()
    table = set()
    try:
        for m in markets:
            total = conn.security_count(m)
            got = 0
            while got < total:
                rows = conn.security_list(m, got, 1000)
                conn._close()
                if not rows:
                    break
                got += len(rows)
                table.update((m, cd) for _mk, cd, _nm in rows)
    finally:
        conn._close()

    rows = []
    for m in markets:
        d = os.path.join(src, "vipdoc", "sh" if m == 1 else "sz", "lday")
        if not os.path.isdir(d):
            continue
        for name in sorted(os.listdir(d)):
            if not name.endswith(".day") or len(name) < 11:
                continue
            code = name[2:8]
            if not code.isdigit():
                continue
            if (m, code) not in table:
                rows.append((m, code))
    if out:
        with open(out, "w", encoding="utf-8") as fh:
            for m, code in rows:
                fh.write("%d:%s\n" % (m, code))
        print("写出 %s" % out)
    return rows, len(table)


def archive_base(src, out_dir=None, day=None):
    """把 base.dbf 的财务快照按日期存一份，攒历史用。

    服务端只给最新一期的财务与股本（base.dbf 单个快照、GXRQ 一个日期），
    历史某天的每股净资产取不到；每天存一份，PB/PE/换手率这些才有人能算。
    """
    src = os.path.abspath(src)
    if os.path.isdir(src):
        src = os.path.join(src, "base.dbf")
    if not os.path.isfile(src):
        raise SystemExit("没有 base.dbf：%s" % src)
    day = day or time.strftime("%Y%m%d")
    out_dir = out_dir or os.path.join(HERE, "db", "base")
    os.makedirs(out_dir, exist_ok=True)
    dst = os.path.join(out_dir, "%s.dbf" % day)
    if os.path.exists(dst) and os.path.getsize(dst) == os.path.getsize(src):
        return dst, False                     # 同一天重复跑，内容大小一致就不重写
    shutil.copyfile(src, dst)
    return dst, True


# ---------------------------------------------------------------- 命令

def cmd_smoke(a):
    c = TdxConnection(timeout=8.0)
    try:
        n = c.security_count(a.market)
        rows = c.tick(a.market, a.code, 0, 5)
        print(json.dumps({"market": a.market, "code": a.code, "count": n,
                          "ticks": len(rows)}, ensure_ascii=False))
        return 0 if rows else 4
    finally:
        c._close()


def cmd_count(a):
    c = TdxConnection(timeout=8.0)
    try:
        print(c.security_count(a.market))
        return 0
    finally:
        c._close()


def cmd_codes(a):
    c = TdxConnection(timeout=8.0)
    try:
        for m, code, name in c.security_list(a.market, a.start, a.count):
            print("%d %s %s" % (m, code, name))
        return 0
    finally:
        c._close()


def cmd_bars(a):
    c = TdxConnection(timeout=8.0)
    try:
        rows = c.security_bars(a.category, a.market, a.code, a.start, a.count)
    finally:
        c._close()
    rows = _maybe_adjust(rows, a, a.market, a.code)
    _print_rows(rows, a.out)
    return 0 if rows else 4


def cmd_minute(a):
    """当日分时。走 1 分钟 K 线（category 7）再筛出今天，比 0x051D 稳。"""
    c = TdxConnection(timeout=8.0)
    rows, start = [], 0
    try:
        while len(rows) < 320:                 # 指数一次只回一根，得按根往后挪
            batch = c.security_bars(7, a.market, a.code, start, 240)
            c._close()
            if not batch:
                break
            rows.extend(batch)
            start += len(batch)
    finally:
        c._close()
    if rows:
        today = rows[-1]["dt"][:3]
        rows = [r for r in rows if r["dt"][:3] == today][::-1]
    rows = [{"t": "%02d:%02d" % (r["dt"][3], r["dt"][4]), "price": r["close"], "vol": r["vol"]}
            for r in rows]
    for r in rows:
        print("%s %8.3f %10d" % (r["t"], r["price"], r["vol"]))
    print("共 %d 点" % len(rows))
    return 0 if rows else 4


def cmd_tick(a):
    c = TdxConnection(timeout=8.0)
    try:
        rows = c.tick(a.market, a.code, a.start, a.count)
    finally:
        c._close()
    for r in rows:
        print("%s %8.3f %8d %d" % (r["t"], r["price"], r["vol"], r["side"]))
    return 0 if rows else 4


def cmd_xdxr(a):
    c = TdxConnection(timeout=8.0)
    try:
        rows = c.xdxr_info(a.market, a.code)
    finally:
        c._close()
    for r in rows:
        print(json.dumps(r, ensure_ascii=False, sort_keys=True))
    return 0 if rows else 4


def cmd_local(a):
    path = vipdoc_path(a.vipdoc, a.market, a.code, a.kind)
    if not os.path.isfile(path):
        print("没有文件: %s" % path, file=sys.stderr)
        return 2
    rows = read_lc1(path) if a.kind in ("minline", "fzline") else read_day(path)
    rows = _maybe_adjust(rows, a, a.market, a.code)
    _print_rows(rows, a.out)
    return 0 if rows else 4


def cmd_login(a):
    """探 7709 的 0x0004 与 UserHost 登录口 TCP 可达性，结果写 JSON。"""
    cfg = a.cfg or CFG_DEFAULT
    got = {"cfg": cfg, "main": [], "login_ports": []}
    host, port, err = None, None, None
    try:
        host, port = TdxConnection(cfg_path=cfg).load_hosts()[0]
    except Exception as exc:                           # noqa: BLE001
        err = repr(exc)[:120]
    sock = None
    if host:
        try:
            sock = socket.create_connection((host, port), timeout=6)
            sock.sendall(bytes.fromhex("0c0218930001030003000d0001"))
            sock.settimeout(6)
            got["main"].append({"variant": "setup1", "host": host, "port": port,
                                "ok": bool(sock.recv(64))})
        except Exception as exc:                       # noqa: BLE001
            got["main"].append({"variant": "setup1", "error": repr(exc)[:120]})
        finally:
            if sock:
                sock.close()
    else:
        got["main"].append({"variant": "setup1", "error": err})
    cp = configparser.ConfigParser(strict=False)
    cp.read(cfg, encoding="gbk")
    uh_host = cp.get("UserHost", "IPAddress", fallback="") if cp.has_section("UserHost") else ""
    try:
        uh_port = int(cp.get("UserHost", "Port", fallback="7715")) if cp.has_section("UserHost") else 7715
    except ValueError:
        uh_port = 7715
    if uh_host:
        try:
            socket.create_connection((uh_host, uh_port), timeout=6).close()
            got["login_ports"].append({"host": uh_host, "port": uh_port, "tcp": True})
        except Exception as exc:                       # noqa: BLE001
            got["login_ports"].append({"host": uh_host, "port": uh_port, "tcp": False,
                                       "error": repr(exc)[:120]})
    out = a.out or os.path.join(BASE_DIR, "login.json")
    with open(out, "w", encoding="utf-8") as fh:
        json.dump(got, fh, ensure_ascii=False, indent=2)
    print("已写", out)
    return 0


def cmd_info_hello(a):
    c = TdxConnection(cfg_path=a.cfg or None, timeout=8.0)
    c.sections = ("INFOHOST", "DSHOST")
    c.handshake = False
    try:
        got = c.info_hello()
    finally:
        c._close()
    got["raw"] = "%d 字节" % len(got.get("raw") or b"")
    print(json.dumps(got, ensure_ascii=False, indent=2))
    return 0 if got.get("time") or got.get("build") else 4


def build_parser():
    ap = argparse.ArgumentParser(description="通达信 7709 行情客户端")
    sub = ap.add_subparsers(dest="cmd", required=True)
    p = sub.add_parser("smoke")
    p.add_argument("--market", type=int, default=1)
    p.add_argument("--code", default="600000")
    p.set_defaults(func=cmd_smoke)
    p = sub.add_parser("count")
    p.add_argument("--market", type=int, default=1)
    p.set_defaults(func=cmd_count)
    p = sub.add_parser("codes")
    p.add_argument("--market", type=int, default=1)
    p.add_argument("--start", type=int, default=0)
    p.add_argument("--count", type=int, default=100)
    p.set_defaults(func=cmd_codes)
    p = sub.add_parser("bars")
    p.add_argument("--market", type=int, default=1)
    p.add_argument("--code", required=True)
    p.add_argument("--category", type=int, default=9)
    p.add_argument("--start", type=int, default=0)
    p.add_argument("--count", type=int, default=100)
    p.add_argument("--out")
    p.add_argument("--adjust", default="none", choices=("none", "hfq", "qfq"))
    p.add_argument("--gbbq")
    p.add_argument("--vipdoc")
    p.set_defaults(func=cmd_bars)
    p = sub.add_parser("minute")
    p.add_argument("--market", type=int, default=1)
    p.add_argument("--code", required=True)
    p.set_defaults(func=cmd_minute)
    p = sub.add_parser("tick")
    p.add_argument("--market", type=int, default=1)
    p.add_argument("--code", required=True)
    p.add_argument("--start", type=int, default=0)
    p.add_argument("--count", type=int, default=50)
    p.set_defaults(func=cmd_tick)
    p = sub.add_parser("xdxr")
    p.add_argument("--market", type=int, default=1)
    p.add_argument("--code", required=True)
    p.set_defaults(func=cmd_xdxr)
    p = sub.add_parser("local")
    p.add_argument("--vipdoc", default=VIPDOC_DEFAULT)
    p.add_argument("--market", type=int, default=1)
    p.add_argument("--code", required=True)
    p.add_argument("--kind", default="lday", choices=("lday", "minline", "fzline"))
    p.add_argument("--out")
    p.add_argument("--adjust", default="none", choices=("none", "hfq", "qfq"))
    p.add_argument("--gbbq")
    p.set_defaults(func=cmd_local)
    p = sub.add_parser("login")
    p.add_argument("--cfg")
    p.add_argument("--out")
    p.set_defaults(func=cmd_login)
    p = sub.add_parser("info-hello")
    p.add_argument("--cfg")
    p.set_defaults(func=cmd_info_hello)
    return ap


def main(argv=None):
    args = build_parser().parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())
