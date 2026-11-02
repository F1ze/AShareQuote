# -*- coding: utf-8 -*-
"""复权事件库：把 gbbq 事件与复权因子落到 SQLite，可增量同步、可导出、可回写 gbbq。

表：
    events(market, code, date, category, dividend, rights_price, bonus, rights, src)
    factor(market, code, date, cum_qfq, cum_hfq)
    meta(key, value)

原则：events 只增不删（同键覆盖），因子每次由 events 全量重算，复权价不落库。
"""
import argparse
import os
import sqlite3
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import tdx_adjust

SCHEMA = """
CREATE TABLE IF NOT EXISTS events(
  market INTEGER NOT NULL, code TEXT NOT NULL, date INTEGER NOT NULL,
  category INTEGER NOT NULL, dividend REAL DEFAULT 0, rights_price REAL DEFAULT 0,
  bonus REAL DEFAULT 0, rights REAL DEFAULT 0, shrink REAL DEFAULT 0,
  warrant_price REAL DEFAULT 0, warrant_ratio REAL DEFAULT 0, src TEXT DEFAULT '',
  PRIMARY KEY(market, code, date, category));
CREATE TABLE IF NOT EXISTS factor(
  market INTEGER NOT NULL, code TEXT NOT NULL, date INTEGER NOT NULL,
  cum_qfq REAL NOT NULL, cum_hfq REAL NOT NULL, basis TEXT NOT NULL DEFAULT 'share',
  PRIMARY KEY(market, code, date));
CREATE TABLE IF NOT EXISTS meta(key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE IF NOT EXISTS done(market INTEGER, code TEXT, at TEXT, events INTEGER,
  PRIMARY KEY(market, code));
"""


# 文件读法：建库 → 导入/同步 → 算因子 → 导出 CSV 或回写 gbbq → CLI。
# 事件表存原始四浮点，factor 表存累计系数，两表分开是为了能重算。
# 建表脚本可重复执行；只有新建库时才写 created 元信息，重跑不会覆盖已有事件。
def open_db(path):
    new = not os.path.exists(path)
    db = sqlite3.connect(path)
    db.executescript(SCHEMA)
    if new:
        db.execute("INSERT INTO meta(key, value) VALUES('created', datetime('now'))")
        db.commit()
    return db


# 主键是 (market, code, date, category)，重复导入只更新字段不新增行，所以可以反复跑。
def upsert(db, events, src="server"):
    n = 0
    for e in events:
        db.execute("INSERT INTO events(market, code, date, category, dividend, rights_price,"
                   " bonus, rights, shrink, warrant_price, warrant_ratio, src)"
                   " VALUES(?,?,?,?,?,?,?,?,?,?,?,?)"
                   " ON CONFLICT(market, code, date, category) DO UPDATE SET"
                   " dividend=excluded.dividend, rights_price=excluded.rights_price,"
                   " bonus=excluded.bonus, rights=excluded.rights, shrink=excluded.shrink,"
                   " warrant_price=excluded.warrant_price,"
                   " warrant_ratio=excluded.warrant_ratio, src=excluded.src",
                   (e.get("market", 0), e["code"], e["date"], e["category"],
                    e.get("dividend") or 0.0, e.get("rights_price") or 0.0,
                    e.get("bonus") or 0.0, e.get("rights") or 0.0,
                    e.get("shrink") or 0.0, e.get("warrant_price") or 0.0,
                    e.get("warrant_ratio") or 0.0, src))
        n += 1
    db.commit()
    return n


def import_gbbq(db, path, market=None):
    """导入 gbbq；11/12 的 f3 是份额拆分比，13/14 的 f1/f3 是行权价与每 10 股份数。"""
    evs = []
    for e in tdx_adjust.load_events(path, market=market):
        cat = e["category"]
        if cat in (11, 12):
            e["shrink"] = e["bonus"]
        elif cat in (13, 14):
            e["warrant_price"], e["warrant_ratio"] = e["dividend"], e["bonus"]
        evs.append(e)
    return upsert(db, evs, src="gbbq")


def _closes(vipdoc, market, code):
    """从本地 .day 取 {日期: 收盘}，取不到返回空表。"""
    import struct
    name = "sh" if market == 1 else "sz" if market == 0 else "bj"
    path = os.path.join(vipdoc, name, "lday", name + code + ".day")
    out = {}
    if not os.path.isfile(path):
        return out
    raw = open(path, "rb").read()
    for off in range(0, len(raw) - 31, 32):
        ymd, _o, _h, _l, close = struct.unpack_from("<IIIII", raw, off)
        out[ymd] = close / 100.0
    return out


# 重算前先清空 factor 表；有前收才走 ratio，缺前收降级 share，两种都在 basis 列里标出来。
def build_factors(db, vipdoc=None):
    """由 events 重算累计因子。

    有前收可算真实比例时 basis=ratio；缺前收则退化为股本比例 basis=share。
    前复权以最新为基准、后复权以最早为基准。
    """
    db.execute("DELETE FROM factor")
    codes = db.execute("SELECT DISTINCT market, code FROM events").fetchall()
    out = 0
    for mkt, code in codes:
        rows = db.execute("SELECT date, category, dividend, rights_price, bonus, rights,"
                          " shrink, warrant_price, warrant_ratio FROM events"
                          " WHERE market=? AND code=? AND category IN (1,11,12,13,14)"
                          " ORDER BY date", (mkt, code)).fetchall()
        if not rows:
            continue
        closes = _closes(vipdoc, mkt, code) if vipdoc else {}
        per, hfq = [], 1.0
        for date, cat, div, rp, bonus, rights, shrink, wpx, wnum in rows:
            prev = None
            for d in sorted(closes, reverse=True):
                if d < date:
                    prev = closes[d]
                    break
            rt = None
            if prev:
                rt = tdx_adjust.ratio({"category": cat, "dividend": div or 0.0,
                                       "rights_price": rp or 0.0, "bonus": bonus or 0.0,
                                       "rights": rights or 0.0, "shrink": shrink or 0.0,
                                       "warrant_price": wpx or 0.0,
                                       "warrant_ratio": wnum or 0.0}, prev)
            basis = "ratio"
            if not rt or rt <= 0:
                if cat in (11, 12):
                    rt = 1.0 / (shrink or 1.0)
                else:
                    rt = 1.0 / (1.0 + (bonus or 0) / 10.0 + (rights or 0) / 10.0)
                basis = "share"
            hfq /= rt
            per.append((date, hfq, basis))
        total = per[-1][1]
        for date, val, basis in per:
            db.execute("INSERT OR REPLACE INTO factor VALUES(?,?,?,?,?,?)",
                       (mkt, code, date, total / val, val, basis))
            out += 1
    db.commit()
    return out


def stats(db):
    q = lambda s: db.execute(s).fetchone()[0]
    return {"events": q("SELECT COUNT(*) FROM events"),
            "priced": q("SELECT COUNT(*) FROM events WHERE category IN (1,11,12,13,14)"),
            "codes": q("SELECT COUNT(DISTINCT market||code) FROM events"),
            "factors": q("SELECT COUNT(*) FROM factor"),
            "ratio_basis": q("SELECT COUNT(*) FROM factor WHERE basis='ratio'"),
            "first": q("SELECT MIN(date) FROM events"),
            "last": q("SELECT MAX(date) FROM events")}


# 导出给人工核对：类别码翻成中文名放在 name 列，其余列保持库里的原始顺序。
def export_csv(db, out):
    with open(out, "w", encoding="utf-8", newline="") as fh:
        import csv
        w = csv.writer(fh)
        w.writerow(["market", "code", "date", "category", "name", "dividend_per10",
                    "rights_price", "bonus_per10", "rights_per10", "shrink",
                    "warrant_price", "warrant_ratio", "src"])
        for row in db.execute("SELECT market, code, date, category, dividend, rights_price,"
                              " bonus, rights, src FROM events ORDER BY market, code, date"):
            w.writerow(list(row[:4]) + [tdx_adjust.CATEGORIES.get(row[3], "")] + list(row[4:]))
    return out


# 库里把 11/12 的拆分比单独存 shrink、13/14 的行权价与份数存 warrant_*；
# 回写时要还原成 gbbq 的 f1/f3 位置，不能直接把列按顺序塞回去。
def to_gbbq(db, out):
    """回写 gbbq；11/12 的缩股比例回填 f1，13/14 的行权价与份数回填 f1/f3。"""
    evs = []
    for m, c, d, k, dv, rp, b, r, sh, wp, wn in db.execute(
            "SELECT market, code, date, category, dividend, rights_price, bonus, rights,"
            " shrink, warrant_price, warrant_ratio FROM events"):
        if k in (11, 12):
            dv, b = sh or dv, b
        elif k in (13, 14):
            dv, b = wp or dv, wn or b
        evs.append({"market": m, "code": c, "date": d, "category": k,
                    "dividend": dv, "rights_price": rp, "bonus": b, "rights": r})
    return tdx_adjust.write_gbbq(evs, out)


def done_set(db, market):
    """已同步过的代码集合，供断点续传。"""
    return {c for (c,) in db.execute("SELECT code FROM done WHERE market=?", (market,))}


# 同步前先读 done 表拿断点；redo 时清空该市场进度。抓取失败只计数，不中断整批。
def sync(db, market, codes, cfg=None, limit=None, redo=False):
    """逐只抓权息入库；默认跳过 done 里已完成的代码，redo 则重来。

    每只无论有无事件都记一行 done，失败不记，下次会重试。
    """
    import tdx_client
    seen = done_set(db, market) if not redo else set()
    todo = [c for c in codes if c not in seen]
    skipped = len(codes) - len(todo)
    print("待同步 %d 只（已完成跳过 %d 只）" % (len(todo), skipped))
    conn = tdx_client.TdxConnection(cfg_path=cfg)
    conn.connect()
    done = failed = 0
    try:
        # 逐只抓 0x000F，入库后立刻记 done，抓完一只断掉也不重来。
        for code in todo:
            try:
                evs = conn.xdxr_info(market, code)
            except Exception as exc:
                failed += 1
                print("  %s 失败 %r" % (code, exc))
                continue
            upsert(db, evs)
            db.execute("INSERT OR REPLACE INTO done VALUES(?,?,datetime('now'),?)",
                       (market, code, len(evs)))
            db.commit()
            done += 1
            if limit and done >= limit:
                break
    finally:
        conn._close()
    print("本轮完成 %d 只，失败 %d 只" % (done, failed))
    return done


def main(argv=None):
    ap = argparse.ArgumentParser(description="复权事件库（SQLite）")
    sub = ap.add_subparsers(dest="cmd", required=True)
    for name in ("stats", "build"):
        p = sub.add_parser(name)
        p.add_argument("--db", required=True)
        if name == "build":
            p.add_argument("--togbbq")
            p.add_argument("--vipdoc")
    p = sub.add_parser("import")
    p.add_argument("--db", required=True)
    p.add_argument("--gbbq", required=True)
    p.add_argument("--market", type=int)
    p = sub.add_parser("sync")
    p.add_argument("--db", required=True)
    p.add_argument("--market", type=int, default=1)
    p.add_argument("--codes", help="逗号分隔，缺省取该市场全部证券")
    p.add_argument("--limit", type=int)
    p.add_argument("--cfg")
    p.add_argument("--redo", action="store_true", help="忽略断点，全部重抓")
    p = sub.add_parser("export")
    p.add_argument("--db", required=True)
    p.add_argument("--out", required=True)
    p = sub.add_parser("gbbq")
    p.add_argument("--db", required=True)
    p.add_argument("--out", required=True)
    a = ap.parse_args(argv)
    a.db = os.path.abspath(a.db)
    for k in ("vipdoc", "gbbq", "togbbq"):
        v = getattr(a, k, None)
        if v:
            setattr(a, k, os.path.abspath(v))
    if a.cmd in ("stats", "build", "export", "gbbq") and not os.path.exists(a.db):
        print("缺库 %s（先用 import 或 sync 建库）" % a.db)
        return 2
    if a.cmd == "import" and not os.path.exists(a.gbbq):
        print("缺文件 %s" % a.gbbq)
        return 2

# 每个子命令进来都先开库，库文件与输入路径已在上面统一成绝对路径并做过存在性判断。
    db = open_db(a.db)
    if a.cmd == "stats":
        print(stats(db))
        print("已完成代码", len(done_set(db, 0)) + len(done_set(db, 1)))
        return 0
    if a.cmd == "build":
        n = build_factors(db, a.vipdoc)
        print("因子行数", n)
        if a.togbbq:
            print("回写 gbbq:", to_gbbq(db, a.togbbq), "条")
        return 0
    if a.cmd == "import":
        print("导入", import_gbbq(db, a.gbbq, a.market), "条")
        return 0
    if a.cmd == "sync":
        if a.codes:
            codes = [c.strip() for c in a.codes.split(",") if c.strip()]
        else:
            import tdx_client
            conn = tdx_client.TdxConnection(cfg_path=a.cfg)
            conn.connect()
            codes, start = [], 0
            try:
                while True:
                    batch = conn.security_list(a.market, start, 1000)
                    if not batch:
                        break
                    codes += [c for _m, c, _n in batch]
                    start += len(batch)
            finally:
                conn._close()
            print("市场 %d 证券 %d 只" % (a.market, len(codes)))
        sync(db, a.market, codes, a.cfg, a.limit, a.redo)
        db.execute("INSERT OR REPLACE INTO meta VALUES('synced_at', datetime('now'))")
        db.commit()
        return 0
    if a.cmd == "export":
        print("导出", export_csv(db, a.out))
        return 0
    if a.cmd == "gbbq":
        print("写出", to_gbbq(db, a.out), "条 →", a.out)
        return 0
    return 0


if __name__ == "__main__":
    sys.exit(main())