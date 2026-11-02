#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
tdx_local.py —— 通达信本地数据资产提取器（只读，纯标准库）

覆盖 Neptune/v11.68 安装目录里所有对量化有意义的离线数据：
证券主表(base.dbf)、行业/地域分类(tdxhy.cfg)、板块与成分(block/*.dat、blocknew/*.blk)、
板块指数(tdxzs.cfg)、横截面指标缓存(tdxzsbase*.cfg)、期货合约规格(code2name.ini)、
期权链(ggqqcode/szqqcode)、交易规则(hqrule.dat)、vipdoc 行情文件(.day/.lc1/.lc5)。

用法示例：
    python tdx_local.py master  --out master.csv
    python tdx_local.py industry --out ind.csv
    python tdx_local.py blocks  --out blocks.json
    python tdx_local.py index   --out index.csv
    python tdx_local.py futures --out fut.csv
    python tdx_local.py options --out opt.csv
    python tdx_local.py snapshot --out snap.csv
    python tdx_local.py rules
    python tdx_local.py files   --out files.txt
    python tdx_local.py --root <数据根> bars --market sh --code 600000 --kind lday --out sh600000.csv

所有路径默认相对 --root（默认取仓库旁的 neptune，可用 TDX_ROOT 覆盖），只读不写安装目录。
"""

import argparse
import base64
import csv
import json
import os
import re
import struct
import sys
import tempfile

# 文件读法：常量与路径助手 → DBF 读写 → 各类资产解析 → 站点配置（解析/生成/自检）→ CLI。
# 所有资产都是只读；唯一会写盘的是 sitecfg-build、base、files/bars 的 --out。
import zlib

# 安装目录：优先 TDX_ROOT，否则取仓库旁的 neptune（相对路径解析，换机可用）
REPO_DIR = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.environ.get("TDX_ROOT")
                       or os.path.join(REPO_DIR, os.pardir, os.pardir, "neptune"))

# ---------------------------------------------------------------- 基础工具

def hq(root):
    return os.path.join(root, "T0002", "hq_cache")


def blk(root):
    return os.path.join(root, "T0002", "blocknew")


def vip(root):
    return os.path.join(root, "vipdoc")


def gbk(b):
    return b.decode("gbk", "replace")


def to_f(s):
    s = (s or "").strip()
    if not s:
        return None
    try:
        return float(s)
    except ValueError:
        return None


def to_i(s):
    v = to_f(s)
    return None if v is None else int(v)


# ---------------------------------------------------------------- DBF

## dBASE III 读取
# 头 32 字节：0 版本、1-3 最后更新日期、4-7 记录数、8-9 头长、10-11 记录长；
# 32 起是每 32 字节一条的字段描述（0-10 名、11 类型、16 长度、17 小数位），0x0D 结束。
def read_dbf(path):
    """读 dBASE III 表，产出 (字段名列表, 行字典)。数值字段按 dec 缩放。"""
    with open(path, "rb") as fh:
        head = fh.read(32)
        if len(head) < 32:
            return [], iter(())
        nrec, hdrlen, reclen = struct.unpack_from("<IHH", head, 4)
        fields = []
        raw = fh.read(hdrlen - 32)
        for i in range(len(raw) // 32):
            d = raw[i * 32:(i + 1) * 32]
            name = d[:11].split(b"\x00")[0].decode("ascii", "replace")
            if not name:
                continue
            fields.append((name, chr(d[11]), d[16], d[17]))
        body = fh.read(nrec * reclen or -1)

    # 记录首字节是删除标记，* 开头的整行跳过；数值列右对齐空格补齐，读时 strip 再转。
    def rows():
        for r in range(nrec):
            off = r * reclen
            rec = body[off:off + reclen]
            if len(rec) < reclen or rec[:1] == b"*":
                continue
            row, pos = {}, 1
            for name, ftype, flen, fdec in fields:
                cell = rec[pos:pos + flen]
                pos += flen
                if ftype in "NF":
                    txt = cell.decode("ascii", "replace").strip()
                    if not txt or txt in ("-", "."):
                        row[name] = None
                    else:
                        try:
                            row[name] = round(float(txt), fdec) if fdec else int(float(txt))
                        except ValueError:
                            row[name] = None
                else:
                    row[name] = gbk(cell).strip()
            yield row

    return [f[0] for f in fields], rows()


BASE_FIELDS = (
    ("SC", "C", 1, 0), ("GPDM", "C", 6, 0), ("GXRQ", "N", 8, 0), ("ZGB", "N", 14, 2),
    ("GJG", "N", 14, 2), ("FQRFRG", "N", 14, 2), ("FRG", "N", 14, 2), ("BG", "N", 14, 2),
    ("HG", "N", 14, 2), ("LTAG", "N", 14, 2), ("ZGG", "N", 14, 2), ("ZPG", "N", 14, 2),
    ("ZZC", "N", 14, 1), ("LDZC", "N", 14, 1), ("GDZC", "N", 14, 1), ("WXZC", "N", 14, 1),
    ("CQTZ", "N", 14, 1), ("LDFZ", "N", 14, 1), ("CQFZ", "N", 14, 1), ("ZBGJJ", "N", 14, 1),
    ("JZC", "N", 14, 1), ("ZYSY", "N", 14, 1), ("ZYLY", "N", 14, 1), ("QTLY", "N", 14, 1),
    ("YYLY", "N", 14, 1), ("TZSY", "N", 14, 1), ("BTSY", "N", 14, 1), ("YYWSZ", "N", 14, 1),
    ("SNSYTZ", "N", 14, 1), ("LYZE", "N", 14, 1), ("SHLY", "N", 14, 1), ("JLY", "N", 14, 1),
    ("WFPLY", "N", 14, 1), ("TZMGJZ", "N", 14, 3), ("DY", "C", 3, 0), ("HY", "C", 4, 0),
    ("ZBNB", "N", 2, 0), ("SSDATE", "C", 8, 0), ("MODIDATE", "C", 6, 0), ("GDRS", "N", 8, 0),
)
# 写出的表固定 0x0D 结束字段区、0x1A 收尾；每条记录首字节写空格（未删除标记）。
def write_dbf(path, fields, rows):
    """写 dBASE III 表：fields 为 (名, 类型, 长度, 小数位)，rows 为对齐后的字符串序列。

    头 32 字节 = 12 字节元信息 + 20 字节保留，随后是 32 字节字段描述、0x0D 结束、
    记录区、0x1A 文件尾。数值字段右对齐空格填充，字符字段左对齐。
    """
    import datetime
    hdrlen = 32 + 32 * len(fields) + 1
    reclen = 1 + sum(f[2] for f in fields)
    today = datetime.date.today()
    with open(path, "wb") as fh:
        fh.write(struct.pack("<BBBBIHH", 3, today.year - 1900, today.month, today.day,
                             len(rows), hdrlen, reclen) + b"\x00" * 20)
        for name, ftype, flen, fdec in fields:
            fh.write(name.encode("ascii").ljust(11, b"\x00") + ftype.encode("ascii")
                     + b"\x00" * 4 + bytes([flen, fdec]) + b"\x00" * 14)
        fh.write(b"\x0d")
        for row in rows:
            fh.write(b" ")
            for (name, ftype, flen, fdec), val in zip(fields, row):
                text = (val or "").encode("gbk", "replace")[:flen]
                fh.write(text.rjust(flen) if ftype in "NF" else text.ljust(flen))
        fh.write(b"\x1a")


# 只填市场与代码，其余 38 列给 0：官方客户端只按这些列认证券，财务列留空也认。
def gen_base(path, rows):
    """按 40 字段生成 base.dbf；rows 只需给 (市场, 代码, 名称)，其余留空。

    名称不进 base.dbf，只用来占位排序，与官方表结构保持一致。
    """
    out = []
    # 名称不写进表，只用来占位；市场进 SC，代码进 GPDM，其余列先填 0。
    for market, code, _name in rows:
        rec = ["0"] * len(BASE_FIELDS)
        rec[0] = str(market)
        rec[1] = code
        out.append(rec)
    write_dbf(path, BASE_FIELDS, out)
    return len(out)


# ---------------------------------------------------------------- 证券主表

def security_master(root):
    """base.dbf：40 字段的证券主表（股本结构、财务快照、上市日、行业/地域码、股东数）。"""
    path = os.path.join(hq(root), "base.dbf")
    fields, rows = read_dbf(path)
    for row in rows:
        row["market"] = to_i(row.get("SC"))
        row["code"] = row.get("GPDM") or ""
        row.pop("SC", None)
        row.pop("GPDM", None)
        yield row


# ---------------------------------------------------------------- 分类

def industry_map(root):
    """tdxhy.cfg：market|code|行业码|?|?|板块码。行业码前缀 T=通达信行业，X=概念/地域板块。"""
    path = os.path.join(hq(root), "tdxhy.cfg")
    with open(path, "rb") as fh:
        for line in fh:
            parts = gbk(line).strip().split("|")
            if len(parts) < 6:
                continue
            yield {
                "market": to_i(parts[0]),
                "code": parts[1],
                "industry": parts[2],
                "flag": parts[3],
                "sub": parts[4],
                "block": parts[5],
            }


def block_index(root):
    """tdxzs.cfg：板块指数（880xxx）定义，名称|代码|类型|?|?|序号。"""
    path = os.path.join(hq(root), "tdxzs.cfg")
    with open(path, "rb") as fh:
        for line in fh:
            parts = gbk(line).strip().split("|")
            if len(parts) < 6 or not parts[1]:
                continue
            yield {"name": parts[0], "code": parts[1], "kind": to_i(parts[2]),
                   "a": to_i(parts[3]), "b": to_i(parts[4]), "seq": to_i(parts[5])}


# ---------------------------------------------------------------- 板块成分

BLOCK_SYNTAX = (
    ("hash_csv", "infoharbor_block.dat"),   # #名,..头部 + m#code 逗号分隔
    ("hash_line7", "spblock.dat"),          # #名 + 7 位 mcode 每行
    ("hash_line", "hkblock.dat"),
    ("hash_line", "mgblock.dat"),
    ("hash_line", "ukblock.dat"),
    ("hash_line", "sbblock.dat"),
    ("hash_line", "jgblock.dat"),
    ("comma", "jjblock.dat"),               # #名 + market,code
    ("csi", "csiblock.dat"),                # #名 + 62,codeCNY01
)


# 板块成员有三种写法：裸代码（按 addcode 语法补市场）、带市场前缀、带交易所后缀。
def _parse_member(tok, syntax):
    tok = tok.strip()
    if not tok:
        return None
    if syntax == "hash_csv":
        if "#" not in tok:
            return None
        m, c = tok.split("#", 1)
        return to_i(m), c
    if syntax == "comma" or syntax == "csi":
        parts = tok.split(",")
        if len(parts) < 2:
            return None
        code = parts[1]
        if syntax == "csi":
            for suf in ("CNY01", "CNY010"):
                if code.endswith(suf):
                    code = code[: -len(suf)]
                    break
        return to_i(parts[0]), code
    if syntax == "hash_line7":
        if len(tok) >= 7 and tok[:-6].isdigit():
            return to_i(tok[:-6]), tok[-6:]
        return None
    return None  # hash_line 只有代码，市场未知


def _resolve(syntax, fname):
    for s, f in BLOCK_SYNTAX:
        if f == fname:
            return s
    return None


def blocks(root):
    """全部板块成分。未知语法(纯代码行)按 market=None 返回，市场由调用方按文件推断。"""
    d = hq(root)
    for fname in sorted(os.listdir(d)):
        if not fname.endswith(".dat") or "block" not in fname:
            continue
        syntax = _resolve(syntax=None, fname=fname) if False else _resolve(None, fname)
        if syntax is None:
            continue
        name, members = None, []
        with open(os.path.join(d, fname), "rb") as fh:
            for line in fh:
                txt = gbk(line).strip()
                if not txt:
                    continue
                if txt.startswith("#"):
                    if name is not None:
                        yield {"file": fname, "name": name, "members": members}
                    name, members = txt[1:], []
                    if syntax == "hash_csv":
                        cut = name.split(",")[0]
                        name = cut
                        rest = txt[1:].split(",", 1)
                        if len(rest) > 1:
                            for tok in rest[1].split(","):
                                mm = _parse_member(tok, syntax)
                                if mm:
                                    members.append({"market": mm[0], "code": mm[1]})
                    continue
                for tok in txt.split(",") if syntax in ("hash_csv", "comma", "csi") else [txt]:
                    mm = _parse_member(tok, syntax)
                    if mm:
                        members.append({"market": mm[0], "code": mm[1]})
        if name is not None:
            yield {"file": fname, "name": name, "members": members}


def custom_blocks(root):
    """blocknew/*.blk：自选与自定义板块，行内容为 market+code 文本。"""
    d = blk(root)
    if not os.path.isdir(d):
        return
    for fname in sorted(os.listdir(d)):
        if not fname.lower().endswith(".blk"):
            continue
        out = []
        with open(os.path.join(d, fname), "rb") as fh:
            for line in fh:
                txt = gbk(line).strip()
                if len(txt) >= 7 and txt[:-6].isdigit():
                    out.append({"market": to_i(txt[:-6]), "code": txt[-6:]})
        yield {"file": fname, "name": os.path.splitext(fname)[0], "members": out}


# ---------------------------------------------------------------- 期货 / 期权

def futures_specs(root):
    """code2name.ini：期货合约规格。code,name,交易所,?,?,?,乘数,保证金率,最小变动,?,?,币种,报价单位,..."""
    path = os.path.join(hq(root), "code2name.ini")
    if not os.path.exists(path):
        return
    with open(path, "rb") as fh:
        for line in fh:
            p = gbk(line).strip().split(",")
            if len(p) < 12:
                continue
            yield {
                "code": p[0], "name": p[1], "exchange": p[2],
                "multiplier": to_f(p[6]), "margin_rate": to_f(p[7]),
                "tick_size": to_f(p[8]), "limit_up": to_f(p[9]), "limit_dn": to_f(p[10]),
                "currency": p[11], "unit": p[12] if len(p) > 12 else "",
                "delivery_rule": p[-1],
            }


OPT_COLS = ("id", "code_full", "name", "underlying", "strike_code", "month",
            "strike", "qty", "strike2", "start", "end", "days")


def options(root):
    """ggqqcode/szqqcode：期权链（沪/深）。首行为 日期,数量。"""
    for fname in ("ggqqcode.txt", "szqqcode.txt"):
        path = os.path.join(hq(root), fname)
        if not os.path.exists(path):
            continue
        with open(path, "rb") as fh:
            for line in fh:
                p = gbk(line).strip().split(",")
                if len(p) < 12 or not p[0].isdigit():
                    continue
                rec = dict(zip(OPT_COLS, p[:12]))
                rec["market"] = 1 if fname.startswith("gg") else 0
                yield rec


# ---------------------------------------------------------------- 横截面缓存 / 规则

# tdxzsbase.cfg 是单日横截面：26 列定长，raw 拆成 cols 计数，不做字段语义推断。
def snapshot(root):
    """tdxzsbase.cfg：逐股横截面指标缓存（流通市值/总市值/换手/涨跌…，列语义见文档）。"""
    path = os.path.join(hq(root), "tdxzsbase.cfg")
    if not os.path.exists(path):
        return
    with open(path, "rb") as fh:
        for line in fh:
            p = gbk(line).strip().split("|")
            if len(p) < 10:
                continue
            yield {"market": to_i(p[0]), "code": p[1], "date": p[7], "raw": p}


def rules(root):
    """hqrule.dat：交易规则常量（涨跌停比例、申报上限、生效门槛等）。"""
    path = os.path.join(hq(root), "hqrule.dat")
    out = {}
    if not os.path.exists(path):
        return out
    with open(path, "rb") as fh:
        for line in fh:
            txt = gbk(line).strip()
            if not txt or txt.startswith("["):
                continue
            if "=" in txt:
                k, v = txt.split("=", 1)
                out[k.strip()] = v.strip()
    return out


# ---------------------------------------------------------------- 本地行情文件

def vipdoc_files(root):
    for market in ("sh", "sz", "bj", "ds"):
        for sub in ("lday", "minline", "fzline", "eday"):
            d = os.path.join(vip(root), market, sub)
            if not os.path.isdir(d):
                continue
            for name in sorted(os.listdir(d)):
                p = os.path.join(d, name)
                yield {"market": market, "kind": sub, "file": name,
                       "size": os.path.getsize(p), "path": p}


def read_day(path):
    """vipdoc/*/lday/*.day：32 字节记录，u32 日期 + u32 开高低收(×100) + float 额 + u32 量 + u32 保留。"""
    with open(path, "rb") as fh:
        while True:
            rec = fh.read(32)
            if len(rec) < 32:
                return
            d, o, h, l, c, amt, vol, _ = struct.unpack("<IIIIIfII", rec)
            yield {"date": "%04d-%02d-%02d" % (d // 10000, d // 100 % 100, d % 100),
                   "open": o / 100.0, "high": h / 100.0, "low": l / 100.0,
                   "close": c / 100.0, "amount": amt, "volume": vol}


def read_lc(path, minutes):
    """vipdoc/*/minline/*.lc1（1 分）与 fzline/*.lc5（5 分）：32 字节记录。"""
    with open(path, "rb") as fh:
        while True:
            rec = fh.read(32)
            if len(rec) < 32:
                return
            zipday, mnt, o, h, l, c, amt, vol, _ = struct.unpack("<HHfffffII", rec)
            y = (zipday >> 11) + 2004
            mo = (zipday % 2048) // 100
            dd = (zipday % 2048) % 100
            yield {"datetime": "%04d-%02d-%02d %02d:%02d" % (y, mo, dd, mnt // 60, mnt % 60),
                   "open": round(o / minutes, 4), "high": round(h / minutes, 4),
                   "low": round(l / minutes, 4), "close": round(c / minutes, 4),
                   "amount": amt, "volume": vol}



# ---------------------------------------------------------------- 站点配置（connect.cfg / newhost.lst）

_CFG_B64 = (
    "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"
)
_CFG_JSON = json.loads(zlib.decompress(base64.b64decode(_CFG_B64)).decode("utf-8"))
SITECFG_ORDER = _CFG_JSON["order"]
SITECFG_DEFAULTS = _CFG_JSON["sections"]

HOST_SECTIONS = ("HQHOST", "TRYHQHOST", "INFOHOST", "WTHOST", "DSHOST")
SINGLE_HOSTS = ("CHATHOST", "ICQHOST")
HOST_FIELDS = ("HostName", "IPAddress", "Port", "Weight", "Areas", "HostType")
OTHERHOST_KEYS = ("L2PrimaryHost", "SSDSPrimaryHost", "SSDS2PrimaryHost", "SSDS3PrimaryHost",
                  "VipHQPrimaryHost", "VipHQ2PrimaryHost")


def _is_ip(text):
    """接受 IPv4、不带方括号的 IPv6；域名也放行（部分券商确实用域名）。"""
    t = (text or "").strip()
    if not t:
        return False
    if re.fullmatch(r"\d{1,3}(\.\d{1,3}){3}", t):
        return all(0 <= int(x) <= 255 for x in t.split("."))
    if ":" in t and re.fullmatch(r"[0-9A-Fa-f:]+", t):
        return t.count(":") >= 2
    return bool(re.fullmatch(r"[A-Za-z0-9.-]+\.[A-Za-z]{2,}", t))


# 解析只做结构校验，不改写：错误是会阻断重建的硬伤，警告是现实里存在但可自愈的写法。
def parse_sitecfg(path):
    """解析站点配置，返回 (分区, 站点表, 错误, 警告)；只读。

    错误 = 会阻断重建的问题（分区缺失/地址端口非法/段内重复/编号与 HostNum 不符）；
    警告 = 现实中存在但可自愈的写法（跨段重复、缺 HostNum 可推断、OTHERHOST 引用越界）。
    """
    txt = open(path, "rb").read().decode("gbk", "replace")
    order, sections, errors, warns = [], {}, [], []
    cur = None
    for no, line in enumerate(txt.splitlines(), 1):
        s = line.strip()
        if s.startswith("[") and s.endswith("]"):
            cur = s[1:-1]
            if cur in sections:
                errors.append("L%d 重复分区 [%s]" % (no, cur))
            order.append(cur)
            sections[cur] = {}
            continue
        if cur is None:
            if s:
                errors.append("L%d 分区外的行: %s" % (no, s[:40]))
            continue
        if "=" in s:
            k, v = s.split("=", 1)
            sections[cur][k] = v
        elif s:
            warns.append("L%d 空行/无法解析: %s" % (no, s[:40]))
    for sec in SITECFG_ORDER:
        if sec not in sections:
            errors.append("缺分区 [%s]" % sec)
    for sec in sections:
        if sec not in SITECFG_ORDER:
            errors.append("未知分区 [%s]" % sec)

    # 编号键取各分区里出现的两位数字后缀；HostNum 与编号组数不符、或编号不连续都算错误，
    # 缺 HostNum 时按最大编号推断并降级为警告。
    hosts, seen = [], {}
    # 每个主机段按 IPAddressNN 编号走一遍，地址非法、缺名、端口越界都记错误。
    for sec in HOST_SECTIONS:
        kv = sections.get(sec, {})
        idx = sorted({int(m.group(1)) for k in kv for m in [re.fullmatch(r".*(\d{2})", k)] if m})
        n_raw = kv.get("HostNum", "").strip()
        if n_raw.isdigit():
            n = int(n_raw)
            if idx and idx != list(range(1, len(idx) + 1)):
                errors.append("[%s] 编号不连续，残留 %s" % (sec, [i for i in idx if i > n][:8]))
            if n != len(idx):
                errors.append("[%s] HostNum=%d 但编号键有 %d 组" % (sec, n, len(idx)))
        elif idx:
            n = max(idx)
            warns.append("[%s] 缺 HostNum，按最大编号推断为 %d" % (sec, n))
        else:
            n = 0
            warns.append("[%s] 空段" % sec)
        for i in range(1, n + 1):
            tag = "%02d" % i
            ip = kv.get("IPAddress" + tag, "").strip()
            name = kv.get("HostName" + tag, "").strip()
            port = kv.get("Port" + tag, "").strip()
            weight = kv.get("Weight" + tag, "90").strip()
            if not _is_ip(ip):
                errors.append("[%s] %s 地址非法: %r" % (sec, tag, ip))
            if not name:
                warns.append("[%s] %s 缺名称" % (sec, tag))
            if not (port.isdigit() and 1 <= int(port) <= 65535):
                errors.append("[%s] %s 端口非法: %r" % (sec, tag, port))
            if not weight.lstrip("-").isdigit():
                warns.append("[%s] %s 权重非法: %r" % (sec, tag, weight))
            dup = seen.get((ip, port))
            if dup and ip and dup.startswith(sec + "/"):
                errors.append("[%s] %s 段内重复 (%s:%s)" % (sec, tag, ip, port))
            elif dup and ip:
                warns.append("[%s] %s 与 %s 重复 (%s:%s)" % (sec, tag, dup, ip, port))
            if ip:
                seen.setdefault((ip, port), "%s/%s" % (sec, tag))
            hosts.append({"section": sec, "index": i, "name": name, "ip": ip,
                          "port": int(port) if port.isdigit() else None,
                          "weight": int(weight) if weight.lstrip("-").isdigit() else None,
                          "areas": kv.get("Areas" + tag, "")})
        ph = kv.get("PrimaryHost", "")
        if ph.lstrip("-").isdigit() and n and int(ph) > n:
            errors.append("[%s] PrimaryHost=%s 超出 HostNum=%d" % (sec, ph, n))

    for sec in SINGLE_HOSTS:
        kv = sections.get(sec, {})
        ip = kv.get("IPAddress", "").strip()
        port = kv.get("Port", "").strip()
        if ip and not _is_ip(ip):
            errors.append("[%s] 地址非法: %r" % (sec, ip))
        if port and not (port.isdigit() and 1 <= int(port) <= 65535):
            errors.append("[%s] 端口非法: %r" % (sec, port))
        if ip:
            hosts.append({"section": sec, "index": 1, "name": "", "ip": ip,
                          "port": int(port) if port.isdigit() else None, "weight": None, "areas": ""})

    for k in OTHERHOST_KEYS:
        v = sections.get("OTHERHOST", {}).get(k)
        if v is None:
            warns.append("[OTHERHOST] 缺 %s" % k)
        elif not re.fullmatch(r"-?\d+", v.strip()):
            errors.append("[OTHERHOST] %s 非整数: %r" % (k, v))
        elif int(v.strip()) > 60:
            warns.append("[OTHERHOST] %s=%s 引用可能越界" % (k, v))
    return {"order": order, "sections": sections, "hosts": hosts,
            "errors": errors, "warns": warns}


# 生成结果先落临时文件再走同一套解析器回读，凭错误/警告数判断这次生成是否可用。
def _selfcheck(text):
    """写到系统临时文件再回读，仓库只读时也能自检。"""
    fd, tmp = tempfile.mkstemp(suffix=".cfg", prefix="tdx_sitecfg_")
    try:
        with os.fdopen(fd, "wb") as fh:
            fh.write(text.encode("gbk", "replace"))
        got = parse_sitecfg(tmp)
    finally:
        try:
            os.remove(tmp)
        except OSError:
            pass
    return got["errors"], got["warns"]


def _fill_hosts(sections, sec, items, default_name, default_port, primary=0):
    kv = sections.setdefault(sec, {})
    for k in list(kv):
        if k.startswith(HOST_FIELDS):
            del kv[k]
    kv["HostNum"] = str(len(items))
    ph = primary if (isinstance(primary, int) and 0 <= primary < len(items)) else 0
    kv["PrimaryHost"] = str(ph)
    for i, h in enumerate(items, 1):
        tag = "%02d" % i
        kv["HostName" + tag] = str(h.get("name") or "%s%d" % (default_name, i))
        kv["IPAddress" + tag] = str(h["ip"]).strip()
        kv["Port" + tag] = str(int(h.get("port") or default_port))
        if sec in ("HQHOST", "TRYHQHOST") or h.get("weight"):
            kv["Weight" + tag] = str(int(h.get("weight") or 90))
        if h.get("areas") or sec in ("HQHOST", "TRYHQHOST", "INFOHOST", "DSHOST"):
            kv["Areas" + tag] = str(h.get("areas") or "")
        if h.get("hosttype") is not None:
            kv["HostType" + tag] = str(h["hosttype"])


# 生成顺序固定按 SITECFG_ORDER，逐分区逐键写；spec 缺的键回落到 SITECFG_DEFAULTS，
# UserHost 的 IPAddress/Port 再空就取 SEED_USERHOST，保证登录落点永远有值。
# spec 里 hosts 是主站、try_hosts 是候选站、其他段各管一摊；每段写完都要回填 HostNum。
def build_sitecfg(spec):
    """从零重建 connect.cfg 文本；缺省键取自内置模板，自检有错则抛 ValueError。

    spec: identity{键:值} / hosts[{name,ip,port,weight}] / try_hosts / ds_hosts /
          single{CHATHOST:{ip,port}, ICQHOST:{...}} / otherhost{键:整数} / primary / sections{}
    """
    spec = dict(spec or {})
    hosts = [h for h in (spec.get("hosts") or []) if (h or {}).get("ip")]
    if not hosts:
        raise ValueError("spec.hosts 为空，至少要一台行情主站")

    sections = {sec: dict(kv) for sec, kv in SITECFG_DEFAULTS.items()}
    for kv in sections.values():
        for k in list(kv):
            if re.fullmatch(r".*\d{2}", k):
                del kv[k]

    # 登录落点即使 spec 没写也要补种子，否则生成物 UserHost.IPAddress 为空
    uh = sections.setdefault("UserHost", {})
    for k, v in SEED_USERHOST.items():
        if not str(uh.get(k, "")).strip():
            uh[k] = v

    _fill_hosts(sections, "HQHOST", hosts, "行情主站", 7709, spec.get("primary", 0))
    _fill_hosts(sections, "TRYHQHOST", [h for h in (spec.get("try_hosts") or []) if (h or {}).get("ip")], "候选站", 7709)
    _fill_hosts(sections, "DSHOST", [h for h in (spec.get("ds_hosts") or []) if (h or {}).get("ip")], "数据站", 7720)
    _fill_hosts(sections, "INFOHOST", [h for h in (spec.get("info_hosts") or []) if (h or {}).get("ip")], "资讯站", 7721)
    _fill_hosts(sections, "WTHOST", [h for h in (spec.get("wt_hosts") or []) if (h or {}).get("ip")], "交易站", 7711)

    for sec, h in (spec.get("single") or {}).items():
        if sec not in SINGLE_HOSTS or not (h or {}).get("ip"):
            continue
        sections.setdefault(sec, {})["IPAddress"] = str(h["ip"]).strip()
        sections[sec]["Port"] = str(int(h.get("port") or 7710))
    for k, v in (spec.get("otherhost") or {}).items():
        sections.setdefault("OTHERHOST", {})[str(k)] = str(int(v))

    for sec, kv in (spec.get("sections") or {}).items():
        if sec in HOST_SECTIONS:
            continue
        sections.setdefault(sec, {}).update({str(k): str(v) for k, v in (kv or {}).items()})
    for k, v in (spec.get("identity") or {}).items():
        target = "Other" if k in sections["Other"] else ("user" if k in sections["user"] else "UserHost")
        sections[target][str(k)] = str(v)

    lines = []
    for sec in SITECFG_ORDER:
        lines.append("[%s]" % sec)
        for k, v in sections.get(sec, {}).items():
            lines.append("%s=%s" % (k, v))
        lines.append("")
    text = "\r\n".join(lines)
    errors, _warns = _selfcheck(text)
    if errors:
        raise ValueError("生成结果自检失败：" + "；".join(errors[:6]))
    return text


def default_spec(path=None):
    """可用起始 spec：身份/链路取自参照安装，主站用第 10 章实连验证过的四台。"""
    ident, ds, info, wt, try_, single, other = {}, [], [], [], [], {}, {}
    if path and os.path.exists(path):
        got = parse_sitecfg(path)
        kv = got["sections"].get("Other", {})
        for k in ("Corp", "PreName", "TitleName", "WTPreNAME", "SysMark", "LogoAdvert",
                  "CorpWeb", "AutoUpZip", "AutoupURLDir", "OtherFileName", "HasDS", "HasSSL"):
            if k in kv:
                ident[k] = kv[k]
        for k in ("QSID", "CloudQSID"):
            if k in got["sections"].get("UserHost", {}):
                ident[k] = got["sections"]["UserHost"][k]
            if k in got["sections"].get("user", {}):
                ident[k] = got["sections"]["user"][k]
        for h in got["hosts"]:
            if h["section"] in ("DSHOST", "INFOHOST", "WTHOST", "TRYHQHOST"):
                bucket = (info if h["section"] == "INFOHOST" else wt if h["section"] == "WTHOST"
                          else try_ if h["section"] == "TRYHQHOST" else ds)
                rec = {"name": h["name"] or "", "ip": h["ip"], "port": h["port"],
                       "weight": h["weight"] if h["section"] in ("TRYHQHOST",) else None,
                       "areas": h.get("areas", "")}
                ht = got["sections"].get(h["section"], {}).get("HostType%02d" % h["index"], "")
                if ht:
                    rec["hosttype"] = ht
                bucket.append(rec)
            elif h["section"] in SINGLE_HOSTS and h["ip"]:
                single[h["section"]] = {"ip": h["ip"], "port": h["port"] or 7710}
        for k in OTHERHOST_KEYS:
            v = got["sections"].get("OTHERHOST", {}).get(k)
            if v is not None and re.fullmatch(r"-?\d+", v.strip()):
                other[k] = int(v)
    base = seed_spec()
    if not try_:
        try_ = base["try_hosts"]
    if not ds:
        ds = base["ds_hosts"]
    if not info:
        info = base["info_hosts"]
    if not wt:
        wt = base["wt_hosts"]
    if not other:
        other = base["otherhost"]
    if not single:
        single = dict(SEED_USERHOST and {"UserHost": dict(SEED_USERHOST)})
    return {"identity": ident,
            "hosts": base["hosts"],
            "try_hosts": try_, "ds_hosts": ds, "info_hosts": info, "wt_hosts": wt,
            "single": single, "otherhost": other,
            "primary": 0, "sections": {"UserHost": dict(SEED_USERHOST)}}


# ---------------------------------------------------------------- CLI

SEED_HQ = (("123.125.108.101", 7709), ("114.141.177.118", 7709),
           ("27.151.2.90", 7709), ("202.100.166.12", 7709),
           ("114.117.72.207", 7709), ("182.118.8.9", 7709))
SEED_TRY = (("114.117.72.207", 7709), ("114.141.177.118", 7709), ("27.151.2.90", 7709),
            ("202.100.166.12", 7709), ("183.201.231.85", 7709))
SEED_INFO = (("114.117.72.207", 7711), ("1.202.143.37", 7711), ("202.100.166.88", 7711),
             ("114.141.177.75", 7711))
SEED_WT = (("61.135.173.138", 7708), ("211.100.23.198", 7708), ("61.151.252.170", 7708))
SEED_DS = (("114.117.72.207", 7720), ("118.31.28.30", 7730), ("114.141.177.118", 7720))
SEED_USERHOST = {"IPAddress": "114.117.72.207", "Port": "7715"}


def seed_spec():
    """纯内置站点表，零外部依赖的最小可用 spec。"""
    def mk(rows, weight=False):
        # 不写 name，交给 _fill_hosts 按分区生成「行情主站1」这类可读名字
        return [{"ip": ip, "port": p, **({"weight": 90} if weight else {})}
                for ip, p in rows]
    return {"identity": {}, "hosts": mk(SEED_HQ, True), "try_hosts": mk(SEED_TRY, True),
            "ds_hosts": mk(SEED_DS), "info_hosts": mk(SEED_INFO), "wt_hosts": mk(SEED_WT),
            "single": {}, "otherhost": {k: -1 for k in OTHERHOST_KEYS},
            "primary": 0, "sections": {"UserHost": dict(SEED_USERHOST)}}


def write_csv(rows, out, fields=None):
    rows = list(rows)
    if not rows:
        print("空结果")
        return
    fields = fields or list(rows[0].keys())
    if out:
        out = os.path.abspath(out)
        with open(out, "w", newline="", encoding="utf-8-sig") as fh:
            w = csv.DictWriter(fh, fieldnames=fields, extrasaction="ignore")
            w.writeheader()
            w.writerows(rows)
        print("%d 行 -> %s" % (len(rows), out))
    else:
        for r in rows[:10]:
            print(r)
        print("... 共 %d 行" % len(rows))


def handle_base(a):
    if a.cmd == "base":
        import tdx_client
        conn = tdx_client.TdxConnection(cfg_path=a.cfg)
        conn.connect()
        rows = []
        try:
            for market in [int(x) for x in a.markets.split(",") if x.strip()]:
                start, got = 0, 0
                while True:
                    batch = conn.security_list(market, start, 1000)
                    if not batch:
                        break
                    for _m, code, name in batch:
                        rows.append((market, code, name))
                        got += 1
                        if a.limit and got >= a.limit:
                            break
                    start += len(batch)
                    if a.limit and got >= a.limit:
                        break
        finally:
            conn._close()
        rows.sort(key=lambda r: (r[0], r[1]))
        n = gen_base(a.out, rows)
        print("生成 %s：%d 条（市场 %s）" % (a.out, n, a.markets))
    return 0




def main(argv=None):
    ap = argparse.ArgumentParser(description="通达信本地数据资产提取器")
    ap.add_argument("--root", default=ROOT)
    sub = ap.add_subparsers(dest="cmd", required=True)
    for name in ("master", "industry", "index", "futures", "options", "snapshot"):
        p = sub.add_parser(name)
        p.add_argument("--out")
        p.add_argument("--limit", type=int, default=0)
    p = sub.add_parser("blocks")
    p.add_argument("--out")
    p.add_argument("--custom", action="store_true")
    p = sub.add_parser("base")
    p.add_argument("--out", required=True)
    p.add_argument("--markets", default="0,1")
    p.add_argument("--limit", type=int, default=0, help="每市场最多多少只，0 为全部")
    p.add_argument("--cfg")
    p = sub.add_parser("sitecfg-parse")
    p.add_argument("--path", default="connect.cfg")
    p.add_argument("--hosts-out")
    p = sub.add_parser("sitecfg-check")
    p.add_argument("--path", default="connect.cfg")
    p = sub.add_parser("sitecfg-build")
    p.add_argument("--spec", required=True)
    p.add_argument("--out", default="connect.new.cfg")
    p = sub.add_parser("sitecfg-defaults")
    p.add_argument("--ref", default="connect.cfg")
    p.add_argument("--out", default="sitecfg_spec.json")
    p = sub.add_parser("rules")
    p.add_argument("--out")
    p = sub.add_parser("files")
    p.add_argument("--out")
    p = sub.add_parser("bars")
    p.add_argument("--market", default="sh")
    p.add_argument("--code", required=True)
    p.add_argument("--kind", default="lday", choices=("lday", "minline", "fzline"))
    p.add_argument("--out")
    a = ap.parse_args(argv)
    if a.cmd == "base":
        return handle_base(a)

    r = os.path.abspath(a.root)
    if a.cmd == "master":
        rows = list(security_master(r))
        write_csv(rows[: a.limit] if a.limit else rows, a.out)
    elif a.cmd == "industry":
        rows = list(industry_map(r))
        write_csv(rows[: a.limit] if a.limit else rows, a.out)
    elif a.cmd == "index":
        rows = list(block_index(r))
        write_csv(rows[: a.limit] if a.limit else rows, a.out)
    elif a.cmd == "futures":
        rows = list(futures_specs(r))
        write_csv(rows[: a.limit] if a.limit else rows, a.out)
    elif a.cmd == "options":
        rows = list(options(r))
        write_csv(rows[: a.limit] if a.limit else rows, a.out)
    elif a.cmd == "snapshot":
        rows = list(snapshot(r))
        write_csv([{k: v for k, v in x.items() if k != "raw"} | {"cols": len(x["raw"])} for x in rows], a.out)
    elif a.cmd == "blocks":
        rows = list(custom_blocks(r) if a.custom else blocks(r))
        summary = [{"file": b["file"], "name": b["name"], "count": len(b["members"])} for b in rows]
        if a.out:
            with open(a.out, "w", encoding="utf-8") as fh:
                json.dump(rows, fh, ensure_ascii=False, indent=1)
            print("%d 个板块 -> %s" % (len(rows), a.out))
        else:
            for s in summary[:20]:
                print(s)
            print("... 共 %d 个板块" % len(rows))
    elif a.cmd in ("sitecfg-parse", "sitecfg-check"):
        path = a.path if os.path.isabs(a.path) else os.path.abspath(a.path)
        if not os.path.exists(path):
            print("缺文件", path)
            return 2
        got = parse_sitecfg(path)
        if a.cmd == "sitecfg-check":
            for e in got["errors"]:
                print("错误:", e)
            for w in got["warns"]:
                print("警告:", w)
            print("结论:", "通过" if not got["errors"] else "错误 %d 个" % len(got["errors"]))
            return 1 if got["errors"] else 0
        print("分区 %d 个，站点 %d 条" % (len(got["sections"]), len(got["hosts"])))
        for h in got["hosts"]:
            print("  [%s] %02d %-18s %s:%s w=%s" % (h["section"], h["index"], h["name"], h["ip"], h["port"], h["weight"]))
        if a.hosts_out:
            write_csv(got["hosts"], a.hosts_out)
        for e in got["errors"]:
            print("错误:", e)
        for w in got["warns"]:
            print("警告:", w)
    elif a.cmd == "sitecfg-build":
        spec = json.load(open(a.spec, encoding="utf-8"))
        text = build_sitecfg(spec)
        out = a.out if os.path.isabs(a.out) else os.path.abspath(a.out)
        with open(out, "wb") as fh:
            fh.write(text.encode("gbk", "replace"))
        got = parse_sitecfg(out)
        print("已生成 %s：%d 分区 / 主站 %d / 数据站 %d / 错误 %d / 警告 %d"
              % (out, len(SITECFG_ORDER), sum(1 for h in got["hosts"] if h["section"] == "HQHOST"),
                 sum(1 for h in got["hosts"] if h["section"] == "DSHOST"),
                 len(got["errors"]), len(got["warns"])))
    elif a.cmd == "sitecfg-defaults":
        ref = a.ref if os.path.isabs(a.ref) else os.path.abspath(a.ref)
        out = a.out if os.path.isabs(a.out) else os.path.abspath(a.out)
        with open(out, "w", encoding="utf-8") as fh:
            json.dump(default_spec(ref), fh, ensure_ascii=False, indent=2)
        print("已写出起始 spec:", out)
    elif a.cmd == "rules":
        lines = ["%s = %s" % (k, v) for k, v in sorted(rules(r).items())]
        if a.out:
            out = os.path.abspath(a.out)
            with open(out, "w", encoding="utf-8") as fh:
                fh.write("\n".join(lines) + "\n")
            print("%d 键 -> %s" % (len(lines), out))
        else:
            for line in lines:
                print(line)
    elif a.cmd == "files":
        rows = [x for x in vipdoc_files(r) if x["kind"] == "lday"]
        if a.out:
            out = os.path.abspath(a.out)
            with open(out, "w", encoding="utf-8") as fh:
                for x in rows:
                    fh.write("%s/%s/%s %d\n" % (x["market"], x["kind"], x["file"], x["size"]))
            print("%d 个文件 -> %s" % (len(rows), out))
        else:
            print("%d 个日线文件" % len(rows))
    elif a.cmd == "bars":
        pre = {"sh": "sh", "sz": "sz", "bj": "bj", "ds": "ds"}.get(a.market, a.market)
        sub_dir = {"lday": "lday", "minline": "minline", "fzline": "fzline"}[a.kind]
        path = os.path.join(vip(r), pre, sub_dir, "%s%s.%s" % (
            pre, a.code, "day" if a.kind == "lday" else ("lc1" if a.kind == "minline" else "lc5")))
        if not os.path.exists(path):
            print("缺文件", path)
            return 2
        rows = list(read_day(path) if a.kind == "lday" else read_lc(path, 1 if a.kind == "minline" else 5))
        write_csv(rows, a.out)
    return 0




if __name__ == "__main__":
    sys.exit(main())
    sys.exit(main())