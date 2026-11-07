#!/usr/bin/env python3
# -*- coding: utf-8 -*-
r"""通达信 7709 行情客户端（协议依据：TDX_接口逆向分析报告.md 第 3 章）

只用标准库。默认从安装目录的 connect.cfg 主机表选优连接（TDX_CFG 可覆盖）。

- 帧头 10 字节：0x0C | 类别(u16) | 子版本(u16) | 1 | len(u16) | len(u16) | 载荷
- 载荷首字段是 commtype(u16)
- 响应：16 字节头 <IIIHH>，末两字段为 zipsize/unzipsize，不等则 zlib 解压
- 实测该服务端每连接只服务一条业务请求，故失败即自动重连重试

用法：
    python tdx_client.py smoke
    python tdx_client.py count    --market 1
    python tdx_client.py codes    --market 1 --start 0 --count 100
    python tdx_client.py bars     --market 1 --code 600000 --category 9 --count 100 --out x.csv
    python tdx_client.py minute   --market 1 --code 600000
    python tdx_client.py tick     --market 1 --code 600000 --start 0 --count 50
    python tdx_client.py local    --market 1 --code 600000 --out x.csv
    python tdx_client.py info-hello           # 资讯口问候帧（INFOHOST，不吃登录态）
"""
import argparse, collections, configparser, csv, json, os, re, socket, struct, sys, threading, time, zlib

BASE_DIR = os.path.dirname(os.path.abspath(__file__))
# 安装目录用相对路径推导，TDX_ROOT 或 TDX_CFG 可覆盖
NP_DEFAULT = os.path.abspath(os.environ.get("TDX_ROOT") or BASE_DIR)
CFG_DEFAULT = os.environ.get("TDX_CFG") or os.path.join(NP_DEFAULT, "connect.cfg")
VIPDOC_DEFAULT = os.path.join(NP_DEFAULT, "vipdoc")

MARKET_DIR = {0: "sz", 1: "sh", 2: "bj"}
PREFIX = {0: "sz", 1: "sh", 2: "bj"}

CATEGORY = {
    0: "5m", 1: "15m", 2: "30m", 3: "60m", 4: "day", 5: "week",
    6: "month", 7: "1m", 8: "1m", 9: "day", 10: "quarter", 11: "year", 12: "xrday",
}

CT_BARS, CT_MINUTE, CT_TICK = 0x052D, 0x051D, 0x0FC5
CAT_BARS, CAT_MINUTE, CAT_TICK = 0x01, 0x1B, 0x17
VER_BARS, VER_MINUTE, VER_TICK = 0x0164, 0x0100, 0x0101


# 文件读法：常量 → 编码函数（价格/成交量/日期）→ 帧与连接类 → 本地读取 → CLI。
# 改协议常量前先看第 3 章的表，CAT_ 与 VER_ 与 CT_ 是三组必须成对改的东西。

# 文件读法：常量 → 编码函数（价格/成交量/日期）→ 帧与连接类 → 本地读取 → CLI。
# 改协议常量前先看第 3 章的表，CAT_ 与 VER_ 与 CT_ 是三组必须成对改的东西。
MAX_INFLIGHT = 5


## 价格编码（服务端所有价格字段都走这里）
# 变长整数：首字节低 6 位是最低位，bit6 是符号，bit7 是续位标记；
# 后续字节每多一个左移 7 位。返回值已乘 1000，调用方再决定是否 ÷100。
def get_price(buf, pos):
    """可变长带符号整数：bit6 符号位，bit7 续位，低 6 位起算，7bit 步进。"""
    n = buf[pos]
    val = n & 0x3F
    neg = bool(n & 0x40)
    # 续位为 1 就继续读，位移每次加 7；越界由外层保证不越界。
    shift = 6
    while n & 0x80:
        pos += 1
        n = buf[pos]
        val += (n & 0x7F) << shift
        shift += 7
    pos += 1
    return (-val if neg else val), pos


## 成交量编码
# 4 字节对数浮点：高字节是指数（带 -0x7F 偏置），低三字节拆成三段尾数，
# 按 2 的幂逐段加权后相加。顺序与还原出的汇编一一对应，别随手重排。
def get_volume(raw):
    """4 字节对数浮点 -> double（快照行情成交量用）。"""
    logpoint = raw >> 24
    hheax, hleax, lheax, lleax = (raw >> 24) & 0xFF, (raw >> 16) & 0xFF, (raw >> 8) & 0xFF, raw & 0xFF
    dwEcx, dwEdx, dwEsi, dwEax = logpoint * 2 - 0x7F, logpoint * 2 - 0x86, logpoint * 2 - 0x8E, logpoint * 2 - 0x96
    tmp = -dwEcx if dwEcx < 0 else dwEcx
    base = 2.0 ** tmp
    if dwEcx < 0:
        base = 1.0 / base
    if hleax > 0x80:
        v4 = (2.0 ** (dwEdx + 1)) * (hleax & 0x7F) + (2.0 ** dwEdx) * 128.0
    else:
        v4 = (2.0 ** dwEdx) * hleax if dwEdx >= 0 else (1.0 / (2.0 ** dwEdx)) * hleax
    v3 = (2.0 ** dwEsi) * lheax
    v1 = (2.0 ** dwEax) * lleax
    if hleax & 0x80:
        v3 *= 2.0
        v1 *= 2.0
    return base + v4 + v3 + v1


def parse_quotes(body):
    """解析 0x053E 快照应答：'B1 CB' + u16 n + 每只 <B6sH> 基准价，再按差分还原五档。"""
    # 应答以 'B1 CB' 两字节开头，随后是 u16 只数；每只先给基准价，其余字段都是相对基准的差分。
    pos = 2
    num = struct.unpack_from("<H", body, pos)[0]
    pos += 2
    out = []
    for _ in range(num):
        market, code, _active1 = struct.unpack_from("<B6sH", body, pos)
        pos += 9
        base, pos = get_price(body, pos)
        rec = {"market": market, "code": code.decode("ascii", "replace")}
        last_close, pos = get_price(body, pos)
        rec["last_close"] = (base + last_close) / 100.0
        # 开高低是相对基准价的差分；加上基准再除 1000 才是真实价。
        for name in ("open", "high", "low"):
            diff, pos = get_price(body, pos)
            rec[name] = (base + diff) / 100.0
        for _skip in range(3):
            _, pos = get_price(body, pos)
        rec["vol"], pos = get_price(body, pos)
        rec["cur_vol"], pos = get_price(body, pos)
        rec["amount"] = get_volume(struct.unpack_from("<I", body, pos)[0])
        pos += 4
        rec["s_vol"], pos = get_price(body, pos)
        rec["b_vol"], pos = get_price(body, pos)
        for _skip in range(2):
            _, pos = get_price(body, pos)
        # 五档：买价、卖价、买量、卖量成对出现，同样是差分价。
        for lvl in range(1, 6):
            bid, pos = get_price(body, pos)
            ask, pos = get_price(body, pos)
            bv, pos = get_price(body, pos)
            av, pos = get_price(body, pos)
            rec["bid%d" % lvl] = (base + bid) / 100.0
            rec["ask%d" % lvl] = (base + ask) / 100.0
            rec["bid_vol%d" % lvl] = bv
            rec["ask_vol%d" % lvl] = av
        pos += 2
        for _skip in range(4):
            _, pos = get_price(body, pos)
        # 末尾两字节是有符号涨速（百分之一）与活跃度标志。
        _, speed_raw, active = struct.unpack_from("<hH", body, pos)
        rec["speed"] = speed_raw / 100.0
        rec["active2"] = active
        rec["price"] = base / 100.0
        out.append(rec)
    return out


def unpack_date(category, buf, pos):
    """日线及以上只有 u32 YYYYMMDD；分钟级是 u16 打包日 + u16 分钟数。"""
    # 分钟级：u16 打包日（bit11 起是年，减 2004；低 11 位是月日）加 u16 分钟数。
    if category < 4 or category in (7, 8):
        zipday, minutes = struct.unpack_from("<HH", buf, pos)
        y = (zipday >> 11) + 2004
        mo = (zipday % 2048) // 100
        d = (zipday % 2048) % 100
        hh, mm = divmod(minutes, 60)
        return (y, mo, d, hh, mm), pos + 4
    ymd = struct.unpack_from("<I", buf, pos)[0]
    return (ymd // 10000, (ymd % 10000) // 100, ymd % 100, 0, 0), pos + 4


class TdxError(RuntimeError):
    pass


class TdxConnection:
    def __init__(self, cfg_path=CFG_DEFAULT, timeout=8.0, verbose=False):
        self.cfg_path = cfg_path or CFG_DEFAULT   # None 会让 configparser 抛 TypeError
        self.timeout = timeout
        self.verbose = verbose
        self.sock = None
        self.host = None
        self.port = None
        self._lock = threading.Lock()
        self._inflight = 0
        self._sem = threading.Semaphore(MAX_INFLIGHT)

    # ---------- 配置与连接 ----------
    @staticmethod
    def _split_host(item):
        item = item.strip()
        if not item:
            return None
        if item.startswith("[") and "]" in item:            # [IPv6]:port
            host, _, port = item[1:].partition("]")
            return host, int(port.lstrip(":") or 7709)
        if item.count(":") == 1:
            host, port = item.split(":")
            try:
                return host, int(port)
            except ValueError:
                return host, 7709
        return item, 7709                                  # 裸 IPv6 / 裸主机

    def _hosts_from(self, path):
        """从一个 ini 风格文件里取 (权重, IP, 端口)；只认 IPAddressNN 这类编号键。"""
        cp = configparser.ConfigParser(strict=False)
        cp.read(path, encoding="gbk")
        got = []
        for section in getattr(self, "sections", ("HQHOST", "TRYHQHOST")):
            if not cp.has_section(section):
                continue
            for m in __import__("re").finditer(r"^ipaddress(\d+)$", "\n".join(cp.options(section)),
                                               __import__("re").M):
                idx = m.group(1)
                ip = cp.get(section, "IPAddress" + idx, fallback="").strip()
                if not ip:
                    continue
                port = cp.getint(section, "Port" + idx, fallback=7709)
                got.append((cp.getint(section, "Weight" + idx, fallback=0), ip, port))
        return got

    def load_hosts(self):
        """主机来源：connect.cfg 优先；没有站点时退到客户端维护的 newhost.lst。

        两个地址都可被 TDX_CFG 覆盖；newhost.lst 由官方更新器写入，
        路径是 <安装根>/T0002/newhost.lst 或 <安装根>/newhost.lst。
        """
        cfg = self.cfg_path or CFG_DEFAULT
        root = os.path.dirname(os.path.abspath(cfg))
        hosts = self._hosts_from(cfg)
        if not hosts:
            for cand in (os.path.join(root, "T0002", "newhost.lst"),
                         os.path.join(root, "newhost.lst")):
                if os.path.isfile(cand):
                    hosts = self._hosts_from(cand)
                    if hosts:
                        break
        if not hosts:
            raise TdxError("connect.cfg 与 newhost.lst 都没有可用主机")
        hosts.sort(key=lambda x: -x[0])
        return [(ip, port) for _w, ip, port in hosts]

    def connect(self):
        last = None
        import random, copy
        hosts = copy.deepcopy(self.load_hosts())
        random.shuffle(hosts)
        for host, port in hosts:
            try:
                s = socket.create_connection((host, port), timeout=self.timeout)
                s.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
                self.sock = s
                if getattr(self, "handshake", True):
                    self._handshake()
                self.host, self.port = host, port
                print(f'Use host-{self.host}:{self.port}')
                return host, port
            except Exception as exc:                        # noqa: BLE001
                last = exc
                try:
                    if self.sock:
                        self.sock.close()
                except Exception:                           # noqa: BLE001
                    pass
                self.sock = None
                print(f'Skip host-{host}:{port}')
        raise TdxError("全部主机连接失败: %r" % (last,))

    def _close(self):
        if self.sock is not None:
            try:
                self.sock.close()
            except OSError:
                pass
            self.sock = None

    def _send_raw(self, data):
        self.sock.sendall(data)

    def _recv_exact(self, n):
        buf = b""
        while len(buf) < n:
            chunk = self.sock.recv(n - len(buf))
            if not chunk:
                raise TdxError("连接被关闭")
            buf += chunk
        return buf

    ## 请求帧
    # 帧结构：0C 魔数 + u16(hi<<8|类别) + u16 版本 + 1 字节标志 + u16 声明长度 ×2 + u16 命令字 + 载荷。
    # 服务端按命令字路由，所以 commtype 必须与业务成对出现，写错只会拿到别的推送。
    def _request(self, payload, category_byte, version, commtype, hi=0x08, flag5=0x01):
        body = struct.pack("<H", commtype) + payload
        length = len(body)
        frame = b"\x0c" + struct.pack("<H", (hi << 8) | category_byte)
        frame += struct.pack("<H", version) + bytes([flag5])
        frame += struct.pack("<HH", length, length) + body
        with self._sem:
            with self._lock:
                last_exc = None
                # 该服务端一条连接只服务一次业务请求，失败即弃连接重连，最多三次。
                for _attempt in range(3):
                    try:
                        if self.sock is None:
                            self.connect()
                        self._send_raw(frame)
                        # 应答头 +10 回显 commtype；中间可能夹带推送，逐条丢弃直到对上。
                        for _ in range(8):
                            body = self._read_message()
                            if self._last_commtype == commtype:
                                return body
                        last_exc = TdxError("应答 commtype 不匹配（期望 0x%04X）" % commtype)
                    except (TdxError, OSError) as exc:
                        last_exc = exc
                    # 实测该服务端每连接只服务一次业务请求，失败后重连再试。
                    self._close()
                raise TdxError("请求失败: %r" % (last_exc,))

    ## 应答帧
    # 16 字节头：前 10 字节是回显，10..12 是命令字（用来丢弃夹带的推送），12..16 是压缩后与解压后长度。
    def _read_message(self):
        head = self._recv_exact(16)
        self._last_commtype = struct.unpack_from("<H", head, 10)[0]
        _, _, _, zipsize, unzipsize = struct.unpack("<IIIHH", head)
        if zipsize > 0x200000 or unzipsize > 0x200000:
            raise TdxError("异常长度 zipsize=%d unzipsize=%d" % (zipsize, unzipsize))
        raw = self._recv_exact(zipsize) if zipsize else b""
        if zipsize != unzipsize:
            raw = zlib.decompress(raw)
        return raw

    ## 握手
    # Setup1/Setup2 服务端会回应答，必须读干净，否则后续报文错位；Setup3 无应答，发出后直接等业务帧。
    def _handshake(self):
        # Setup1/Setup2 有应答必须读干净，否则后续报文错位；Setup3 无应答，发了就等业务帧。
        for pkt in (
            bytes.fromhex("0c0218930001030003000d0001"),
            bytes.fromhex("0c0218940001030003000d0002"),
        ):
            self._send_raw(pkt)
            self._read_message()
        self._send_raw(bytes.fromhex(
            "0c0318990001200020000fdbd5d0c9ccd6a4a8af0000008fc22540130000d500c9ccbdf0d7ea00000002"))

    ## 通用裸帧
    # 给不走 _request（它把 commtype 塞进 body）的那些命令用：commtype 在头 10..12，body 原样，
    # 两段长度相等即不压缩。应答同样按 commtype 对齐，最多丢弃 8 条夹带推送。
    def request_raw(self, commtype, payload=b"", catword=1, ver=0, flag=0):
        body = payload
        frame = b"\x0c" + struct.pack("<HHBHH", catword, ver, flag, len(body), len(body))
        frame += struct.pack("<H", commtype) + body
        with self._sem:
            with self._lock:
                last_exc = None
                for _attempt in range(3):
                    try:
                        if self.sock is None:
                            self.connect()
                        self._send_raw(frame)
                        for _ in range(8):
                            got = self._read_message()
                            if self._last_commtype == commtype:
                                return got
                        last_exc = TdxError("应答 commtype 不匹配（期望 0x%04X）" % commtype)
                    except (TdxError, OSError) as exc:
                        last_exc = exc
                    self._close()
                raise TdxError("请求失败: %r" % (last_exc,))

    ## 资讯服务器（INFOHOST 7711）的问候帧
    # 0x1F7E 是客户端连上资讯口后的第一条命令，body 固定两个零字节；应答是明文段里的问候，
    # 带服务端时间、版本串、路径前缀 /tdx/infohost 和 F10/表格两个能力标志。不吃登录态。
    def info_hello(self):
        body = self.request_raw(0x1F7E, b"\x00\x00")
        out = {"raw": body}
        stamp = body[9:40].split(b"\x00")[0]
        out["time"] = stamp.decode("gbk", "ignore")
        m = re.search(rb"[0-9][0-9.]*\s*build[^\x00]*", body)
        out["build"] = m.group(0).decode("ascii", "ignore") if m else ""
        at = body.find(b"/tdx/")
        out["path"] = body[at:body.find(b"\x00", at)].decode("ascii", "ignore") if at >= 0 else ""
        out["f10"] = b"F10: Y" in body
        out["table"] = b"Table: Y" in body
        return out

    def heartbeat(self):
        self._send_raw(bytes.fromhex("0c0108000101000000000000"))

    # ---------- 码表 ----------
    def security_quotes(self, stocks):
        """快照行情（commtype 0x053E，五档）。stocks 为 [(market, b"code"), ...]。

        注意：本机对银河 v11.68 主站实测该命令只被接受、不回包（疑被 L2 门控），
        代码留作可用主站的备选；批量截面优先用 BDHQ。
        """
        n = len(stocks)
        payload = b"\x05\x00\x00\x00" + struct.pack("<H", n)
        payload += b"".join(struct.pack("<B6s", m, c) for m, c in stocks)
        # 0x053E 的声明长度固定为 每只 7 字节 + 12 字节头，先算长度再拼帧。
        decl = 7 * n + 12
        frame = (b"\x0c" + b"\x01\x20\x63\x00\x02" + struct.pack("<HH", decl, decl)
                 + struct.pack("<H", 0x053E) + payload)
        with self._sem:
            with self._lock:
                if self.sock is None:
                    self.connect()
                self._send_raw(frame)
                return parse_quotes(self._read_message())

    def security_count(self, market):
        payload = struct.pack("<H", market) + bytes.fromhex("75c73301")
        body = self._request(payload, 0x0C, 0x016C, 0x044E, hi=0x18, flag5=0x00)
        return struct.unpack_from("<H", body, 0)[0]

    def security_list(self, market, start, count=1000):
        body = self._request(struct.pack("<HH", market, start), 0x01, 0x0164, 0x0450, hi=0x18)
        num = struct.unpack_from("<H", body, 0)[0]
        pos, out = 2, []
        for _ in range(num):
            code, _unit, name_b, _r1, _dp, _pc, _r2 = struct.unpack_from("<6sH8s4sB4s4s", body, pos)
            pos += 29
            out.append((market, code.decode("ascii", "ignore"),
                        name_b.split(b"\0")[0].decode("gbk", "ignore")))
        return out

    # 应答体：9 字节前缀 + u16 条数，每条 8 字节身份 + 日期 + 类别 + 16 字节浮点区；
    # 只有类别 1 用满四个浮点，11/12 取第 3 个，13/14 取第 1、3 个，其余类别只留日期。
    def xdxr_info(self, market, code):
        """除权除息明细（服务端权限内的 gbbq 子集），逐条返回事件。"""
        payload = struct.pack("<H", 1) + struct.pack("<B6s", market, code.encode())
        body = self._request(payload, 0x1F, 0x0076, 0x000F, hi=0x18)
        if len(body) < 11:
            return []
        pos = 9
        num = struct.unpack_from("<H", body, pos)[0]
        pos += 2
        out = []
        for _ in range(num):
            mkt, codeb = struct.unpack_from("<B6s", body, pos)
            pos += 8                      # 7 字节身份 + 1 字节保留
            ymd, pos = unpack_date(9, body, pos)
            cat = body[pos]
            pos += 1
            raw = body[pos:pos + 16]
            pos += 16
            ev = {"market": mkt, "code": codeb.decode("ascii", "ignore"),
                  "date": ymd[0] * 10000 + ymd[1] * 100 + ymd[2], "category": cat}
            if cat == 1:
                ev["dividend"], ev["rights_price"], ev["bonus"], ev["rights"] = \
                    struct.unpack("<ffff", raw)
            elif cat in (11, 12):
                ev["shrink"] = struct.unpack("<f", raw[8:12])[0]
            elif cat in (13, 14):
                ev["warrant_price"], ev["warrant_ratio"] = \
                    struct.unpack("<f", raw[0:4])[0], struct.unpack("<f", raw[8:12])[0]
            else:
                ev["shares_before"], ev["shares_after"] = \
                    struct.unpack("<I", raw[0:4])[0], struct.unpack("<I", raw[12:16])[0]
            out.append(ev)
        return out

    # ---------- 行情 ----------
    def security_bars(self, category, market, code, start=0, count=100):
        if count > 800:
            raise ValueError("单次最多 800 条")
        payload = struct.pack("<H6sHHHHIIH", market, code.encode(),
                              category, 1, start, count, 0, 0, 0)
        body = self._request(payload, CAT_BARS, VER_BARS, CT_BARS)
        num = struct.unpack_from("<H", body, 0)[0]
        pos, base, out = 2, 0, []
        # 服务端声明条数常大于实际，尾部会剩碎字节；日期不像日期就收工，别硬解出天文数字。
        for _ in range(num):
            if pos + 4 > len(body):
                break
            dt, pos = unpack_date(category, body, pos)
            stamp = dt[0] * 10000 + dt[1] * 100 + dt[2]
            if not (19900101 <= stamp <= 20301231):
                break
            d_open, pos = get_price(body, pos)
            d_close, pos = get_price(body, pos)
            d_high, pos = get_price(body, pos)
            d_low, pos = get_price(body, pos)
            vol_f, pos = struct.unpack_from("<I", body, pos)[0], pos + 4
            amt_f, pos = struct.unpack_from("<I", body, pos)[0], pos + 4
            o = base + d_open
            c = o + d_close
            h = o + d_high
            low = o + d_low
            base = o + d_close
            out.append({"dt": dt, "open": o / 1000.0, "close": c / 1000.0,
                        "high": h / 1000.0, "low": low / 1000.0,
                        "vol": get_volume(vol_f), "amount": get_volume(amt_f)})
        return out

    def minute_time_data(self, market, code):
        body = self._request(struct.pack("<H6sI", market, code.encode(), 0),
                             CAT_MINUTE, VER_MINUTE, CT_MINUTE)
        # 应答头：条数(u16) + 0000 + 01 + 代码(6 字节) + 2 字节填充，记录自偏移 13 起。
        num = struct.unpack_from("<H", body, 0)[0]
        pos, last, out = 13, 0, []
        for _ in range(num):
            diff, pos = get_price(body, pos)
            vol, pos = get_price(body, pos)