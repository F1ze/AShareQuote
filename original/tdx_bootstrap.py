# -*- coding: utf-8 -*-
"""从零引导一套可用的通达信数据目录（不联网、不依赖安装包）。

只做「造骨架」这件事：目录树、connect.cfg、空 gbbq 占位、清单元数据。
二进制客户端文件（TdxW.exe 及 DLL、dsys.dat、dsmarket.dat）可通过 --ref
从任意现成安装复制，也可以用 tdx_client 拉数据把目录填满。
"""
import argparse
import hashlib
import json
import os
import shutil
import struct
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import tdx_local

T0002_DIRS = ("bdhq_cache blocknew cache Certi cloud_cache cloud_cfg cloud_dax cloud_pad "
              "cloud_pad2 cloud_pad3 cloud_pad4 cloud_pad5 cloud_pad6 cloud_pad7 cloud_xml2 "
              "cloud_xml3 cloud_xml4 cloud_xml5 cloud_xml6 cloud_xml7 cw_cache diary dlls "
              "ds_cache export ext_cache gs_bak hq_cache info_cache invest lc log "
              "logoflash_v1 lzjy msg_hq msg_jy msg_tq msg_zx note pad qqdata signals sound "
              "tmp User usrdat utx_data wt_cache wt_data xml_cache zst_cache").split()
MARKETS = ("bj", "sh", "sz", "ds", "ot", "cw")
SUB_DIRS = ("lday", "minline", "fzline", "eday")
REF_GLOBS = ("*.exe", "*.dll", "*.dax", "*.daq", "*.dai", "*.xmb", "*.ini", "*.dat",
             "*.dbf", "*.cfg", "*.json", "*.txt", "*.html", "*.css", "*.chm", "*.bmp",
             "*.jpg", "*.ql", "*.up")
REF_SKIP = ("connect.cfg", "base.dbf")
REF_DIRS = ("chrome", "files", "funcs", "GNPlugins", "jspages", "QHData", "SEPlugins",
            "TCloudCalc", "TCPlugins", "webs", "xiadan", "ZDPlugins")


# 文件读法：目录蓝图 → init/regen → fill 抓行情 → base 主表 → 登录探测 → 权息断点同步 → verify → CLI。
# 写操作全在 root 里发生，--ref 只读；exe/DLL 是复制，不是重新生成。
# 待建目录 = T0002 下 51 个缓存/板块子目录 + 6 个市场 × 4 类行情目录 + vipdoc/ds，共 76 个。

def _dirs(root):
    out = [os.path.join(root, "T0002", d) for d in T0002_DIRS]
    for m in MARKETS:
        for s in SUB_DIRS:
            out.append(os.path.join(root, "vipdoc", m, s))
    out.append(os.path.join(root, "vipdoc", "ds"))
    return out


def init(root, spec_path=None, ref=None):
    if os.path.exists(root) and os.listdir(root):
        raise SystemExit("目标目录非空：%s" % root)
    created, copied = [], []
    for d in _dirs(root):
        os.makedirs(d, exist_ok=True)
        created.append(os.path.relpath(d, root))

    spec = (json.load(open(spec_path, encoding="utf-8")) if spec_path
            else tdx_local.default_spec(os.path.join(ref, "connect.cfg") if ref else None))
    text = tdx_local.build_sitecfg(spec)
    cfg = os.path.join(root, "connect.cfg")
    with open(cfg, "wb") as fh:
        fh.write(text.encode("gbk", "replace"))

    gbbq = os.path.join(root, "T0002", "hq_cache", "gbbq")
    with open(gbbq, "wb") as fh:
        fh.write(struct.pack("<I", 0))

    # 参照层只搬程序文件：T0002 里小于 2 MB 的单文件全拷，根目录按白名单模式拷；
    # connect.cfg 与 base.dbf 一律跳过，它们由本工具自己造。
    if ref:
        import glob
        t2 = os.path.join(ref, "T0002")
        if os.path.isdir(t2):
            for name in os.listdir(t2):
                src = os.path.join(t2, name)
                if os.path.isfile(src) and os.path.getsize(src) < 2000000:
                    shutil.copy2(src, os.path.join(root, "T0002", name))
                    copied.append("T0002/" + name)
        for pat in REF_GLOBS:
            for src in sorted(glob.glob(os.path.join(ref, pat))):
                name = os.path.basename(src)
                if name in REF_SKIP or name in copied:
                    continue
                if name.endswith((".bak", ".id0", ".id1", ".id2", ".nam", ".til")):
                    continue
                shutil.copy2(src, os.path.join(root, name))
                copied.append(name)
        for d in REF_DIRS:
            src = os.path.join(ref, d)
            if os.path.isdir(src):
                shutil.copytree(src, os.path.join(root, d), dirs_exist_ok=True,
                                ignore=shutil.ignore_patterns("*.bak"))
                copied.append(d + "/")

    man = {"built_at": time.strftime("%Y-%m-%d %H:%M:%S"),
           "spec_sha256": hashlib.sha256(json.dumps(spec, sort_keys=True,
                                                    ensure_ascii=False).encode()).hexdigest(),
           "connect_cfg_sha256": hashlib.sha256(open(cfg, "rb").read()).hexdigest(),
           "dirs": len(created), "copied": copied,
           "hosts": len(spec.get("hosts") or [])}
    with open(os.path.join(root, "bootstrap.json"), "w", encoding="utf-8") as fh:
        json.dump(man, fh, ensure_ascii=False, indent=2)
    return man


def regen(root, spec_path=None, ref=None):
    """重造 <root>/connect.cfg 并刷新清单里的配置哈希，树的其它内容不动。"""
    if not os.path.isdir(root):
        raise SystemExit("目录不存在：%s" % root)
    spec = (json.load(open(spec_path, encoding="utf-8")) if spec_path
            else tdx_local.default_spec(os.path.join(ref, "connect.cfg") if ref else None))
    text = tdx_local.build_sitecfg(spec)
    cfg = os.path.join(root, "connect.cfg")
    with open(cfg, "wb") as fh:
        fh.write(text.encode("gbk", "replace"))
    path = os.path.join(root, "bootstrap.json")
    man = {}
    if os.path.isfile(path):
        try:
            man = json.load(open(path, encoding="utf-8"))
        except Exception:
            man = {}
    man["connect_cfg_sha256"] = hashlib.sha256(open(cfg, "rb").read()).hexdigest()
    man["regen_at"] = time.strftime("%Y-%m-%d %H:%M:%S")
    man.setdefault("built_at", man["regen_at"])
    man.setdefault("spec_sha256", hashlib.sha256(
        json.dumps(spec, sort_keys=True, ensure_ascii=False).encode()).hexdigest())
    man["hosts"] = len(spec.get("hosts") or [])
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(man, fh, ensure_ascii=False, indent=2)
    return cfg


def fill(root, market, codes, days=250, cfg=None, merge=True):
    """用 7709 客户端把日线写进 vipdoc。

    merge=True 时与已有 .day 按日期合并，新抓的覆盖同日旧值；这样每次只抓最近
    days 根也能把历史一天天攒厚，不必一次拉满。
    """
    import tdx_client
    pref = tdx_client.PREFIX[market]
    outdir = os.path.join(root, "vipdoc", tdx_client.MARKET_DIR[market], "lday")
    os.makedirs(outdir, exist_ok=True)
    conn = tdx_client.TdxConnection(cfg_path=cfg or os.path.join(root, "connect.cfg"))
    conn.connect()
    written = []
    try:
        for code in codes:
            recs = conn.security_bars(9, market, code, 0, min(days, 800))
            path = os.path.join(outdir, "%s%s.day" % (pref, code))
            # 合并写：先把旧文件按日期读进字典（32 字节记录：u32 日期 + 4 个 u32 价 + f32 额 + u32 量 + u32 保留），
            # 新抓的同日覆盖，最后按日期升序整体重写，旧历史一根不丢。
            if merge and os.path.exists(path):
                old = {}
                with open(path, "rb") as fh:
                    raw = fh.read()
                for off in range(0, len(raw) - 31, 32):
                    old[struct.unpack_from("<I", raw, off)[0]] = raw[off:off + 32]
                for r in recs:
                    y, mo, d = r["dt"][0], r["dt"][1], r["dt"][2]
                    old[y * 10000 + mo * 100 + d] = struct.pack(
                        "<IIIIIfII", y * 10000 + mo * 100 + d,
                        int(round(r["open"] * 100)), int(round(r["high"] * 100)),
                        int(round(r["low"] * 100)), int(round(r["close"] * 100)),
                        float(r["amount"]), int(r["vol"]), 0)
                with open(path, "wb") as fh:
                    for k in sorted(old):
                        fh.write(old[k])
                written.append((code, len(recs)))
                continue
            with open(path, "wb") as fh:
                for r in sorted(recs, key=lambda x: x["dt"]):
                    y, mo, d = r["dt"][0], r["dt"][1], r["dt"][2]
                    fh.write(struct.pack("<IIIIIfII", y * 10000 + mo * 100 + d,
                                         int(round(r["open"] * 100)), int(round(r["high"] * 100)),
                                         int(round(r["low"] * 100)), int(round(r["close"] * 100)),
                                         float(r["amount"]), int(r["vol"]), 0))
            written.append((code, len(recs)))
    finally:
        conn._close()
    return written


def gen_base(root, markets, limit=0, cfg=None):
    """按服务端证券清单生成 base.dbf，替代参照安装里的 3.8 MB 主表。"""
    import tdx_client
    conn = tdx_client.TdxConnection(cfg_path=cfg or os.path.join(root, "connect.cfg"))
    conn.connect()
    rows = []
    try:
        for market in markets:
            start, got = 0, 0
            while True:
                batch = conn.security_list(market, start, 1000)
                if not batch:
                    break
                for _m, code, name in batch:
                    rows.append((market, code, name))
                    got += 1
                    if limit and got >= limit:
                        break
                start += len(batch)
                if limit and got >= limit:
                    break
    finally:
        conn._close()
    # 按市场、代码排序后再写，保证同样输入得到同样字节序的表，便于比对哈希。
    rows.sort(key=lambda r: (r[0], r[1]))
    path = os.path.join(root, "T0002", "hq_cache", "base.dbf")
    os.makedirs(os.path.dirname(path), exist_ok=True)
    return tdx_local.gen_base(path, rows), path


def probe_login(root, cfg=None):
    """探测登录链路并把结果写进 <root>/login.json（只读探测，不改配置）。"""
    import tdx_client
    got = tdx_client.login_probe(cfg_path=cfg or os.path.join(root, "connect.cfg"))
    got["at"] = time.strftime("%Y-%m-%d %H:%M:%S")
    path = os.path.join(root, "login.json")
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(got, fh, ensure_ascii=False, indent=1)
    return got, path


# 断点文件放在树内的 hq_cache 下，跟着树走：换机器、换目录都还能接着跑。
def _state_path(root):
    return os.path.join(root, "T0002", "hq_cache", ".xdxr_state.json")


def _load_state(root):
    path = _state_path(root)
    if not os.path.exists(path):
        return {"done": {}}
    try:
        got = json.load(open(path, encoding="utf-8"))
    except Exception:
        return {"done": {}}
    got.setdefault("done", {})
    return got


def _save_state(root, state):
    state["at"] = time.strftime("%Y-%m-%d %H:%M:%S")
    with open(_state_path(root), "w", encoding="utf-8") as fh:
        json.dump(state, fh, ensure_ascii=False, indent=1)


def sync_gbbq(root, market, codes=None, limit=None, cfg=None, redo=False):
    """从服务端逐只抓除权除息，写成合法 gbbq。

    断点续传：已成功的代码记在 hq_cache/.xdxr_state.json，默认跳过，
    redo 为真时清空该市场的进度重来。gbbq 每只写完即落盘，中途断掉不丢已抓部分。
    """
    import tdx_adjust
    import tdx_client
    state = _load_state(root)
    done = {} if redo else dict(state["done"].get(str(market), {}))
    conn = tdx_client.TdxConnection(cfg_path=cfg or os.path.join(root, "connect.cfg"))
    conn.connect()
    if not codes:
        codes, start = [], 0
        while True:
            batch = conn.security_list(market, start, 1000)
            if not batch:
                break
            codes += [c for _m, c, _n in batch]
            start += len(batch)
    # 已完成的代码直接跳过；每抓完一只就把进度写盘，中断重跑从这里接着走。
    todo = [c for c in codes if c not in done]
    print("待同步 %d 只（已完成跳过 %d 只）" % (len(todo), len(codes) - len(todo)))
    path = os.path.join(root, "T0002", "hq_cache", "gbbq")
    events = {}
    if os.path.exists(path):
        for m, c, d, cat, f1, f2, f3, f4 in tdx_adjust.iter_events(path):
            events[(m, c, d, cat)] = {"market": m, "code": c, "date": d, "category": cat,
                                      "dividend": f1, "rights_price": f2, "bonus": f3,
                                      "rights": f4}
    base = len(events)
    failed = 0
    try:
        for code in todo:
            try:
                got = conn.xdxr_info(market, code)
            except Exception:
                failed += 1
                continue
            for ev in got:
                events[(ev["market"], ev["code"], ev["date"], ev["category"])] = ev
            done[code] = len(got)
            state["done"][str(market)] = done
            _save_state(root, state)
            # limit 按已完成的代码数收口，不是按请求数；配合断点正好可以分几次拉完。
            if limit and len(done) >= limit:
                break
    finally:
        conn._close()
    n = tdx_adjust.write_gbbq(list(events.values()), path)
    return {"records": n, "added": n - base, "symbols": len({(e["market"], e["code"]) for e in events}),
            "failed": failed, "codes": len(codes), "skipped": len(codes) - len(todo)}


# 校验项：目录齐全、connect.cfg 可解析、清单里的配置哈希对得上、gbbq 头与长度自洽、base.dbf 可读。
def verify(root):
    problems = []
    if not os.path.isdir(root):
        return ["目录不存在"], {}
    # 目录缺一个都算问题；配置、清单、gbbq、主表逐项校验，最后汇总成问题列表。
    for d in _dirs(root):
        if not os.path.isdir(d):
            problems.append("缺目录 %s" % os.path.relpath(d, root))
    cfg = os.path.join(root, "connect.cfg")
    stats = {}
    if not os.path.isfile(cfg):
        problems.append("缺 connect.cfg")
    else:
        got = tdx_local.parse_sitecfg(cfg)
        stats["sections"] = len(got["sections"])
        stats["hosts"] = len(got["hosts"])
        problems += got["errors"]
        if not got["hosts"]:
            problems.append("站点表为空")
        uh = got["sections"].get("UserHost", {})
        if not uh.get("IPAddress", "").strip():
            problems.append("UserHost.IPAddress 为空，登录口没有落点")
        if uh.get("Port", "").strip() not in ("7715", ""):
            problems.append("UserHost.Port 是 %s，参照为 7715" % uh.get("Port"))
        for key in ("QSID", "CloudQSID"):
            if not uh.get(key, "").strip():
                problems.append("UserHost.%s 为空" % key)
        stats["userhost_keys"] = len(uh)
    gbbq = os.path.join(root, "T0002", "hq_cache", "gbbq")
    if not os.path.isfile(gbbq):
        problems.append("缺 gbbq 占位")
    else:
        raw = open(gbbq, "rb").read()
        stats["gbbq_bytes"] = len(raw)
        if len(raw) >= 4:
            n = struct.unpack_from("<I", raw, 0)[0]
            stats["gbbq_records"] = n
            if 4 + n * 29 != len(raw):
                problems.append("gbbq 长度 %d 与条数 %d 不符" % (len(raw), n))
    man = os.path.join(root, "bootstrap.json")
    if os.path.isfile(man):
        got = json.load(open(man, encoding="utf-8"))
        if got.get("connect_cfg_sha256") and os.path.isfile(cfg):
            now = hashlib.sha256(open(cfg, "rb").read()).hexdigest()
            if now != got["connect_cfg_sha256"]:
                problems.append("connect.cfg 已被改动（清单哈希不符）")
    else:
        problems.append("缺 bootstrap.json 清单")
    return problems, stats


def main(argv=None):
    ap = argparse.ArgumentParser(description="从零引导通达信数据目录")
    sub = ap.add_subparsers(dest="cmd", required=True)
    p = sub.add_parser("plan")
    p.add_argument("root")
    p = sub.add_parser("init")
    p.add_argument("root")
    p.add_argument("--spec")
    p.add_argument("--ref", help="复制 exe/dsys.dat 等静态文件的参照安装目录")
    p = sub.add_parser("fill")
    p.add_argument("root")
    p.add_argument("--market", type=int, default=1)
    p.add_argument("--codes", required=True, help="逗号分隔")
    p.add_argument("--days", type=int, default=250)
    p.add_argument("--no-merge", action="store_true", help="覆盖写，不与已有日线合并")
    p.add_argument("--cfg", default=None)
    p = sub.add_parser("sync")
    p.add_argument("root")
    p.add_argument("--market", type=int, default=1)
    p.add_argument("--codes")
    p.add_argument("--limit", type=int, default=20, help="本轮最多同步多少只证券")
    p.add_argument("--cfg")
    p.add_argument("--redo", action="store_true", help="忽略断点，清空该市场进度重抓")
    p = sub.add_parser("regen")
    p.add_argument("root")
    p.add_argument("--spec")
    p.add_argument("--ref")
    p = sub.add_parser("login")
    p.add_argument("root")
    p.add_argument("--cfg")
    p = sub.add_parser("base")
    p.add_argument("root")
    p.add_argument("--markets", default="0,1")
    p.add_argument("--limit", type=int, default=0)
    p.add_argument("--cfg")
    p = sub.add_parser("verify")
    p.add_argument("root")
    a = ap.parse_args(argv)
    # 相对路径统一按当前目录展开，后续一律用绝对路径，避免受 cwd 变化影响
    a.root = os.path.abspath(a.root)
    for k in ("spec", "ref", "cfg"):
        v = getattr(a, k, None)
        if v:
            setattr(a, k, os.path.abspath(v))

    if getattr(a, "ref", None) and not os.path.isdir(a.ref):
        print("缺参照目录 %s（--ref 指向客户端安装目录）" % a.ref)
        return 2

    if a.cmd == "plan":
        dirs = _dirs(a.root)
        print("将创建 %d 个目录：" % len(dirs))
        for d in dirs[:8]:
            print("  " + os.path.relpath(d, a.root))
        print("  ...")
        print("另写 connect.cfg、T0002\\hq_cache\\gbbq 占位、bootstrap.json")
        return 0
    if a.cmd == "init":
        man = init(a.root, a.spec, a.ref)
        print("已生成 %s：目录 %d，站点 %d，复制 %d 个静态文件"
              % (a.root, man["dirs"], man["hosts"], len(man["copied"])))
        return 0
    if a.cmd == "fill":
        got = fill(a.root, a.market, [c.strip() for c in a.codes.split(",") if c.strip()],
                   a.days, a.cfg)
        for code, n in got:
            print("  %s 写入 %d 条日线" % (code, n))
        print("合计 %d 只 / %d 条" % (len(got), sum(n for _, n in got)))
        return 0
    if a.cmd == "sync":
        codes = [c.strip() for c in a.codes.split(",")] if a.codes else None
        got = sync_gbbq(a.root, a.market, codes, a.limit, a.cfg, a.redo)
        print("同步 %d 只（跳过 %d，失败 %d），新增 %d 条，gbbq 共 %d 条"
              % (got["symbols"], got["skipped"], got["failed"], got["added"], got["records"]))
        return 0
    if a.cmd == "regen":
        cfg = regen(a.root, a.spec, a.ref)
        print("已重造", cfg)
        return 0
    if a.cmd == "login":
        got, path = probe_login(a.root, a.cfg)
        for rec in got["main"]:
            print("7709 %-12s %s" % (rec["variant"], "回包" if rec["ok"] else rec.get("error")))
        for rec in got["login_ports"]:
            print("登录口 %s:%d %s" % (rec["host"], rec["port"], "可达" if rec["tcp"] else "不可达"))
        print("已写", path)
        return 0
    if a.cmd == "base":
        n, path = gen_base(a.root, [int(x) for x in a.markets.split(",") if x.strip()],
                           a.limit, a.cfg)
        print("已生成 %s：%d 条" % (path, n))
        return 0
    problems, stats = verify(a.root)
    for p_ in problems:
        print("问题:", p_)
    print("统计:", stats)
    print("结论:", "通过" if not problems else "%d 个问题" % len(problems))
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())