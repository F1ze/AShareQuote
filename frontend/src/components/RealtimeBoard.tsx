import { useEffect, useState } from "react"
import { Activity, CircleAlert, Pause, Play, Plus, Radio, RefreshCw, PanelsTopLeft } from "lucide-react"
import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts"
import { api } from "@/lib/api"
import { WatchlistTable } from "@/components/WatchlistTable"
import { useRealtime } from "@/hooks/useRealtime"
import { priceChange, quoteKey, readWatch, stockKey } from "@/lib/realtime"
import { OrderBook } from "@/components/OrderBookRow"

const number = new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 2 })
const price = (value?: number) => value && Number.isFinite(value) ? value.toFixed(2) : "—"
const change = (value: number | null) => value === null ? "—" : `${value > 0 ? "+" : ""}${value.toFixed(2)}%`
const tone = (value: number | null) => value === null || value === 0 ? "rt-neutral" : value > 0 ? "rt-up" : "rt-down"
const clock = (value?: string) => value ? new Date(value).toLocaleTimeString("zh-CN", { hour12: false }) : "尚未接收"

export function RealtimeBoard() {
  const [settings, setSettings] = useState(readWatch)
  const [bookMotion, setBookMotion] = useState(true)
  const [paused, setPaused] = useState(false)
  const [revision, setRevision] = useState(0)
  const [input, setInput] = useState("")
  const [validation, setValidation] = useState("")
  const { codes, selected, interval } = settings
  const { data, names, loading, error } = useRealtime(codes, selected, interval, paused, revision)
  useEffect(() => { try { localStorage.setItem("qflux.realtime", JSON.stringify(settings)) } catch { /* 私密模式不影响看盘。 */ } }, [settings])
  useEffect(() => { void api.saveWatch(settings).catch(() => { /* 发布失败不阻塞主看盘，桌面可手工配置。 */ }) }, [settings])
  const quotes = new Map((data?.quotes || []).map((quote) => [quoteKey(quote), quote]))
  const current = quotes.get(selected)
  const movement = priceChange(current)
  const trades = [...(data?.trades || [])].sort((a, b) => a.time.localeCompare(b.time))
  const chart = trades.map((trade, index) => ({ ...trade, index }))
  const add = (event: React.FormEvent) => {
    event.preventDefault()
    const key = stockKey(input)
    if (!key) { setValidation("请输入六位 A 股代码，例如 600036、sz000001、bj920002。"); return }
    if (codes.includes(key)) { setSettings({ ...settings, selected: key }); setInput(""); setValidation(""); return }
    if (codes.length >= 20) { setValidation("最多监控 20 只股票，请先移除一只。"); return }
    setSettings({ ...settings, codes: [...codes, key], selected: key }); setInput(""); setValidation("")
  }
  const remove = (key: string) => {
    if (codes.length === 1) { setValidation("至少保留一只监控股票。"); return }
    const next = codes.filter((code) => code !== key)
    setSettings({ ...settings, codes: next, selected: selected === key ? next[0] : selected })
  }
  return <main className="rt-board" aria-label="实时看盘工作区">
    <div className="rt-toolbar">
      <div className="rt-live-label"><Radio className="size-4" /><span>{paused ? "刷新已暂停" : "行情轮询"}</span><small>接收于 {clock(data?.receivedAt)}</small></div>
      <div className="rt-controls">
        <label className="sr-only" htmlFor="realtime-interval">刷新间隔</label>
        <select id="realtime-interval" aria-label="刷新间隔" value={interval} onChange={(event) => setSettings({ ...settings, interval: Number(event.target.value) })} className="form-control select-control">
          {[2, 3, 5, 10, 30].map((seconds) => <option key={seconds} value={seconds}>{seconds} 秒间隔</option>)}
        </select>
        <button type="button" className="secondary-button" onClick={() => setPaused(!paused)}>{paused ? <Play className="size-3.5" /> : <Pause className="size-3.5" />}{paused ? "继续" : "暂停"}</button>
        <button type="button" className="secondary-button" disabled={loading || paused} onClick={() => setRevision(revision + 1)} aria-label="立即刷新行情"><RefreshCw className={`size-3.5 ${loading ? "animate-spin" : ""}`} /></button>
      </div>
    </div>
    <div className="rt-capability"><CircleAlert className="size-4 shrink-0" /><span><strong>完整 L2 未接入</strong> · 当前展示真实五档快照与可用的扩展成交明细；无十档盘口、独立逐笔委托和已验证的 L2 授权。成交明细通常为约 3 秒聚合，非逐笔全量。</span></div>
    {error && <div role="alert" className="notice notice-warn"><CircleAlert className="size-4 shrink-0" /><span>{error}。{data ? "下方保留上次数据，已过期；" : "尚未取得行情；"}将自动退避重试，可检查顶部代理与站点。</span></div>}
    <div className="rt-layout">
      <section className="rt-main rt-card" aria-label="当前股票主屏">
        <div className="rt-card-title"><div><span className="eyebrow">FOCUS / 当前股票</span><h2>{current?.name || names[selected] || "名称待补充"} <small>{selected.toUpperCase()}</small></h2></div><span className="rt-tag">{loading ? "刷新中" : paused ? "已暂停" : error ? "旧数据" : current ? "五档快照" : "等待行情"}</span></div>
        <div className="rt-price-block"><strong className={tone(movement)}>{price(current?.price)}</strong><span className={tone(movement)}>{change(movement)}</span><small>较昨收 {price(current?.lastClose)} 元</small></div>
        <div className="rt-metrics">{[["开盘", price(current?.open)], ["最高", price(current?.high)], ["最低", price(current?.low)], ["成交量 / 手", current ? number.format(current.volume) : "—"], ["成交额 / 万元", current ? number.format(current.amount / 10000) : "—"]].map(([label, value]) => <div key={label}><span>{label}</span><strong>{value}</strong></div>)}</div>
        <div className="rt-chart-heading"><h3>成交窗口走势</h3><span>最近至多 200 条 · 非全天分时</span></div>
        <div className="rt-chart" aria-label="扩展成交窗口价格走势">
          {chart.length > 1 ? <ResponsiveContainer width="100%" height="100%"><AreaChart data={chart} margin={{ top: 12, right: 12, left: -18, bottom: 0 }}><defs><linearGradient id="rt-fill" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stopColor="#a7b6ff" stopOpacity={0.3} /><stop offset="100%" stopColor="#a7b6ff" stopOpacity={0} /></linearGradient></defs><CartesianGrid stroke="#29384f" strokeDasharray="3 6" vertical={false} /><XAxis dataKey="time" minTickGap={65} tick={{ fill: "#8c9eb8", fontSize: 10 }} axisLine={false} tickLine={false} /><YAxis domain={["auto", "auto"]} tick={{ fill: "#8c9eb8", fontSize: 10 }} axisLine={false} tickLine={false} /><Tooltip contentStyle={{ background: "#131e2e", borderColor: "#3d506d", borderRadius: 8 }} formatter={(value) => [Number(value).toFixed(2), "成交价 / 元"]} /><Area type="linear" dataKey="price" stroke="#a7b6ff" strokeWidth={2} fill="url(#rt-fill)" isAnimationActive={false} /></AreaChart></ResponsiveContainer> : <div className="rt-empty" role="status"><Activity className="size-7" /><span>{loading ? "正在读取真实行情…" : "暂无可绘制的成交窗口"}</span><small>不使用模拟价格填充图表</small></div>}
        </div>
        <div className="rt-source-line"><span>通达信 · 0x053E / 0x0FC2</span><span>快照耗时 {data ? `${data.latencyMs} ms` : "—"}{data?.cached ? " · 缓存" : ""}</span></div>
      </section>
      <section className="rt-book rt-card" aria-label="五档盘口">
        <div className="rt-card-title"><h3>买卖盘口</h3><button type="button" className="text-button text-[10px]" aria-label="数量条平滑动效" aria-pressed={bookMotion} onClick={() => setBookMotion(!bookMotion)}>条形动效{bookMotion ? "开" : "关"}</button><span className="rt-tag">L1 · 五档</span></div>
        <div className="rt-book-labels"><span>档位</span><span>价格 / 元</span><span>委托量 / 手</span></div>
        <OrderBook key={selected} quote={current} animated={bookMotion} />
        <p className="rt-muted">0 价表示该档无有效报价；这里不是 L2 十档订单簿。</p>
      </section>
      <section className="rt-watch rt-card" aria-label="多股票实时监控">
        <div className="rt-card-title"><h3>自选监控 <small>{codes.length}/20 · 批量快照</small></h3><button type="button" className="text-button" onClick={() => {
          const url = new URL(window.location.href); url.search = new URLSearchParams({ widget: "1", codes: codes.join(","), selected, interval: String(Math.max(10, interval * 2)) }).toString()
          const popup = window.open(url, "qflux-widget", "popup,width=440,height=320,resizable=yes")
          if (!popup) setValidation("浏览器阻止了独立窗口，请允许此站点弹出窗口后重试。")
        }}><PanelsTopLeft className="size-3.5" />轻量窗口</button></div>
        <form onSubmit={add} className="rt-add"><label htmlFor="watch-code" className="sr-only">添加股票代码</label><input id="watch-code" className="form-control" value={input} onChange={(event) => setInput(event.target.value)} placeholder="股票代码，如 600036" maxLength={16} /><button type="submit" className="secondary-button" aria-label="添加监控股票"><Plus className="size-4" /></button></form>
        {validation && <p role="alert" className="rt-validation">{validation}</p>}
        <WatchlistTable names={names} codes={codes} selected={selected} quotes={quotes} receivedAt={data?.receivedAt} paused={paused} loading={loading} error={error} onSelect={(key) => setSettings({ ...settings, selected: key })} onRemove={remove} />
      </section>
      <section className="rt-tape rt-card" aria-label="扩展成交明细">
        <div className="rt-card-title"><h3>扩展成交明细 <small>{selected.toUpperCase()}</small></h3><span className="rt-tag">{data?.detailStatus === "available" ? `${trades.length} 条窗口` : "五档降级"}</span></div>
        <p className="rt-muted">{data?.detailMessage || "正在尝试读取扩展成交接口，不据此认定完整 L2 权限。"}</p>
        <div className="rt-tape-scroll"><table><thead><tr><th>源端时间</th><th>成交价 / 元</th><th>成交量 / 手</th><th>聚合笔数</th><th>方向</th></tr></thead><tbody>{trades.length ? [...trades].reverse().map((trade, index) => <tr key={`${trade.time}-${index}`}><td>{trade.time}</td><td>{price(trade.price)}</td><td>{number.format(trade.volume)}</td><td>{trade.trades}</td><td className={trade.side === 0 ? "rt-up" : trade.side === 1 ? "rt-down" : "rt-neutral"}>{({ 0: "主动买", 1: "主动卖", 2: "中性", 5: "盘后" } as Record<number, string>)[trade.side] || "未知"}</td></tr>) : <tr><td colSpan={5} className="rt-empty-cell">{loading ? "正在读取…" : "暂无明细；五档快照和自选监控独立可用"}</td></tr>}</tbody></table></div>
      </section>
    </div>
  </main>
}
