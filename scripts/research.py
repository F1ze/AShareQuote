"""Read a research snapshot while QFLUX runs, or its DuckDB after shutdown."""
import argparse
from pathlib import Path
import re
import sys

import duckdb

for stream in (sys.stdout, sys.stderr):
    if hasattr(stream, "reconfigure"):
        stream.reconfigure(encoding="utf-8")

parser = argparse.ArgumentParser(description="QFLUX 原始日线查询示例")
location = parser.add_mutually_exclusive_group(required=True)
location.add_argument("--database", type=Path)
location.add_argument("--snapshot", type=Path)
parser.add_argument("--symbol", default="sh600000")
args = parser.parse_args()
match = re.fullmatch(r"(sh|sz|bj)([0-9]{6})", args.symbol.lower())
if not match:
    parser.error("symbol 应类似 sh600000 / sz000001 / bj920001")

try:
    con = duckdb.connect(str(args.database), read_only=True) if args.database else duckdb.connect(":memory:")
    if args.snapshot:
        for table in ("bars", "instruments", "trading_calendar", "provider_factors", "adjustment_events", "bar_revisions", "ingestion_receipts"):
            path = args.snapshot / (table + ".parquet")
            if path.is_file():
                con.read_parquet(str(path)).create_view(table)
        con.execute("CREATE VIEW daily_bars AS SELECT *,ts::DATE trade_date FROM bars WHERE period='day'")
    with con:
        summary = con.execute("""SELECT count(*),min(trade_date),max(trade_date)
            FROM daily_bars WHERE exchange=? AND code=?""", [match[1].upper(), match[2]]).fetchone()
        print("records / first / last:", summary)
        print("原始价（元） / 成交量（股） / 成交额（元）")
        for row in con.execute("""SELECT trade_date,open,high,low,close,volume_shares,amount_cny
            FROM daily_bars WHERE exchange=? AND code=? ORDER BY trade_date DESC LIMIT 10""", [match[1].upper(), match[2]]).fetchall():
            print(row)
except duckdb.IOException as exc:
    print(f"数据库不可读：{exc}\n应用持有写锁时请先导出快照，或关闭应用后使用 --database。", file=sys.stderr)
    sys.exit(1)
