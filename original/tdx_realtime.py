# -*- coding: utf-8 -*-
"""tdx_realtime.py —— 通达信行情实时采集器（L1 快照 / L2 逐笔，可切换）。

设计要点（为什么这么写）：

1. 本服每连接只服务一次业务请求，所以"低延迟"靠的是**连接池 + 流水线**：一条连接连发多批，
   把一次 RTT 摊到几十只上；退避靠失败率，不靠固定 sleep。
2. 盘口是**拉**：轮询节奏跟着交易所档位走（L1 三秒一档），压到更短只是抢相位。
3. 全链路只用标准库；连接、解析、差分、输出四层分开，便于替换。

安全边界：只读网络，不碰安装目录；主机表来自 connect.cfg（spec.json 可现造）。
帧、握手、记录编解码与价格分母全在 tdx_client，这里只负责池化、调度、差分与落盘。

用法：
    python tdx_realtime.py probe-l2                        # 探测本服是否给 L2
    python tdx_realtime.py rtt --codes 600000,600519       # 量往返分布
    python tdx_realtime.py once --codes 600000 --mode l2   # 打一份快照
    python tdx_realtime.py run --watch watch.txt --cycle 3 --mode auto --out q.jsonl
"""
import argparse
import io
import collections
import json
import os
import queue
import random
import struct
import sys
import threading
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import tdx_client as T                                   # 复用配置、主机表与帧头结构

# ---------------------------------------------------------------- L2 命令


def _dedupe_sort(rows):
    seen = set()
    out = []
    for r in rows:
        sig = tick_sig(r)
        if sig in seen:
            continue
        seen.add(sig)
        out.append(r)
    out.sort(key=_tick_order_key)
    return out


def cmd_l2_fetch(args):
    """往回翻历史逐笔，整段合并去重后按时间正序写文件或打屏幕。"""
    stocks = parse_codes(args.codes)
    pool = Pool(workers=min(4, len(stocks) or 1), timeout=8.0)
    store = None
    if args.out:
        store = TickStore(args.out, args.day, args.kind)
    total = 0
    try:
        for m, c in stocks:
            rows = []
            start = args.start
            while len(rows) < args.count:
                want = min(args.count - len(rows), args.window)
                got = pool.workers[0].ticks(m, c, start, want) if args.kind == "tick" else \
                    pool.workers[0].orders(m, c, start, want)
                pool.workers[0]._drop()
                if not got:
                    break
                rows.extend(got)
                start += len(got)
            rows = _dedupe_sort(rows)
            for r in rows:
                r.update({"market": m, "code": c.decode("ascii"), "kind": args.kind,
                          "mode": "l2", "ts": time.time()})
                if store:
                    store.write(r)
                else:
                    print(json.dumps(r, ensure_ascii=False, sort_keys=True))
            total += len(rows)
            print("%d%s 取回 %d 条" % (m, c.decode("ascii"), len(rows)), file=sys.stderr)
        return 0 if total else 4
    finally:
        if store:
            store.close()
        pool.close()


def cmd_l2_tail(args):
    """实时增量：只落新成交，断点续采靠文件末尾签名，进程重启不重不漏。"""
    stocks = parse_codes(args.codes)
    store = TickStore(args.out, args.day, args.kind)
    n = max(1, min(args.concurrency, len(stocks)))
    pool = Pool(workers=n, timeout=8.0)
    tails = {}
    for i, (m, c) in enumerate(stocks):
        seen, order = store.seed_seen(m, c)
        tails[(m, c)] = TickTail(pool.workers[i % n], m, c, seen, order,
                                 args.window, args.kind)
    t_end = time.time() + args.seconds
    written = 0
    try:
        while time.time() < t_end:
            for (m, c), tail in tails.items():
                for r in tail.poll():
                    r.update({"market": m, "code": c.decode("ascii"), "kind": args.kind,
                              "mode": "l2", "ts": time.time()})
                    store.write(r)
                    written += 1
            store.flush()
            time.sleep(max(0.0, args.interval))
        print(json.dumps({"written": written, "codes": len(stocks),
                          "seconds": args.seconds}, ensure_ascii=False))
        return 0
    finally:
        store.close()
        pool.close()


def cmd_l2_replay(args):
    """把某天某只的逐笔文件按时间正序回放；--speed 0 全速，否则按 sec 原速。"""
    store = TickStore(args.out, args.day, args.kind)
    p = store.path(args.market, args.code.encode("ascii"))
    if not os.path.exists(p):
        print("没有文件: %s" % p, file=sys.stderr)
        return 4
    prev = None
    with open(p, "r", encoding="utf-8") as fh:
        for line in fh:
            rec = json.loads(line)
            if args.speed and prev:
                gap = _tick_order_key(rec)[1] - prev
                if gap > 0:
                    time.sleep(min(gap, 60) / float(args.speed))
            prev = _tick_order_key(rec)[1]
            print(json.dumps(rec, ensure_ascii=False, sort_keys=True))
    return 0


def cmd_l2_check(args):
    """逐笔文件的量核对。盘中文件只覆盖一段，就按同一时间窗对在线回包比；整日文件对日线比。

    两侧都换算成手，闸值默认 0.5%。任一判据过就算通过：盘中段必然比整日少，
    所以文件末笔离现在很远时，日线那条只作参考，不作判据。
    """
    store = TickStore(args.out, args.day, args.kind)
    p = store.path(args.market, args.code.encode("ascii"))
    if not os.path.exists(p):
        print("没有文件: %s" % p, file=sys.stderr)
        return 4
    recs = [json.loads(l) for l in io.open(p, encoding="utf-8")]
    if not recs:
        print("文件是空的", file=sys.stderr)
        return 4
    recs.sort(key=_tick_order_key)
    lo, hi = _tick_order_key(recs[0]), _tick_order_key(recs[-1])
    file_sum = sum(r["vol"] for r in recs)

    pool = Pool(workers=1, timeout=8.0)
    try:
        w = pool.workers[0]
        try:
            live = w.orders(args.market, args.code.encode("ascii"), 0, 800)
        finally:
            w._drop()
    finally:
        pool.close()

    # 在线回包必须完全盖住文件的时间窗，这段才可比
    live_lo, live_hi = _tick_order_key(min(live, key=_tick_order_key)),         _tick_order_key(max(live, key=_tick_order_key)) if live else (lo, lo)
    live_cover = bool(live) and live_lo <= hi and live_hi >= lo
    inter_lo, inter_hi = max(lo, live_lo), min(hi, live_hi)
    covered = sum(1 for r in recs if inter_lo <= _tick_order_key(r) <= inter_hi)
    if live_cover and covered < 0.9 * len(recs):
        live_cover = False                    # 只盖住一小段就不算可比
    live_sum = sum(r["vol"] for r in live if inter_lo <= _tick_order_key(r) <= inter_hi)
    file_sum = sum(r["vol"] for r in recs if inter_lo <= _tick_order_key(r) <= inter_hi)

    try:
        base = day_volume(args.market, args.code.encode("ascii"))
    except Exception as exc:                              # noqa: BLE001
        base = 0.0
        print("日线取不到: %s" % exc, file=sys.stderr)

    out = {"条数": len(recs), "文件量": file_sum, "起": lo[0], "止": hi[0], "覆盖": live_cover}
    ok = False
    if live_cover and live_sum:
        rate = abs(file_sum - live_sum) / live_sum
        out.update({"窗内在线量": live_sum, "窗内偏差": round(rate, 6), "窗内通过": rate <= args.tolerance})
        ok = ok or rate <= args.tolerance
    if base:
        rate = abs(file_sum - base) / base
        out.update({"日线量": base, "日线偏差": round(rate, 6), "日线通过": rate <= args.tolerance})
        ok = ok or rate <= args.tolerance
    out["通过"] = ok
    print(json.dumps(out, ensure_ascii=False))
    return 0 if ok else 1


BATCH_MAX = 80            # 单请求品种数上限（56 字节/只，回包约 4.5 KB）
TICK_WINDOW = 50          # 采集器内轮询用的逐笔条数：客户端成交明细一次就要 115 条，50 够一轮不漏
TICK_WINDOW_FRESH = 200   # 增量尾随用的窗口：活跃票一秒能出几十笔，窗口给足才不漏
TICK_SEEN_MAX = 4000      # 每只记住的已见签名上限
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
        self.retries = 0      # 换机重试次数，失败主机被挪队尾后计数
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


# ---------------------------------------------------------------- 连接工人

class Worker(threading.Thread):
    """一个工人 = 一条客户端连接 + 一条任务队列。

    协议、握手、帧全在 tdx_client；这里只管排队、退避与主机簿记。
    出错就地换机重连，退避 0.2s 起翻倍、封顶 5s。
    """

    # ---- 连接与取数：协议全在 tdx_client，这里只留池子侧的簿记 ----
    def __init__(self, pool, idx):
        threading.Thread.__init__(self, daemon=True)
        self.pool = pool
        self.idx = idx
        self.q = queue.Queue()
        self.stop = pool.stop
        self.backoff = 0.2
        self.host = None
        self.conn = T.TdxConnection(hosts=pool.hosts(), timeout=pool.timeout,
                                    on_ban=self._on_ban)

    def _on_ban(self, host):
        """客户端换了台主机，池子这把表也挪一下队尾。"""
        self.host = host
        self.pool.ban(host)

    def _drop(self):
        self.conn._close()

    # 委派给客户端，保持原来的调用名
    def snapshot(self, stocks):
        t0 = time.time()
        out = self.conn.snapshot(stocks)
        self.pool.stats.add_rtt((time.time() - t0) * 1000)
        self.host = self.conn.host
        return out

    def ticks(self, market, code, start=0, count=30):
        t0 = time.time()
        out = self.conn.ticks(market, code, start, count)
        self.pool.stats.add_rtt((time.time() - t0) * 1000)
        self.host = self.conn.host
        return out

    def orders(self, market, code, start=0, count=30):
        t0 = time.time()
        out = self.conn.orders(market, code, start, count)
        self.pool.stats.add_rtt((time.time() - t0) * 1000)
        self.host = self.conn.host
        return out

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

    def ban(self, host, keep_last=1):
        """把刚失败的主机挪到队尾，别让它一直占着头位。留最后一台，全不符时还能兜底。"""
        for i, (h, _p) in enumerate(self._hosts):
            if h == host and i < len(self._hosts) - keep_last:
                self._hosts.append(self._hosts.pop(i))
                return

    def _probe(self, host, port, timeout, out):
        """单机健康探测：协议全交给客户端，只把耗时收进来。"""
        t0 = time.time()
        try:
            T.TdxConnection(hosts=[(host, port)], timeout=timeout).probe()
            out.append((round((time.time() - t0) * 1000, 1), host, port))
        except Exception:                                 # noqa: BLE001
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


def guess_market(code):
    """按代码前缀判市场：沪深两边都有 5 与 1 开头的品种，只看首字符会串市场。

    沪：6/9 开头（股票、B 股）、5 开头（50-58 的 ETF 与 LOF）、11/13 开头（转债、可交换债）
    深：0/2/3 开头（股票、B 股）、12/15/16/17/18 开头（转债、ETF、LOF、权证）
    北交所是 8 开头与 920 号段（920 先判），调用方可用 市场:代码 显式指定。
    """
    if not code:
        return 1
    head = code[0]
    if code[:2] == "92":
        return 2                      # 北交所 920 号段先于 9 开头（9 开头别的号段是沪市 B 股）
    if head in "69":
        return 1
    if head in "023":
        return 0
    if head == "5":
        return 1                      # 50-58 全是沪市基金，深市 ETF 是 15 开头
    if head == "8":
        return 2                      # 北交所 8 开头
    if head == "1":
        return 1 if code[:2] in ("11", "13") else 0
    return 1


def parse_codes(text):
    """600000、000001:0、sh600519 三种写法都认；市场按 guess_market 判，冒号可覆盖。"""
    out = []
    for item in text.replace(" ", "").split(","):
        if not item:
            continue
        code = item
        mkt = guess_market(item)
        if item.startswith(("sh", "sz", "bj")) and len(item) == 8:
            mkt = {"sh": 1, "sz": 0, "bj": 2}[item[:2]]
            code = item[2:]
        if ":" in item:
            head, code = item.split(":", 1)
            mkt = int(head)
            if guess_market(code) != mkt:
                print("提示: %s 按前缀看像市场 %d，这里按你写的市场 %d 发"
                      % (code, guess_market(code), mkt), file=sys.stderr)
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


# ---------------------------------------------------------------- 逐笔存储

TICK_SIG = ("t", "price", "vol", "trades")


def tick_sig(rec):
    """逐笔去重签名：时间是 3 秒精度，加上价、量、笔数就足够唯一。"""
    return tuple(rec.get(k) for k in TICK_SIG)


def _tick_order_key(rec):
    return (rec.get("t") or "", rec.get("sec") or 0, rec.get("price") or 0.0)


class TickStore(object):
    """按交易日、按只落盘：<根>/YYYYMMDD/<市场><代码>.jsonl。

    开文件时把最后一条签名读回来当续采锚点，重启后不重不漏。
    kind 也进文件名后缀，成交与委托两路不会互串。
    """

    def __init__(self, root, day=None, kind="order"):
        self.root = root
        self.day = day or time.strftime("%Y%m%d")
        self.dir = os.path.join(root, self.day)
        os.makedirs(self.dir, exist_ok=True)
        self.kind = kind
        self.fh = {}
        self.anchor = {}

    def path(self, market, code):
        code = code.decode("ascii", "replace") if isinstance(code, bytes) else code
        return os.path.join(self.dir, "%d%s.%s.jsonl" % (market, code, self.kind))

    def open(self, market, code):
        """取追加句柄与续采锚点；锚点来自文件最后一行，没有就 None。"""
        key = (market, code)
        if key in self.fh:
            return self.fh[key], self.anchor.get(key)
        p = self.path(market, code)
        anchor = None
        if os.path.exists(p) and os.path.getsize(p):
            with open(p, "rb") as fh:
                fh.seek(max(0, os.path.getsize(p) - 4096))
                last = fh.read().decode("utf-8", "replace").strip().splitlines()[-1:]
            if last:
                try:
                    anchor = tick_sig(json.loads(last[0]))
                except ValueError:
                    anchor = None
        self.anchor[key] = anchor
        self.fh[key] = open(p, "a", encoding="utf-8")
        return self.fh[key], anchor

    def write(self, rec):
        key = (rec["market"], rec["code"])
        fh, anchor = self.open(key[0], key[1].encode("ascii"))
        fh.write(json.dumps(rec, ensure_ascii=False, sort_keys=True) + "\n")
        self.anchor[key] = tick_sig(rec)
        return fh

    def seed_seen(self, market, code, limit=500):
        """从文件尾部回种已见签名：进程重启后接着采，不把老成交当成新的重写一遍。"""
        seen = set()
        order = collections.deque()
        p = self.path(market, code)
        if not os.path.exists(p) or not os.path.getsize(p):
            return seen, order
        with open(p, "rb") as fh:
            fh.seek(max(0, os.path.getsize(p) - limit * 96))
            rows = fh.read().decode("utf-8", "replace").strip().splitlines()[-limit:]
        for line in rows:
            try:
                sig = tick_sig(json.loads(line))
            except ValueError:
                continue
            seen.add(sig)
            order.append(sig)
        return seen, order

    def flush(self):
        for fh in self.fh.values():
            fh.flush()

    def close(self):
        for fh in self.fh.values():
            try:
                fh.close()
            except OSError:
                pass
        self.fh = {}


class TickTail(object):
    """增量：每次从最新处取一窗，跳过已经见过的那一段，正序返回剩下的新记录。

    逐笔的签名会重复（同一秒里同价同量的两笔成交长得一样），所以不能只拿
    一个"上一条"去比对，必须用已见签名集合从最新那头往回扫：
    扫到集合里没有的签名就停，它之后的全是新的。整窗都见过就是没有新成交。
    集合按只封顶 TICK_SEEN_MAX，防长跑吃内存。
    """

    def __init__(self, worker, market, code, seen=None, order=None,
                 window=TICK_WINDOW_FRESH, kind="order"):
        self.worker = worker
        self.market = market
        self.code = code
        self.seen = seen if seen is not None else set()
        self.order = order if order is not None else collections.deque()
        self.window = window
        self.kind = kind

    def remember(self, sig):
        if sig in self.seen:
            return
        self.seen.add(sig)
        self.order.append(sig)
        while len(self.order) > TICK_SEEN_MAX:
            self.seen.discard(self.order.popleft())

    def poll(self):
        fn = self.worker.ticks if self.kind == "tick" else self.worker.orders
        try:
            rows = fn(self.market, self.code, 0, self.window)
        finally:
            self.worker._drop()
        if not rows:
            return []
        keep = []
        for r in rows:                       # 服务端从最新往回给
            if tick_sig(r) in self.seen:
                continue                     # 见过的整段跳过
            keep.append(r)
        if not keep:
            return []
        keep.reverse()                       # 变成时间正序
        batch = []
        local = set()
        for r in keep:
            sig = tick_sig(r)
            if sig in local:
                continue
            local.add(sig)
            batch.append(r)
        batch.sort(key=_tick_order_key)
        for r in batch:
            self.remember(tick_sig(r))
        return batch


def day_volume(market, code):
    """当日日线成交量（手），用于整日量核对。code 给 bytes 或 str 都行。"""
    if isinstance(code, bytes):
        code = code.decode("ascii")
    cli = T.TdxConnection()                       # 参数顺序是 category、market、code
    try:
        bars = cli.security_bars(4, market, code, 0, 1)      # 4 是日线，只要最新一根
    finally:
        cli._close()
    # 日线按日期倒序给（0 是最新），量是股，逐笔是手（11-04 的 1347238 手对死过单位）
    return bars[0]["vol"] / 100.0 if bars else 0.0


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


def snapshot_best(pool, stocks):
    """取快照，价格全零的按前缀改判市场重取一次（常见于把 589020 写成 0:589020）。

    服务端对错市场的代码照样回记录，只是价量额全零，很容易被当成"没数据"。
    改判只在显式市场与 guess_market 不一致且首轮全零时发生，改判命中的记录带 remapped。
    """
    out = [{}] * len(stocks)
    w = pool.workers[0]
    try:
        out = w.snapshot(stocks)
    finally:
        w._drop()
    retry = [(i, s) for i, s in enumerate(stocks) if not out[i].get("price")]
    if not retry:
        return out
    fixed = []
    for i, (m, c) in retry:
        guess = guess_market(c.decode("ascii", "replace"))
        if guess == m:
            continue
        try:
            got = w.snapshot([(guess, c)])[0]
        except Exception:                              # noqa: BLE001
            continue
        finally:
            w._drop()
        if got.get("price"):
            got["remapped"] = guess
            out[i] = got
            fixed.append((c.decode("ascii", "replace"), m, guess))
    for code, was, now in fixed:
        print("提示: %s 在市场 %d 全零，按前缀改判市场 %d 后取到" % (code, was, now),
              file=sys.stderr)
    return out


def cmd_once(args):
    stocks = parse_codes(args.codes)
    pool = Pool(workers=1, timeout=8.0)
    got = []
    try:
        if args.mode != "l1":
            ok = Collector(pool).probe_l2()
            print("l2=%s" % ok, file=sys.stderr)
        got = snapshot_best(pool, stocks)
        for rec in got:
            print(json.dumps(rec, ensure_ascii=False, sort_keys=True))
        if args.l2_orders:                        # 逐笔只在要的时候打，默认不打
            w = pool.workers[0]
            w._drop()                             # 快照那条连接已用掉，逐笔要重连
            for m, c in stocks:
                for kind in ("tick", "order"):
                    for rec in (w.ticks if kind == "tick" else w.orders)(m, c if isinstance(c, bytes) else c.encode(), 0, 20):
                        rec["kind"] = kind
                        print(json.dumps(rec, ensure_ascii=False, sort_keys=True))
                    w._drop()
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

    p = sub.add_parser("l2", help="逐笔取数、增量落盘、回放与核对")
    l2sub = p.add_subparsers(dest="l2cmd", required=True)
    q = l2sub.add_parser("fetch", help="往回翻历史逐笔，正序落盘")
    q.add_argument("--codes", required=True)
    q.add_argument("--count", type=int, default=300, help="要取的条数上限")
    q.add_argument("--start", type=int, default=0, help="从最新往回数第几条开始，默认 0")
    q.add_argument("--window", type=int, default=800, help="单次回包上限，默认 800")
    q.add_argument("--kind", default="order", choices=("order", "tick"))
    q.add_argument("--out", default="")
    q.add_argument("--day", default="")
    q.set_defaults(func=cmd_l2_fetch)
    q = l2sub.add_parser("tail", help="实时增量落盘，断点续采")
    q.add_argument("--codes", required=True)
    q.add_argument("--seconds", type=float, default=60)
    q.add_argument("--interval", type=float, default=1.0, help="两轮间隔秒，默认 1")
    q.add_argument("--window", type=int, default=TICK_WINDOW_FRESH)
    q.add_argument("--concurrency", type=int, default=4)
    q.add_argument("--kind", default="order", choices=("order", "tick"))
    q.add_argument("--day", default="")
    q.add_argument("--out", required=True)
    q.set_defaults(func=cmd_l2_tail)
    q = l2sub.add_parser("replay", help="按时间正序回放某天的文件")
    q.add_argument("--market", type=int, required=True)
    q.add_argument("--code", required=True)
    q.add_argument("--day", default="")
    q.add_argument("--kind", default="order", choices=("order", "tick"))
    q.add_argument("--speed", type=float, default=0, help="0 全速，1 按秒原速，60 则快 60 倍")
    q.add_argument("--out", required=True)
    q.set_defaults(func=cmd_l2_replay)
    q = l2sub.add_parser("check", help="文件量合计对日线，闸值 0.5%%")
    q.add_argument("--market", type=int, required=True)
    q.add_argument("--code", required=True)
    q.add_argument("--day", default="")
    q.add_argument("--kind", default="order", choices=("order", "tick"))
    q.add_argument("--tolerance", type=float, default=0.005)
    q.add_argument("--out", required=True)
    q.set_defaults(func=cmd_l2_check)

    p = sub.add_parser("once", help="打一份快照")
    p.add_argument("--codes", required=True)
    p.add_argument("--mode", default="l1", choices=("l1", "l2", "auto"))
    p.add_argument("--l2-orders", action="store_true", help="再打一份带秒的逐笔成交（0FC2）")
    p.set_defaults(func=cmd_once)

    p = sub.add_parser("run", help="常驻采集")
    p.add_argument("--codes", default="")
    p.add_argument("--watch", default=WATCH_DEFAULT)
    p.add_argument("--mode", default="auto", choices=("l1", "l2", "auto"))
    p.add_argument("--cycle", type=float, default=3.0, help="轮询周期秒，默认贴 3 秒档")
    p.add_argument("--concurrency", type=int, default=4)
    p.add_argument("--l2-every", type=int, default=0, help="L2 模式下每轮补逐笔的只数")
    p.add_argument("--l2-orders", action="store_true", help="再补一路带秒的逐笔成交（0FC2，时间到 3 秒）")
    p.add_argument("--out", default="")
    p.add_argument("--seconds", type=float, default=0)
    p.add_argument("--stats-every", type=int, default=10)
    p.set_defaults(func=cmd_run)

    args = ap.parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())