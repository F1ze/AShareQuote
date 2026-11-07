"""Official BaoStock SDK adapter: sequential anonymous session, bounded sockets.

This process only reads public data. It cannot access or mutate the warehouse.
"""
import base64
import contextlib
import datetime as dt
import json
import os
import socket
import struct
import sys
import time
import urllib.parse

sys.stdin.reconfigure(encoding="utf-8")
sys.stdout.reconfigure(encoding="utf-8")
socket.setdefaulttimeout(15)

class CheckedSocket(socket.socket):
    """The SDK's recv loop does not detect EOF; fail instead of spinning forever."""
    def recv(self, *args, **kwargs):
        data = super().recv(*args, **kwargs)
        if not data:
            raise ConnectionError("BaoStock 服务器关闭了连接，请稍后重试")
        return data


def _recv_exact(sock, size):
    """Read exactly size bytes; CheckedSocket turns EOF into a hard failure."""
    data = b""
    while len(data) < size:
        chunk = sock.recv(size - len(data))
        if not chunk:
            raise ConnectionError("代理在握手完成前关闭了连接")
        data += chunk
    return data


def _socks5_handshake(sock, target_host, target_port, username=None, password=None):
    """RFC 1928 CONNECT with optional username/password authentication."""
    methods = b"\x05\x02\x00\x02" if username else b"\x05\x01\x00"
    sock.sendall(methods)
    reply = _recv_exact(sock, 2)
    if reply[0] != 5:
        raise ConnectionError("代理应答不是 SOCKS5 协议")
    method = reply[1]
    if method == 0x02:
        if not username:
            raise ConnectionError("代理要求认证，但未配置用户名密码")
        user = username.encode()
        secret = (password or "").encode()
        sock.sendall(b"\x01" + bytes([len(user)]) + user + bytes([len(secret)]) + secret)
        auth = _recv_exact(sock, 2)
        if auth[1] != 0:
            raise ConnectionError("代理认证失败")
    elif method != 0x00:
        raise ConnectionError("代理拒绝了全部可用的认证方式")
    host = target_host.encode()
    if len(host) > 255:
        raise ValueError("目标主机名超出 SOCKS5 长度限制")
    sock.sendall(b"\x05\x01\x00\x03" + bytes([len(host)]) + host + struct.pack(">H", target_port))
    head = _recv_exact(sock, 4)
    if head[1] != 0:
        raise ConnectionError(f"代理连接失败，状态码 {head[1]}")
    length = {1: 4, 4: 16}.get(head[3])
    if length is None:
        length = _recv_exact(sock, 1)[0]
    _recv_exact(sock, length + 2)


def _http_connect(sock, target, auth_header=""):
    """Open an HTTP CONNECT tunnel to host:port through the proxy."""
    lines = [f"CONNECT {target} HTTP/1.1", f"Host: {target}"]
    if auth_header:
        lines.append(f"Proxy-Authorization: {auth_header}")
    sock.sendall(("\r\n".join(lines) + "\r\n\r\n").encode("ascii"))
    response = b""
    while b"\r\n\r\n" not in response:
        chunk = sock.recv(4096)
        if not chunk:
            raise ConnectionError("代理在 CONNECT 应答前关闭了连接")
        response += chunk
        if len(response) > 8192:
            raise ConnectionError("代理 CONNECT 应答头过大")
    status = response.split(b"\r\n", 1)[0].decode("latin-1", "replace")
    parts = status.split()
    if len(parts) < 2 or parts[1] != "200":
        raise ConnectionError(f"代理 CONNECT 失败：{status}")


def resolve_proxy_url():
    """Resolve the outbound proxy following the ALL_PROXY → HTTPS_PROXY → HTTP_PROXY convention."""
    for key in ("ALL_PROXY", "all_proxy", "HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"):
        value = os.environ.get(key, "").strip()
        if value:
            return value
    return ""


def build_socket_class(proxy_url=""):
    """Return the socket.socket replacement; a proxy URL yields a tunneled socket."""
    proxy_url = (proxy_url or "").strip()
    if not proxy_url:
        return CheckedSocket
    parsed = urllib.parse.urlsplit(proxy_url)
    scheme = (parsed.scheme or "").lower()
    default_ports = {"socks5": 1080, "socks5h": 1080, "http": 80, "https": 443}
    if scheme not in default_ports:
        raise ValueError("代理协议须为 socks5、http 或 https")
    if not parsed.hostname:
        raise ValueError("代理地址缺少主机名")
    proxy_host = parsed.hostname
    proxy_port = parsed.port or default_ports[scheme]
    username = urllib.parse.unquote(parsed.username) if parsed.username else None
    password = urllib.parse.unquote(parsed.password) if parsed.password else ""
    auth_header = ""
    if username:
        credentials = base64.b64encode(f"{username}:{password}".encode()).decode()
        auth_header = "Basic " + credentials

    class ProxySocket(CheckedSocket):
        """All connections are established through the configured proxy tunnel."""
        def connect(self, address):
            if not isinstance(address, tuple) or len(address) != 2:
                raise ValueError("代理套接字需要 (host, port) 形式的目标地址")
            target_host, target_port = address
            super().connect((proxy_host, proxy_port))
            if scheme in ("socks5", "socks5h"):
                # The handshake below already delegates hostname resolution to the proxy.
                _socks5_handshake(self, target_host, int(target_port), username, password)
            else:
                _http_connect(self, f"{target_host}:{int(target_port)}", auth_header)

    return ProxySocket


bs = None
socketutil = None
sdk_send = None
sdk_missing = False
try:
    import baostock as bs
    import baostock.util.socketutil as socketutil
    sdk_send = socketutil.send_msg
except ImportError:
    # Defer the error to main(): importing this module for tests must not exit.
    sdk_missing = True

last_wire_request = 0.0

def checked_send(message):
    global last_wire_request
    time.sleep(max(0, 0.15 - (time.monotonic() - last_wire_request)))
    last_wire_request = time.monotonic()
    result = sdk_send(message)
    # next() otherwise treats a transport failure as a normal end of history.
    if result is None or not result.strip():
        raise ConnectionError("BaoStock 传输中断，未将部分分页当成完整结果保存")
    return result

if socketutil is not None:
    socketutil.send_msg = checked_send

logged_in = False
last_request = 0.0


def collect(result):
    if result is None:
        raise ConnectionError("BaoStock 未返回有效结果")
    if result.error_code != "0":
        raise ValueError(f"BaoStock {result.error_code}: {result.error_msg}")
    rows = []
    pages = set()
    while result.next():
        if getattr(result, "cur_row_num", -1) == 0:
            page = str(result.cur_page_num)
            if page in pages:
                raise ValueError("BaoStock 重复返回同一页，拒绝保存不完整历史")
            pages.add(page)
        if result.error_code != "0":
            raise ValueError(f"BaoStock 分页失败: {result.error_msg}")
        rows.append(dict(zip(result.fields, result.get_row_data())))
    if result.error_code != "0":
        raise ValueError(f"BaoStock 分页失败: {result.error_msg}")
    return rows


def handle(request):
    global logged_in, last_request
    time.sleep(max(0, 0.3 - (time.monotonic() - last_request)))
    last_request = time.monotonic()
    if not logged_in:
        login = bs.login()
        if login.error_code != "0":
            raise ValueError(f"BaoStock 登录失败: {login.error_msg}")
        logged_in = True
    op = request["op"]
    if op == "universe":
        markets = request["markets"]
        if any(m not in (0, 1) for m in markets):
            raise ValueError("BaoStock 适配器仅提供沪深市场，请用通达信采集北交所")
        rows = collect(bs.query_stock_basic())
        result = []
        for row in rows:
            prefix, code = row["code"].split(".", 1)
            market = {"sz": 0, "sh": 1}.get(prefix)
            if market not in markets or row["type"] != "1":
                continue
            result.append({"market": market, "code": code, "name": row["code_name"],
                "listingDate": row["ipoDate"], "delistingDate": row["outDate"],
                "status": "listed" if row["status"] == "1" else "delisted",
                "referenceSource": "baostock"})
        return result
    if op == "calendar":
        rows = collect(bs.query_trade_dates(request["from"], request["to"]))
        # BaoStock calendar is applied only to the exchanges supported by this source.
        return [{"exchange": exchange, "date": row["calendar_date"], "open": row["is_trading_day"] == "1", "source": "baostock"} for row in rows for exchange in ("SH", "SZ")]
    if op == "bars":
        security = request["security"]
        prefix = {0: "sz", 1: "sh"}.get(security["market"])
        frequency = {0: "5", 1: "15", 2: "30", 3: "60", 4: "d", 5: "w", 6: "m", 9: "d"}.get(request["category"])
        if not prefix or not frequency:
            raise ValueError("该市场或周期不在 BaoStock 支持范围内，1 分钟/北交所请用通达信")
        fields = "date,open,high,low,close,volume,amount"
        minute = frequency.isdigit()
        if minute:
            fields = "date,time,open,high,low,close,volume,amount"
        rows = collect(bs.query_history_k_data_plus(f'{prefix}.{security["code"]}', fields,
            start_date=request["from"], end_date=request["to"], frequency=frequency, adjustflag="3"))
        result = []
        for row in rows:
            if not row["open"] or not row["close"]:
                raise ValueError("返回了无效/空价格，拒绝静默丢弃行情")
            date = row["date"]
            if minute:
                date = dt.datetime.strptime(row["time"][:12], "%Y%m%d%H%M").strftime("%Y-%m-%d %H:%M")
            result.append({"date": date, **{key: float(row[key]) for key in ("open", "high", "low", "close", "volume", "amount")}})
        return result
    if op == "factors":
        security = request["security"]
        prefix = {0: "sz", 1: "sh"}.get(security["market"])
        if not prefix:
            raise ValueError("BaoStock 复权因子只支持沪深证券")
        rows = collect(bs.query_adjust_factor(f'{prefix}.{security["code"]}', request["from"], request["to"]))
        return [{"date": row["dividOperateDate"], "forward": row["foreAdjustFactor"],
            "backward": row["backAdjustFactor"], "event": row["adjustFactor"]} for row in rows]
    raise ValueError("未知历史数据源操作")


def main():
    global logged_in
    if sdk_missing:
        print(json.dumps({"error": "缺少 BaoStock SDK，请运行 npm run setup:data"}), flush=True)
        return
    # The Go bridge rewrites the standard proxy variables at each process start,
    # so this process mirrors the resolved application state, not raw user env.
    socket.socket = build_socket_class(resolve_proxy_url())
    for line in sys.stdin:
        try:
            request = json.loads(line)
            # The SDK prints login messages; keep stdout strictly JSON lines.
            with contextlib.redirect_stdout(sys.stderr):
                result = handle(request)
            print(json.dumps({"data": result}, ensure_ascii=False), flush=True)
        except Exception as exc:
            logged_in = False
            print(json.dumps({"error": str(exc)}, ensure_ascii=False), flush=True)
    with contextlib.redirect_stdout(sys.stderr):
        if logged_in:
            bs.logout()

if __name__ == "__main__":
    main()
