# -*- coding: utf-8 -*-
"""tdx_dash.py —— 行情实盘看板：本地 HTTP 服务，把 tdx_realtime 的取数喂给一个单页。

只给演示用，绑 127.0.0.1，标准库 http.server。上游按代码缓存 0.9 秒，
浏览器 1 秒一刷也不会把主站打爆；接口只有三个：

    GET /                       单页 dash.html
    GET /api/snap?codes=1:600036 一份 L1 快照（五档、价量额）
    GET /api/ticks?code=1:600036 最新逐笔 200 条（0x0FC2 带秒）

用法：python tdx_dash.py --port 8811
"""
import argparse
import json
import os
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import tdx_realtime as R

HERE = os.path.dirname(os.path.abspath(__file__))
PAGE = os.path.join(HERE, "dash.html")
CACHE_TTL = 0.9
TICK_TTL = 0.9

_lock = threading.Lock()
_cache = {}
_pool = None
_err = {"n": 0, "refreshing": False}


def parse_one(text):
    pairs = R.parse_codes(text)
    return pairs[0] if pairs else None


def cached(key, ttl, fn):
    now = time.time()
    with _lock:
        hit = _cache.get(key)
        if hit and now - hit[0] < ttl:
            return hit[1], hit[2]          # 命中缓存也回上次的真实上游耗时，别显示 0
    t0 = time.time()
    val = fn()
    ms = (time.time() - t0) * 1000.0
    with _lock:
        _cache[key] = (time.time(), val, ms)   # 上游耗时一并缓存，命中时照样能报
    return val, ms


def do_snap(codes):
    pairs = R.parse_codes(codes)
    w = _pool.workers[0]
    payload = R.SNAP_PREFIX + R.struct.pack("<H", len(pairs)) + b"".join(
        R.struct.pack("<B6s", m, c) for m, c in pairs) + R.SNAP_TAIL
    body = w._call(R.CT_SNAPSHOT, payload, R.SNAP_CAT, R.SNAP_VER, R.SNAP_FLAG)
    w._drop()
    recs = R.parse_snapshot(body, pairs)
    keep = ("code", "market", "price", "last_close", "open", "high", "low", "vol", "cur_vol",
            "amount", "b_vol", "s_vol", "bid1", "ask1", "bid_vol1", "ask_vol1", "bid2", "ask2",
            "bid_vol2", "ask_vol2", "bid3", "ask3", "bid_vol3", "ask_vol3", "bid4", "ask4",
            "bid_vol4", "ask_vol4", "bid5", "ask5", "bid_vol5", "ask_vol5", "mismatch")
    return [{k: r.get(k) for k in keep if k in r} for r in recs]


def do_ticks(code):
    m, c = parse_one(code)
    w = _pool.workers[0]
    try:
        rows = w.orders(m, c, 0, 200)
    finally:
        w._drop()
    keep = ("t", "sec", "price", "vol", "trades", "side")
    return [{k: r.get(k) for k in keep} for r in rows]


def note_error():
    """连着出错说明主机表里坏机器占多了，去重探一遍，别让长跑的进程抱着死表不放。"""
    with _lock:
        _err["n"] += 1
        need = _err["n"] >= 5 and not _err["refreshing"]
        if need:
            _err["n"] = 0
            _err["refreshing"] = True

    def job():
        try:
            _pool.health()
        finally:
            with _lock:
                _err["refreshing"] = False
    if need:
        threading.Thread(target=job, daemon=True).start()


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *a):
        pass

    def _send(self, code, body, ctype):
        data = body if isinstance(body, bytes) else body.encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(data)))
        self.send_header("Cache-Control", "no-store")
        self.send_header("Access-Control-Allow-Origin", "*")   # 页面可能从 file:// 打开
        self.end_headers()
        self.wfile.write(data)

    def do_OPTIONS(self):
        self.send_response(204)
        self.send_header("Access-Control-Allow-Origin", "*")
        self.send_header("Access-Control-Allow-Methods", "GET, OPTIONS")
        self.send_header("Content-Length", "0")
        self.end_headers()

    def do_GET(self):
        u = urlparse(self.path)
        q = parse_qs(u.query)
        try:
            if u.path in ("/", "/index.html"):
                with open(PAGE, "rb") as fh:
                    self._send(200, fh.read(), "text/html; charset=utf-8")
            elif u.path == "/api/snap":
                codes = (q.get("codes") or ["1:600036"])[0]
                data, ms = cached("snap:" + codes, CACHE_TTL, lambda: do_snap(codes))
                self._send(200, json.dumps({"quotes": data, "latency_ms": round(ms, 1)},
                                           ensure_ascii=False), "application/json; charset=utf-8")
            elif u.path == "/api/ticks":
                code = (q.get("code") or ["1:600036"])[0]
                data, _ms = cached("tick:" + code, TICK_TTL, lambda: do_ticks(code))
                self._send(200, json.dumps({"ticks": data, "l2": True}, ensure_ascii=False),
                           "application/json; charset=utf-8")
            else:
                self._send(404, "not found", "text/plain; charset=utf-8")
        except Exception as exc:                                  # noqa: BLE001
            note_error()
            self._send(200, json.dumps({"error": "%s: %s" % (type(exc).__name__, exc)},
                                       ensure_ascii=False), "application/json; charset=utf-8")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=8811)
    ap.add_argument("--host", default="127.0.0.1")
    args = ap.parse_args()
    global _pool
    _pool = R.Pool(workers=2, timeout=8.0)
    srv = ThreadingHTTPServer((args.host, args.port), Handler)
    srv.daemon_threads = True
    print("看板: http://%s:%d/" % (args.host, args.port), flush=True)
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        _pool.close()
        srv.server_close()


if __name__ == "__main__":
    main()