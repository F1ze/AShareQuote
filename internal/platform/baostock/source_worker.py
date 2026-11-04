"""Official BaoStock SDK adapter: sequential anonymous session, bounded sockets.

This process only reads public data. It cannot access or mutate the warehouse.
"""
import contextlib
import datetime as dt
import json
import socket
import sys
import time

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


try:
    import baostock as bs
    import baostock.util.socketutil as socketutil
except ImportError:
    print(json.dumps({"error": "缺少 BaoStock SDK，请运行 npm run setup:data"}), flush=True)
    sys.exit(1)

sdk_send = socketutil.send_msg
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
    socket.socket = CheckedSocket
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
