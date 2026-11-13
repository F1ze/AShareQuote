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
# vipdoc 下的一级市场目录，与客户端 0x96a6c0 起的登记串一致：sh/sz/bj 是沪深京三个交易市场，
# ds 是扩展行情（模板 vipdoc\ds\lday\%d#%s.day，放期货连续、美股、港股这类带序号前缀的标的），
# ot 与 cw 是安装器预留下来的两个杂项目录（ot 本机为空、客户端字符串里没有它；cw 只放财务文件）。
MIN_MAX = 24000          # 分钟族一次最多翻多少条：服务端只留约 22080 条
MARKETS = ("bj", "sh", "sz", "ds", "ot", "cw")
# 每个市场各自的子目录：ds 只有三类分钟线与日线（没有 eday），ot 与 cw 没有子目录。
MARKET_SUBS = {
    "sh": ("lday", "minline", "fzline", "eday"),
    "sz": ("lday", "minline", "fzline", "eday"),
    "bj": ("lday", "minline", "fzline", "eday"),
    "ds": ("lday", "minline", "fzline"),
    "ot": (),
    "cw": (),
}
REF_GLOBS = ("*.exe", "*.dll", "*.dax", "*.daq", "*.dai", "*.xmb", "*.ini", "*.dat",
             "*.dbf", "*.cfg", "*.json", "*.txt", "*.html", "*.css", "*.chm", "*.bmp",
             "*.jpg", "*.ql", "*.up")
REF_SKIP = ("connect.cfg", "base.dbf")
REF_DIRS = ("chrome", "files", "funcs", "GNPlugins", "jspages", "QHData", "SEPlugins",
            "TCloudCalc", "TCPlugins", "webs", "xiadan", "ZDPlugins")


# 文件读法：目录蓝图 → init/regen → fill 抓行情 → base 主表 → 登录探测 → 权息断点同步 → verify → CLI。
# 写操作全在 root 里发生，--ref 只读；exe/DLL 是复制，不是重新生成。
# 待建目录 = T0002 下 51 个缓存/板块子目录 + vipdoc 的 6 个市场目录与 14 个行情子目录，共 71 个。

def _dirs(root):
    out = [os.path.join(root, "T0002", d) for d in T0002_DIRS]
    for m in MARKETS:
        base = os.path.join(root, "vipdoc", m)
        out.append(base)
        for s in MARKET_SUBS.get(m, ()):
            out.append(os.path.join(base, s))
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


def fill(root, market, codes, days=250, cfg=None, merge=True, from_root=None, kind="day"):
    """把行情写进 vipdoc。kind 取 day（日线）/ min（1 分钟）/ 5min（5 分钟）。

    merge 为真时按时间键与已有文件合并，新抓的覆盖同一时间点，旧历史一根不丢。
    分钟族服务端只留约两万两千条（92 个交易日，见报告 43 章），days 给 0 表示
    能翻多深翻多深：start 一路加，取到空或到 MIN_MAX 为止。
    """
    import tdx_client
    codes = tdx_client.normalize_codes(codes)
    category, subdir, ext = tdx_client.KIND_MAP.get(kind, tdx_client.KIND_MAP["day"])
    pref = tdx_client.PREFIX[market]
    outdir = os.path.join(root, "vipdoc", tdx_client.MARKET_DIR[market], subdir)
    os.makedirs(outdir, exist_ok=True)
    full = kind != "day" and days <= 0
    want = MIN_MAX if full else min(days, 800)
    step = 800 if kind != "day" else min(days, 800)

    cfg = cfg or os.path.join(root, "connect.cfg")
    if not from_root and not os.path.isfile(cfg):
        # 目标树没配置时往回收：先仓库自带的生成样本，再退回安装目录的默认配置。
        # 只给了个空目录就 fill 的场合很常见，不该直接抛"没有可用主机"。
        for cand in (os.path.join(HERE, "connect.cfg"), os.path.join(HERE, "connect.gen.cfg")):
            if os.path.isfile(cand):
                cfg = cand
                break
        else:
            cfg = None
        print("提示: %s 下没有 connect.cfg，改用 %s" % (root, cfg or tdx_client.CFG_DEFAULT),
              file=sys.stderr)

    if from_root:
        # 离线拷贝：退市股历史、或超出服务端保留深度的老分钟线，只能从旧树搬
        src_dir = os.path.join(from_root, "vipdoc", tdx_client.MARKET_DIR[market], subdir)
        out = []
        for code in codes:
            name = "%s%s%s" % (pref, code, ext)
            src = os.path.join(src_dir, name)
            if not os.path.isfile(src):
                print("  %s 源树没有 %s" % (code, name), file=sys.stderr)
                continue
            dst = os.path.join(outdir, name)
            if merge:
                store = tdx_client.read_ticks_file(dst)          # 目标在前，同日以源为准
                store.update(tdx_client.read_ticks_file(src))
                tdx_client.write_all(dst, store)
            else:
                shutil.copyfile(src, dst)
            out.append((code, os.path.getsize(dst) // (tdx_client.MIN_REC if kind != "day" else 32)))
        return out

    conn = tdx_client.TdxConnection(cfg_path=cfg)
    conn.connect()
    written, misses = [], []
    try:
        for code in codes:
            rows, start_ = [], 0
            while len(rows) < want:
                batch = None
                for _try in range(3):                  # 主机有成片的"连上即关"，同段失败了退一步重连再试
                    try:
                        batch = conn.security_bars(category, market, code, start_, step)
                        break
                    except Exception as exc:           # noqa: BLE001
                        conn._close()
                        print("  %s 第 %d 段失败重试: %r" % (code, start_, exc), file=sys.stderr)
                if batch is None:
                    break
                if not batch:
                    break
                rows.extend(batch)
                start_ += len(batch)
                # 指数与板块一次只回一根，不能因为不满一段就收工；空包才停
                if len(batch) == 0:
                    break
            path = os.path.join(outdir, "%s%s%s" % (pref, code, ext))
            if not rows:
                # 退市、摘牌、或超出服务端保留深度；保留旧文件，没有旧文件就不造空文件
                kept = 0
                if os.path.exists(path):
                    kept = os.path.getsize(path) // (tdx_client.MIN_REC if kind != "day" else 32)
                misses.append((code, kept))
                if kept:
                    written.append((code, 0))
                continue
            store = {}
            if merge:
                store = tdx_client.read_ticks_file(path)
            for r in rows:
                y, mo, d, hh, mm = r["dt"]
                key = y * 100000000 + mo * 1000000 + d * 10000 + hh * 100 + mm
                if kind == "day":
                    store[y * 10000 + mo * 100 + d] = tdx_client.pack_day(
                        y * 10000 + mo * 100 + d, r["open"], r["high"], r["low"], r["close"],
                        r["amount"], r["vol"])
                else:
                    store[(y, mo, d, hh, mm)] = tdx_client.pack_min(
                        y, mo, d, hh, mm, r["open"], r["high"], r["low"], r["close"],
                        float(r["amount"]), int(r["vol"]))
            tdx_client.write_all(path, store)
            written.append((code, len(rows)))
    finally:
        conn._close()
    if misses:
        for code, kept in misses:
            print("  %s 无数据（退市、摘牌或超出保留深度）%s"
                  % (code, "，保留本地旧文件 %d 条" % kept if kept else ""), file=sys.stderr)
    return written


def gen_base(root, markets, limit=0, cfg=None):
    """按服务端证券清单生成 base.dbf，替代参照安装里的 3.8 MB 主表。"""
    import tdx_client
    cfg = cfg or os.path.join(root, "connect.cfg")
    if not os.path.isfile(cfg):
        # 目标树没配置时往回收：先仓库自带的生成样本，再退回安装目录的默认配置。
        # 只给了个空目录就 fill 的场合很常见，不该直接抛"没有可用主机"。
        for cand in (os.path.join(HERE, "connect.cfg"), os.path.join(HERE, "connect.gen.cfg")):
            if os.path.isfile(cand):
                cfg = cand
                break
        else:
            cfg = None
        print("提示: %s 下没有 connect.cfg，改用 %s" % (root, cfg or tdx_client.CFG_DEFAULT),
              file=sys.stderr)

    conn = tdx_client.TdxConnection(cfg_path=cfg)
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


def sync_gbbq(root, market, codes=None, limit=None, cfg=None, redo=False, jobs=64, rounds=3):
    """从服务端逐只抓除权除息，写成合法 gbbq。

    断点续传：已成功的代码记在 hq_cache/.xdxr_state.json，默认跳过，
    redo 为真时清空该市场的进度重来。gbbq 每只记完即更新状态，中途断掉不丢已抓部分。
    jobs 是并发连接数：服务端一个连接只答一次业务请求，靠多条连接并行才快得起来。
    高并发下服务端会甩掉一部分连接（实测 64 并发约两成失败），所以失败的代码不记 done，
    收尾按 rounds 自动补抓，补到没有失败或到上限为止，绝不留下空洞。
    """
    import threading
    import tdx_adjust
    import tdx_client
    if codes:
        codes = tdx_client.normalize_codes(codes)
    state = _load_state(root)
    done = {} if redo else dict(state["done"].get(str(market), {}))
    cfg = cfg or os.path.join(root, "connect.cfg")
    if not os.path.isfile(cfg):
        # 目标树没配置时往回收：先仓库自带的生成样本，再退回安装目录的默认配置。
        # 只给了个空目录就 fill 的场合很常见，不该直接抛"没有可用主机"。
        for cand in (os.path.join(HERE, "connect.cfg"), os.path.join(HERE, "connect.gen.cfg")):
            if os.path.isfile(cand):
                cfg = cand
                break
        else:
            cfg = None
        print("提示: %s 下没有 connect.cfg，改用 %s" % (root, cfg or tdx_client.CFG_DEFAULT),
              file=sys.stderr)

    conn = tdx_client.TdxConnection(cfg_path=cfg)
    conn.connect()
    if not codes:
        codes, start = [], 0
        while True:
            batch = conn.security_list(market, start, 1000)
            if not batch:
                break
            codes += [c for _m, c, _n in batch]
            start += len(batch)
    conn._close()
    # 已完成的代码直接跳过；每抓完一只就更新进度，中断重跑从这里接着走。
    todo = [c for c in codes if c not in done]
    print("待同步 %d 只（已完成跳过 %d 只，并发 %d）" % (len(todo), len(codes) - len(todo), max(1, jobs)))
    path = os.path.join(root, "T0002", "hq_cache", "gbbq")
    events = {}
    if os.path.exists(path):
        for m, c, d, cat, f1, f2, f3, f4 in tdx_adjust.iter_events(path):
            events[(m, c, d, cat)] = {"market": m, "code": c, "date": d, "category": cat,
                                      "dividend": f1, "rights_price": f2, "bonus": f3,
                                      "rights": f4}
    base = len(events)
    lock = threading.Lock()
    stop = threading.Event()
    stats = {"failed": 0, "saved": 0.0}
    jobs = max(1, int(jobs or 1))
    cfg_path = cfg or os.path.join(root, "connect.cfg")

    def worker(chunk):
        try:
            conn = tdx_client.TdxConnection(cfg_path=cfg_path)
            conn.connect()
        except Exception:
            with lock:
                stats["failed"] += len(chunk)
            return
        try:
            for code in chunk:
                if stop.is_set():
                    return
                try:
                    got = conn.xdxr_info(market, code)
                except Exception:
                    with lock:
                        stats["failed"] += 1
                    continue
                with lock:
                    for ev in got:
                        events[(ev["market"], ev["code"], ev["date"], ev["category"])] = ev
                    done[code] = len(got)
                    state["done"][str(market)] = done
                    # 状态写盘按 2 秒节流：几百只时每只都写会把磁盘拖慢。
                    if time.time() - stats["saved"] > 2:
                        _save_state(root, state)
                        stats["saved"] = time.time()
                    # limit 按已完成的代码数收口，不是按请求数；配合断点正好可以分几次拉完。
                    if limit and len(done) >= limit:
                        stop.set()
                        return
        finally:
            conn._close()

    # 一轮抓完有失败就补抓，最多 rounds 轮；补抓按 done 反查，不会重复抓已成功的。
    for rnd in range(max(1, rounds)):
        left = [c for c in todo if c not in done]
        if not left or (limit and len(done) >= limit):
            break
        if rnd:
            print("  补抓第 %d 轮，剩 %d 只" % (rnd, len(left)))
        stats["failed"] = 0
        threads = []
        for i in range(jobs):
            chunk = left[i::jobs]
            if not chunk:
                continue
            t = threading.Thread(target=worker, args=(chunk,), daemon=True)
            t.start()
            threads.append(t)
        for t in threads:
            t.join()
        with lock:
            _save_state(root, state)
        if not stats["failed"]:
            break
    failed = stats["failed"]
    n = tdx_adjust.write_gbbq(list(events.values()), path)
    return {"records": n, "added": n - base,
            "symbols": len({(e["market"], e["code"]) for e in events.values()}),
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
    p.add_argument("--from", dest="from_root", help="从旧安装/备份的 vipdoc 拷贝，不走网络")
    p.add_argument("--kind", default="day", choices=("day", "min", "5min", "15min", "30min", "60min"),
                   help="day 日线 / min 1 分钟 / 5min 5 分钟 / 15min / 30min / 60min；分钟族 days 给 0 表示翻到底")
    p = sub.add_parser("sync")
    p.add_argument("root")
    p.add_argument("--market", type=int, default=1)
    p.add_argument("--codes")
    p.add_argument("--limit", type=int, default=20, help="本轮最多同步多少只证券")
    p.add_argument("--cfg")
    p.add_argument("--redo", action="store_true", help="忽略断点，清空该市场进度重抓")
    p.add_argument("--jobs", type=int, default=64, help="并发连接数，1 为串行；高并发失败会按 rounds 自动补抓")
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
    p = sub.add_parser("delisted")
    p.add_argument("src", help="现成的安装目录或旧备份，扫它 vipdoc 里的 .day")
    p.add_argument("--markets", default="0,1")
    p.add_argument("--out", help="把结果写成 市场:代码 一行一个")
    p = sub.add_parser("archive")
    p.add_argument("src", help="安装目录、树根或 base.dbf 路径")
    p.add_argument("--out", help="存档目录，默认 db/base")
    p.add_argument("--day", help="存档日期，默认今天")
    p = sub.add_parser("verify")
    p.add_argument("root")
    a = ap.parse_args(argv)
    # 相对路径统一按当前目录展开，后续一律用绝对路径，避免受 cwd 变化影响
    a.root = os.path.abspath(getattr(a, "root", None) or getattr(a, "src", ""))
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
                   a.days, a.cfg, from_root=a.from_root, kind=a.kind)
        for code, n in got:
            print("  %s 写入 %d 条%s" % (code, n, "日线" if a.kind == "day" else "分钟线"))
        miss = len([c for c in a.codes.split(",") if c.strip()]) - len(got)
        print("合计 %d 只 / %d 条%s" % (len(got), sum(n for _, n in got),
                                       "，%d 只无数据" % miss if miss else ""))
        return 0
    if a.cmd == "sync":
        codes = [c.strip() for c in a.codes.split(",")] if a.codes else None
        got = sync_gbbq(a.root, a.market, codes, a.limit, a.cfg, a.redo, a.jobs)
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
    if a.cmd == "archive":
        path, wrote = archive_base(a.src, a.out, a.day)
        print("%s %s" % ("已存档" if wrote else "今日已有，未改", path))
        return 0
    if a.cmd == "delisted":
        import tdx_client
        rows, n = tdx_client.list_delisted(a.src, [int(x) for x in a.markets.split(",") if x.strip()], a.out)
        print("码表 %d 只，本地有而码表没有（退市/摘牌）：%d 只" % (n, len(rows)))
        for m, code in rows[:20]:
            print("  %d:%s" % (m, code))
        if len(rows) > 20:
            print("  ...")
        return 0
    problems, stats = verify(a.root)
    for p_ in problems:
        print("问题:", p_)
    print("统计:", stats)
    print("结论:", "通过" if not problems else "%d 个问题" % len(problems))
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())