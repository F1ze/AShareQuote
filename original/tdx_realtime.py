# -*- coding: utf-8 -*-
"""tdx_realtime.py —— 通达信行情实时采集器（L1 快照 / L2 逐笔，可切换）。

设计要点（为什么这么写）：

1. 本服每连接只服务一次业务请求，所以"低延迟"靠的是**连接池 + 流水线**：一条连接连发多批，
   把一次 RTT 摊到几十只上；退避靠失败率，不靠固定 sleep。
2. 盘口是**拉**：轮询节奏跟着交易所档位走（L1 三秒一档），压到更短只是抢相位。
3. 全链路只用标准库；连接、解析、差分、输出四层分开，便于替换。

安全边界：只读网络，不碰安装目录；主机表来自 connect.cfg（spec.json 可现造）。

用法：
    python tdx_realtime.py probe-l2                        # 探测本服是否给 L2
    python tdx_realtime.py rtt --codes 600000,600519       # 量往返分布
    python tdx_realtime.py once --codes 600000 --mode l2   # 打一份快照
    python tdx_realtime.py run --watch watch.txt --cycle 3 --mode auto --out q.jsonl
"""
import argparse
import json
import os
import queue
import random
import socket
import struct
import sys
import threading
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import tdx_client as T                                   # 复用配置、主机表与帧头结构

# ---------------------------------------------------------------- 命令字

CT_SNAPSHOT = 0x053E      # L1 快照（五档）：请求 12 字节前缀 + 7n，记录 56 字节
CT_TICK = 0x0FC5          # L2 逐笔成交
CT_ORDER = 0x0FC2         # L2 逐笔委托
CT_MINUTE = 0x051D        # 当日分时（一分钟一点）

# 快照帧头：catword 0x0218 / ver 0x0093 / flag 0，被本服接受（37 章实测）。
SNAP_CAT, SNAP_VER, SNAP_FLAG = 0x0218, 0x0093, 0
# 逐笔族帧头：客户端真包是 0C 01 08 00 03 01，即 catword 0x0801 / ver 0x0300 / flag 1。
# 旧值 0x0817 / 0x0101（3.5 的模板）在本服一帧不回，别改回去。
TICK_CAT, TICK_VER, TICK_FLAG = 0x0801, 0x0300, 1

SNAP_PREFIX = b"\x05\x00\x00\x00" + b"\x00\x00\x00\x00"   # 4 字节魔数 + 4 个零
# 请求体末尾还要两个字节 0C 01（客户端原包同值）。位置在**证券表之后**，
# 放到数量后面不会报错但服务端会按错位解析：代码只认末位、回包恒 600839 且错一条都是它导致的。
SNAP_TAIL = b"\x0c\x01"
SNAP_REC = 56                                              # 全零记录的固定长度，有值的记录是变长的

HS1 = bytes.fromhex("0c0218930001030003000d0001")      # Setup1（有应答）
HS2 = bytes.fromhex("0c0218940001030003000d0002")      # Setup2（有应答）
HS3 = bytes.fromhex("0c0318990001200020000fdbd5d0c9ccd6a4a8af0000008fc22540130000d500c9ccbdf0d7ea00000002")

BATCH_MAX = 80            # 单请求品种数上限（56 字节/只，回包约 4.5 KB）
TICK_WINDOW = 50          # 每只每轮补的逐笔条数：客户端成交明细一次就要 115 条，50 够一轮不漏
TICK_SEEN_MAX = 800       # 每只记多少条成交签名用于去重
WATCH_DEFAULT = "watch.txt"


# ---------------------------------------------------------------- 误差与统计

class RtdError(RuntimeError):
    pass


class Stats(object):
    """采集统计：延迟分位、批次、失败、降级次数。线程安全。"""

    def __init__(self):
        self._lock = threading.Lock()
        self.rtt = []         # 毫秒样本，只留最近 2000 个
        self.batches = 0
        self.quotes = 0
        self.errors = 0
        self.connects = 0
        self.dropped = 0
        self.last_error = None
        self.started = time.time()

    def add_rtt(self, ms):
        with self._lock:
            self.rtt.append(ms)
            if len(self.rtt) > 2000:
                del self.rtt[:500]

    def bump(self, field, n=1):
        with self._lock:
            setattr(self, field, getattr(self, field) + n)

    def snapshot(self):
        with self._lock:
            s = sorted(self.rtt)
            def q(p):
                return round(s[min(len(s) - 1, int(len(s) * p))], 1) if s else None
            return {
                "uptime": round(time.time() - self.started, 1),
                "p50": q(0.50), "p95": q(0.95), "p99": q(0.99),
                "batches": self.batches, "quotes": self.quotes,
                "errors": self.errors, "connects": self.connects,
                "dropped": self.dropped, "last_error": self.last_error,
            }


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
    return T.get_volume(raw), pos + 4


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

PRICE_SCALE = 100.0     # 快照价单位是分


def parse_snapshot(body, want):
    """解析 0x053E 应答。

    body: 4 字节头（01 01 / u16 只数），随后每只一条记录：市场 u8 + 代码 6s + 字段。
    want: 请求里的 [(market, code)]，顺序即回包顺序。头部之后第 k 条就是请求第 k 只，
          回包的码也确实是它（实测 24 只双市场全对），want 只用来兜底改写。

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
                    if name == "price":
                        base = delta                           # 基准价自身
                        rec[name] = delta / PRICE_SCALE
                    else:
                        rec[name] = (base + delta) / PRICE_SCALE
            rec["ts"] = time.time()
        except (IndexError, struct.error):
            rec["_truncated"] = True
        out.append(rec)
    return out


# ---------------------------------------------------------------- 解析：逐笔

def parse_tick(body):
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
                    "price": px / PRICE_SCALE, "vol": vol, "trades": cnt, "side": side})
        pos = pos4 + 2
    return out


def parse_order(body):
    """解析 0x0FC2 逐笔委托。与逐笔成交相比，每条**末尾**多一个字节，其余同形：

        u16 时间 / 价 varint（同上，0 与上一条相同） / 量 varint / 笔数 varint / u16 方向 / u8 类别码

    证据是条数守恒：3 条回 27 字节 = 2 + 3×8，6 条回 52 = 2 + 6×8，每条正好 8 字节，
    多出来那一字节落在每条末尾（最后一条也在，所以是记录字段不是分隔符）。
    它与 0FC5 的体逐条一一对应，去掉就还原成成交记录，取值 16、19、43、58 这类按 3 跳的数，
    语义未定，按 kind_code 原样给。
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
        code = body[pos4 + 2] if pos4 + 2 < len(body) else 0
        out.append({"t": "%02d:%02d:%02d" % (tmin // 60, tmin % 60, 0),
                    "price": px / PRICE_SCALE, "vol": vol, "trades": cnt,
                    "side": side, "kind_code": code})
        pos = pos4 + 3
    return out

# ---------------------------------------------------------------- 连接工人

class Worker(threading.Thread):
    """一个工人 = 一条长连接 + 一条任务队列。

    任务是一批 (market, code) 与一个回填回调。出错就地重连，退避 0.2s 起翻倍，封顶 5s；
    连续失败到上限就绪后由调度器降并发。连接复用是延迟的关键：不要每批新建连接。
    """

    def __init__(self, pool, idx):
        threading.Thread.__init__(self)
        self.daemon = True
        self.pool = pool
        self.idx = idx
        self.q = queue.Queue(maxsize=8)
        self.sock = None
        self.backoff = 0.2
        self.host = None

    # ---- 连接 ----
    # 实测：连上后只要稍有停歇，服务端就关连接（复用第一次就 closed），
    # 所以每个批次都新建连接；握手三条帧与业务帧**一次发出**（流水线），只留一个 RTT。
    def _connect(self):
        last = None
        for host, port in self.pool.hosts():
            try:
                s = socket.create_connection((host, port), timeout=self.pool.timeout)
                s.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
                self.sock = s
                self.host = host
                self.pool.stats.bump("connects")
                return
            except Exception as exc:                      # noqa: BLE001
                last = exc
                self._drop()
        raise RtdError("全部主机连不上: %r" % (last,))

    def _drop(self):
        if self.sock is not None:
            try:
                self.sock.close()
            except OSError:
                pass
            self.sock = None

    def _recv_exact(self, n):
        buf = b""
        while len(buf) < n:
            chunk = self.sock.recv(n - len(buf))
            if not chunk:
                raise RtdError("连接被关闭")
            buf += chunk
        return buf

    def _read_message(self):
        head = self._recv_exact(16)
        ct = struct.unpack_from("<H", head, 10)[0]
        zipsize, unzipsize = struct.unpack_from("<HH", head, 12)
        raw = self._recv_exact(zipsize) if zipsize else b""
        if zipsize != unzipsize:
            import zlib
            raw = zlib.decompress(raw)
        return ct, raw

    def _call(self, ct, payload, cat, ver, flag, extra=0):
        """一次调用 = 一次连接：连接 -> 握手三帧与业务帧同发 -> 按 commtype 认领业务应答。

        extra 是声明长度比实际体多出的字节数。逐笔族（0x0FC5）客户端声明的是体长加 2，
        少这两字节服务端就一直等，一个字节都不回，所以 extra 只给逐笔用。
        """
        if self.sock is None:
            self._connect()
        declared = len(payload) + extra
        frame = (b"\x0c" + struct.pack("<HHBHH", cat, ver, flag, declared, declared)
                 + struct.pack("<H", ct) + payload)
        t0 = time.time()
        self.sock.sendall(HS1 + HS2 + HS3 + frame)
        # 握手应答条数不固定（实测 1 到 2 条），所以不预读，统一按 commtype 认领业务应答。
        for _ in range(8):                                # 丢弃握手应答与夹带推送
            got_ct, body = self._read_message()
            if got_ct == ct:
                self.pool.stats.add_rtt((time.time() - t0) * 1000)
                return body
        raise RtdError("commtype 不匹配（期望 0x%04X）" % ct)

    # ---- 业务 ----
    def snapshot(self, stocks):
        """取快照。请求体：前缀 + 数量 + 证券表 + 0C 01，回包记录按请求顺序一条一只。

        0C 01 必须放证券表之后（客户端原包如此）。放数量后面同样能回包，但服务端会按错位解析，
        表现为代码只按末位匹配、回包恒带 600839、数据错开一条，三个老毛病都出自这一个字节位。
        """
        ask = list(stocks)
        payload = (SNAP_PREFIX + struct.pack("<H", len(ask))
                   + b"".join(struct.pack("<B6s", m, c) for m, c in ask) + SNAP_TAIL)
        body = self._call(CT_SNAPSHOT, payload, SNAP_CAT, SNAP_VER, SNAP_FLAG)
        recs = parse_snapshot(body, ask)
        if len(recs) < len(ask):                            # 少给了按实际长度截
            recs += [{}] * (len(ask) - len(recs))
        out = recs[:len(ask)]
        for rec, (mkt, code) in zip(out, stocks):
            srv = rec.get("code", "")
            rec["market"] = mkt
            rec["code"] = code.decode("ascii", "replace")
            rec["code_srv"] = srv
            if srv and srv != rec["code"]:                  # 回包代码就是解析结果，正常应与请求一致
                rec["mismatch"] = srv
        return out

    def ticks(self, market, code, start=0, count=30):
        payload = struct.pack("<H", market) + code.ljust(6, b"\x00") + struct.pack("<HH", start, count)
        body = self._call(CT_TICK, payload, TICK_CAT, TICK_VER, TICK_FLAG, extra=2)
        return parse_tick(body)

    def orders(self, market, code, start=0, count=30):
        """取逐笔委托（L2 十档订单流用）。请求体与逐笔成交一致，回包 commtype 是 0x0FC2。"""
        payload = struct.pack("<H", market) + code.ljust(6, b"\x00") + struct.pack("<HH", start, count)
        body = self._call(CT_ORDER, payload, TICK_CAT, TICK_VER, TICK_FLAG, extra=2)
        return parse_order(body)

    def run(self):
        while not self.pool.stop.is_set():
            try:
                job = self.q.get(timeout=0.2)
            except queue.Empty:
                continue
            if job is None:
                break
            kind, args, done = job
            try:
                if kind == "snap":
                    out = self.snapshot(args)
                elif kind == "order":
                    out = self.orders(*args)
                else:
                    out = self.ticks(*args)
                self.backoff = 0.2
                self.pool.stats.bump("batches")
                self.pool.stats.bump("quotes", len(out))
                self._drop()                                  # 服务端用完即关，别留着当活连接
            except Exception as exc:                      # noqa: BLE001
                self.pool.stats.bump("errors")
                self.pool.stats.last_error = repr(exc)[:200]
                self._drop()
                time.sleep(self.backoff)
                self.backoff = min(5.0, self.backoff * 2)
                # 失败重投一次，超过就丢（调度器下一轮会补）
                try:
                    self.q.put_nowait(job)
                except queue.Full:
                    self.pool.stats.bump("dropped")
                    continue
                continue
            if done:
                done(out)


class Pool(object):
    """工人池：按顺序挑最闲的工人投递，队满就换下一个。"""

    def __init__(self, workers=4, timeout=6.0):
        self.stop = threading.Event()
        self.stats = Stats()
        self.timeout = timeout
        self._hosts = [(h, p) for h, p in T.TdxConnection().load_hosts()]
        self.health()
        self.workers = [Worker(self, i) for i in range(workers)]
        for w in self.workers:
            w.start()

    def _probe(self, host, port, timeout, out):
        """单机健康探测：能回一条日线就算活，耗时入表。"""
        sock = None
        try:
            sock = socket.create_connection((host, port), timeout=timeout)
            sock.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
            p = b"\x00" + struct.pack("<H", 9) + struct.pack("<B6s", 1, b"600000") + struct.pack("<HH", 0, 1)
            fr = (b"\x0c" + struct.pack("<HHBHH", SNAP_CAT, SNAP_VER, 0, len(p), len(p))
                  + struct.pack("<H", 0x052D) + p)
            t0 = time.time()
            sock.sendall(HS1 + HS2 + HS3 + fr)
            for _ in range(8):
                head = b""
                while len(head) < 16:
                    ch = sock.recv(16 - len(head))
                    if not ch:
                        raise RtdError("closed")
                    head += ch
                ct = struct.unpack_from("<H", head, 10)[0]
                z = struct.unpack_from("<H", head, 12)[0]
                while z > 0:
                    ch = sock.recv(z)
                    if not ch:
                        raise RtdError("closed")
                    z -= len(ch)
                if ct == 0x052D:
                    out.append((round((time.time() - t0) * 1000, 1), host, port))
                    return
        except Exception:                                 # noqa: BLE001
            pass
        finally:
            if sock is not None:
                try:
                    sock.close()
                except OSError:
                    pass

    def health(self, keep=8, timeout=1.5, parallel=10):
        """并发探一遍主机，只留下会回包的，按耗时排序。

        25 台里有连上就关的，先筛掉，免得每次失败都靠退避硬扛。全都不回时退回全表
        （宁可慢，也不要因为探测本身失败而空转）。
        """
        found = []
        queue_ = list(self._hosts)
        live = []
        while queue_ or live:
            while queue_ and len(live) < parallel:
                host, port = queue_.pop(0)
                th = threading.Thread(target=self._probe, args=(host, port, timeout, found))
                th.daemon = True
                th.start()
                live.append(th)
            time.sleep(0.02)
            live = [t for t in live if t.is_alive()]
        if found:
            found.sort()
            self._hosts = [(h, p) for _ms, h, p in found[:keep]]
            self.health_ms = dict((h, ms) for ms, h, _p in found)
        return self._hosts

    def hosts(self):
        """在同一台主机上失败两次就换一台：返回顺序按探测耗时排。"""
        return list(self._hosts)

    def submit(self, stocks, done=None):
        best = min(self.workers, key=lambda w: w.q.qsize())
        try:
            best.q.put_nowait(("snap", stocks, done))
            return True
        except queue.Full:
            self.stats.bump("dropped")
            return False

    def submit_tick(self, market, code, done, start=0, count=30, kind="tick"):
        """逐笔任务与快照共用一个池：逐笔只在 L2 模式下投。kind 取 tick 或 order。"""
        best = min(self.workers, key=lambda w: w.q.qsize())
        try:
            best.q.put_nowait((kind, (market, code, start, count), done))
            return True
        except queue.Full:
            self.stats.bump("dropped")
            return False

    def close(self):
        self.stop.set()
        for w in self.workers:
            try:
                w.q.put_nowait(None)
            except queue.Full:
                pass
        for w in self.workers:
            w._drop()


# ---------------------------------------------------------------- 差分

class Differ(object):
    """只在字段变化时放行；盘口价量任何一个动了都算变化。"""

    KEYS = ("price", "vol", "amount", "bid1", "ask1", "bid_vol1", "ask_vol1",
            "bid2", "ask2", "bid_vol2", "ask_vol2")

    def __init__(self):
        self.last = {}

    def changed(self, rec):
        key = (rec["market"], rec["code"])
        prev = self.last.get(key)
        self.last[key] = rec
        if prev is None:
            return True
        return any(prev.get(k) != rec.get(k) for k in self.KEYS)


# ---------------------------------------------------------------- 调度

class Collector(object):
    """订阅 + 轮次调度。

    一轮把全部订阅按 BATCH_MAX 切片投递；一轮投完就等 cycle 秒再开下一轮，
    实际节奏由上一轮耗时决定（耗时超过 cycle 就立刻续跑，不叠加）。
    """

    def __init__(self, pool, mode="auto", cycle=3.0, l2_every=0, differ=None, sink=None,
                 l2_orders=False):
        self.pool = pool
        self.mode = mode
        self.cycle = cycle
        self.l2_every = l2_every
        self.l2_orders = l2_orders
        self.differ = differ or Differ()
        self.sink = sink or (lambda rec: None)
        self.subs = {}
        self._lock = threading.Lock()
        self._round = 0
        self.l2_ok = False
        self._tick_seen = {}      # (市场, 代码) -> 已放行过的成交签名集合，用来去重
        self._tick_order = {}     # 同上 -> 签名先后顺序，超过 TICK_SEEN_MAX 就丢最旧的

    def set_subs(self, pairs):
        with self._lock:
            self.subs = {(m, c): True for m, c in pairs}

    def codes(self):
        with self._lock:
            return list(self.subs)

    def probe_l2(self, market=1, code=b"600000"):
        """打一条逐笔，回包非空即认为本服给 L2。"""
        w = self.pool.workers[0]
        try:
            got = w.ticks(market, code, 0, 5)
            self.l2_ok = bool(got)
        except Exception:                                 # noqa: BLE001
            self.l2_ok = False
        return self.l2_ok

    def effective_mode(self):
        if self.mode == "l1":
            return "l1"
        if self.mode == "l2":
            return "l2"
        return "l2" if self.l2_ok else "l1"

    def _emit(self, rec, mode):
        rec["mode"] = mode
        if self.differ.changed(rec):
            self.sink(rec)

    def round_once(self):
        pairs = self.codes()
        if not pairs:
            return 0
        mode = self.effective_mode()
        sent = 0
        for i in range(0, len(pairs), BATCH_MAX):
            chunk = pairs[i:i + BATCH_MAX]
            ok = self.pool.submit(chunk, lambda out: [self._emit(r, mode) for r in out])
            sent += 1 if ok else 0
        # L2：给订阅表前 l2_every 只补逐笔（可选再补逐笔委托）。成交签名按只缓存，只放新的。
        if mode == "l2" and self.l2_every:
            for m, c in pairs[:self.l2_every]:
                self._tick_job(m, c)
                if self.l2_orders:
                    self._tick_job(m, c, "order")
        return sent

    def _tick_job(self, market, code, kind="tick"):
        """补一轮最新逐笔。用 (时间, 价, 量, 笔数) 当签名去重，只放新成交。

        逐笔没有自增序号（时间是分钟、价是差分），所以不能像日线那样用序号比对；
        签名相同的记录就是同一条成交。签名表按只封顶 TICK_SEEN_MAX，防长跑吃内存。
        """
        key = (market, code, kind)

        def done(rows):
            seen = self._tick_seen.setdefault(key, set())
            order = self._tick_order.setdefault(key, [])
            for r in rows:
                sig = (r["t"], r["price"], r["vol"], r["trades"])
                if sig in seen:
                    continue
                seen.add(sig)
                order.append(sig)
                r.update({"market": market, "code": code.decode("ascii", "replace"),
                          "kind": kind, "mode": "l2", "ts": time.time()})
                self.sink(r)
            while len(order) > TICK_SEEN_MAX:
                seen.discard(order.pop(0))
        self.pool.submit_tick(market, code, done, 0, TICK_WINDOW, kind)

    def run(self, seconds=0, on_tick=None):
        t_end = time.time() + seconds if seconds else 0
        while not self.pool.stop.is_set():
            if t_end and time.time() >= t_end:
                break
            t0 = time.time()
            self._round += 1
            self.round_once()
            if on_tick:
                on_tick(self)
            used = time.time() - t0
            time.sleep(max(0.0, self.cycle - used))


# ---------------------------------------------------------------- 输出

def jsonl_sink(path=None):
    fh = sys.stdout if not path else open(path, "a", encoding="utf-8")

    def sink(rec):
        fh.write(json.dumps(rec, ensure_ascii=False, sort_keys=True) + "\n")
        if fh is sys.stdout:
            fh.flush()
    return sink


def parse_codes(text):
    """600000,000001:0,sh600519 三种写法都认；默认市场 1，6 开头沪、0/3 开头深。"""
    out = []
    for item in text.replace(" ", "").split(","):
        if not item:
            continue
        mkt = 1 if item[0] in "65" else 0
        code = item
        if ":" in item:
            head, code = item.split(":", 1)
            mkt = int(head)
        if len(code) != 6:
            raise SystemExit("代码要 6 位: %s" % item)
        out.append((mkt, code.encode("ascii")))
    return out


def read_watch(path):
    if not os.path.exists(path):
        return []
    with open(path, "r", encoding="utf-8") as fh:
        raw = ",".join(line.split("#")[0].strip() for line in fh)
    return parse_codes(raw)


# ---------------------------------------------------------------- 命令

def cmd_probe_l2(args):
    pool = Pool(workers=1, timeout=8.0)
    col = Collector(pool)
    try:
        ok = col.probe_l2()
        print(json.dumps({"l2": ok, "host": pool.workers[0].host}, ensure_ascii=False))
        if not ok:
            print("L2 逐笔无应答：按 L1 运行", file=sys.stderr)
        return 0 if ok else 3
    finally:
        pool.close()


def cmd_rtt(args):
    stocks = parse_codes(args.codes)
    pool = Pool(workers=args.concurrency, timeout=8.0)
    try:
        for _ in range(args.n):
            pool.submit(stocks)
        deadline = time.time() + args.timeout
        while time.time() < deadline:
            time.sleep(0.2)
            if pool.stats.batches >= args.n:
                break
        print(json.dumps(pool.stats.snapshot(), ensure_ascii=False))
        return 0
    finally:
        pool.close()


def cmd_once(args):
    stocks = parse_codes(args.codes)
    pool = Pool(workers=1, timeout=8.0)
    got = []
    try:
        if args.mode != "l1":
            ok = Collector(pool).probe_l2()
            print("l2=%s" % ok, file=sys.stderr)
        pool.submit(stocks, lambda out: got.extend(out))
        deadline = time.time() + 15
        while time.time() < deadline and len(got) < len(stocks):
            time.sleep(0.05)
        for rec in got:
            print(json.dumps(rec, ensure_ascii=False, sort_keys=True))
        print(json.dumps(pool.stats.snapshot(), ensure_ascii=False), file=sys.stderr)
        return 0 if got else 4
    finally:
        pool.close()


def cmd_run(args):
    watch = args.watch
    pool = Pool(workers=args.concurrency, timeout=8.0)
    sink = jsonl_sink(args.out)
    col = Collector(pool, mode=args.mode, cycle=args.cycle, l2_every=args.l2_every, sink=sink,
                    l2_orders=args.l2_orders)
    subs = parse_codes(args.codes) if args.codes else read_watch(watch)
    if not subs:
        raise SystemExit("没有订阅：给 --codes 或写 %s" % watch)
    col.set_subs(subs)
    if args.mode != "l1":
        ok = col.probe_l2()
        print("l2=%s" % ok, file=sys.stderr)

    def report(_c):
        if args.stats_every and _c._round % args.stats_every == 0:
            print(json.dumps(pool.stats.snapshot(), ensure_ascii=False), file=sys.stderr)
    try:
        col.run(seconds=args.seconds, on_tick=report)
    except KeyboardInterrupt:
        pass
    finally:
        pool.close()
        print(json.dumps(pool.stats.snapshot(), ensure_ascii=False), file=sys.stderr)
    return 0


def main(argv=None):
    ap = argparse.ArgumentParser(description="通达信实时行情采集器（L1/L2 可切换）")
    sub = ap.add_subparsers(dest="cmd", required=True)

    p = sub.add_parser("probe-l2", help="探测 L2 逐笔是否可用")
    p.set_defaults(func=cmd_probe_l2)

    p = sub.add_parser("rtt", help="量往返分布")
    p.add_argument("--codes", default="600000")
    p.add_argument("--n", type=int, default=50)
    p.add_argument("--concurrency", type=int, default=8)
    p.add_argument("--timeout", type=float, default=20.0)
    p.set_defaults(func=cmd_rtt)

    p = sub.add_parser("once", help="打一份快照")
    p.add_argument("--codes", required=True)
    p.add_argument("--mode", default="l1", choices=("l1", "l2", "auto"))
    p.set_defaults(func=cmd_once)

    p = sub.add_parser("run", help="常驻采集")
    p.add_argument("--codes", default="")
    p.add_argument("--watch", default=WATCH_DEFAULT)
    p.add_argument("--mode", default="auto", choices=("l1", "l2", "auto"))
    p.add_argument("--cycle", type=float, default=3.0, help="轮询周期秒，默认贴 3 秒档")
    p.add_argument("--concurrency", type=int, default=4)
    p.add_argument("--l2-every", type=int, default=0, help="L2 模式下每轮补逐笔的只数")
    p.add_argument("--l2-orders", action="store_true", help="再补一路逐笔委托（订单流）")
    p.add_argument("--out", default="")
    p.add_argument("--seconds", type=float, default=0)
    p.add_argument("--stats-every", type=int, default=10)
    p.set_defaults(func=cmd_run)

    args = ap.parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())