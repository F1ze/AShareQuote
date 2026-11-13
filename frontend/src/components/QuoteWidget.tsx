import { useEffect, useState } from "react"
import { ArrowDown, ArrowUp, ChevronsUpDown, Minus, Pause, Pin, Play, Settings2, X } from "lucide-react"
import { useRealtime } from "@/hooks/useRealtime"
import { api } from "@/lib/api"
import { priceChange, quoteKey, readWatch, realtimeClock, realtimePrice, stockKey } from "@/lib/realtime"
import { browserWidgetSettings, nativeWidget, widgetInterval } from "@/lib/widget"

const columns = [
  { key: "name", label: "证券 / 代码" },
  { key: "price", label: "最新价" },
  { key: "change", label: "涨跌幅" },
  { key: "time", label: "接收时间" },
] as const
type SortColumn = (typeof columns)[number]["key"]
const namesCollator = new Intl.Collator("zh-CN", { numeric: true })

// 同一轻量页面可运行在普通弹窗或 Tauri 的系统 WebView 中，不导入主屏图表或任务 SSE。
export function QuoteWidget() {
  const [settings, setSettings] = useState(() => browserWidgetSettings(readWatch()))
  const [minimumInterval, setMinimumInterval] = useState(() => browserWidgetSettings(readWatch()).interval)
  const [paused, setPaused] = useState(false)
  const [nativeHidden, setNativeHidden] = useState(false)
  const [configuring, setConfiguring] = useState(false)
  const [input, setInput] = useState(settings.codes.join(","))
  const [opacity, setOpacity] = useState(0.82)
  const [pinned, setPinned] = useState(true)
  const [message, setMessage] = useState("")
  const [sort, setSort] = useState<{ column: SortColumn; descending: boolean } | null>(null)
  const native = nativeWidget()
  const state = useRealtime(settings.codes, settings.selected, settings.interval, paused || nativeHidden, 0, false)
  const quotes = new Map((state.data?.quotes || []).map((quote) => [quoteKey(quote), quote]))
  const receivedTime = realtimeClock(state.data?.receivedAt)
  const rows = settings.codes.map((key) => {
    const quote = quotes.get(key)
    const name = quote?.name || state.names[key]
    return {
      key, name, symbol: key.toUpperCase(),
      values: {
        name: name || key.toUpperCase(),
        price: quote && quote.price > 0 ? quote.price : null,
        change: priceChange(quote),
        time: quote ? state.data?.receivedAt ?? null : null,
      },
    }
  })
  if (sort) {
    rows.sort((a, b) => {
      const left = a.values[sort.column], right = b.values[sort.column]
      if (left === null || right === null) return Number(left === null) - Number(right === null)
      const comparison = typeof left === "number" && typeof right === "number"
        ? left - right
        : sort.column === "time"
          ? Date.parse(String(left)) - Date.parse(String(right))
          : namesCollator.compare(String(left), String(right))
      return sort.descending ? -comparison : comparison
    })
  }
  const sync = async () => {
    try {
      const watch = await api.watchSettings()
      setMinimumInterval(widgetInterval(watch.interval)); setSettings({ ...watch, interval: widgetInterval(watch.interval) }); setInput(watch.codes.join(",")); setMessage("")
    } catch { setMessage("主面板自选暂不可读，可在这里直接配置代码。") }
  }
  useEffect(() => {
    document.documentElement.dataset.widget = native ? "native" : "web"
    document.title = "QFLUX · 轻量行情"
    if (native) void api.watchSettings().then((watch) => { setMinimumInterval(widgetInterval(watch.interval)); setSettings({ ...watch, interval: widgetInterval(watch.interval) }); setInput(watch.codes.join(",")) }).catch(() => setMessage("请先启动 QFLUX 后端，也可手动配置股票。"))
    // 原生最小化不一定触发 WebView 的 visibilitychange；仅检查窗口状态，不请求行情。
    let disposed = false
    const timer = native ? setInterval(() => {
      const win = native.window.getCurrentWindow()
      void Promise.all([win.isVisible(), win.isMinimized()]).then(([visible, minimized]) => { if (!disposed) setNativeHidden(!visible || minimized) }).catch(() => {})
    }, 1000) : undefined
    return () => { disposed = true; clearInterval(timer); delete document.documentElement.dataset.widget }
  }, [native])
  const control = async (action: "close" | "pin" | "minimize") => {
    try {
      if (action === "close") { setPaused(true); if (native) await native.window.getCurrentWindow().close(); else window.close() }
      if (action === "minimize" && native) { setPaused(true); await native.window.getCurrentWindow().minimize(); setPaused(false) }
      if (action === "pin" && native) { await native.window.getCurrentWindow().setAlwaysOnTop(!pinned); setPinned(!pinned) }
    } catch { setMessage("窗口操作未完成，请使用系统窗口控制。") }
  }
  return <main className="quote-widget" style={{ backgroundColor: `rgba(14,23,37,${opacity})` }} aria-label="轻量行情小组件">
    <header className="widget-bar" data-tauri-drag-region><strong data-tauri-drag-region>QFLUX <small data-tauri-drag-region>轻量行情</small></strong><div>
      <button type="button" title="配置股票与透明度" aria-label="配置小组件" onClick={() => setConfiguring(!configuring)}><Settings2 /></button>
      {native && <><button type="button" title={pinned ? "取消置顶" : "置顶"} aria-label="切换置顶" aria-pressed={pinned} onClick={() => void control("pin")}><Pin /></button><button type="button" aria-label="最小化小组件" onClick={() => void control("minimize")}><Minus /></button></>}
      <button type="button" aria-label="关闭小组件" onClick={() => void control("close")}><X /></button>
    </div></header>
    {configuring && <form className="widget-config" onSubmit={(event) => {
      event.preventDefault()
      const keys = input.split(/[,，\s]+/).filter(Boolean).map(stockKey)
      if (!keys.length || keys.some((key) => !key) || keys.length > 20) { setMessage("请填写 1–20 只有效股票代码。"); return }
      const codes = [...new Set(keys as string[])]
      setSettings({ ...settings, codes, selected: codes.includes(settings.selected) ? settings.selected : codes[0] }); setConfiguring(false); setMessage("")
    }}>
      <label>监控代码<input aria-label="小组件股票代码" value={input} onChange={(event) => setInput(event.target.value)} /></label>
      <div><label>间隔<select aria-label="小组件刷新间隔" value={settings.interval} onChange={(event) => setSettings({ ...settings, interval: Number(event.target.value) })}>{[10, 15, 30, 60].filter((value) => value >= minimumInterval).map((value) => <option key={value} value={value}>{value} 秒</option>)}</select></label><label>不透明度<input aria-label="小组件不透明度" type="range" min="0.35" max="1" step="0.05" value={opacity} onChange={(event) => setOpacity(Number(event.target.value))} /></label></div>
      <div><button type="submit">应用</button><button type="button" onClick={() => void sync()}>同步主面板自选</button></div>
    </form>}
    {message && <p role="alert" className="widget-message">{message}</p>}
    <div className="widget-quotes">
      <table>
        <thead><tr>{columns.map(({ key, label }) => {
          const active = sort?.column === key
          const descending = active && sort.descending
          const Icon = active ? descending ? ArrowDown : ArrowUp : ChevronsUpDown
          return <th key={key} scope="col" aria-sort={active ? descending ? "descending" : "ascending" : undefined}>
            <button type="button" aria-label={`按${label}排序`} onClick={() => setSort({ column: key, descending: active ? !descending : key !== "name" })}>
              {label}<Icon aria-hidden="true" />
            </button>
          </th>
        })}</tr></thead>
        <tbody>{rows.map(({ key, name, symbol, values }) => {
          const delta = values.change
          const tone = delta === null || delta === 0 ? "" : delta > 0 ? "rt-up" : "rt-down"
          return <tr key={key} title={state.error ? "旧数据：" + state.error : paused ? "已暂停" : "本机接收时间，非交易所时间"}>
            <td><strong>{name || symbol}</strong>{name && <small>{symbol}</small>}</td>
            <td className={tone}>{realtimePrice(values.price ?? undefined)}</td>
            <td className={tone}>{delta === null ? "—" : `${delta > 0 ? "+" : ""}${delta.toFixed(2)}%`}</td>
            <td><time dateTime={values.time ?? undefined}>{values.time ? receivedTime : "—"}</time></td>
          </tr>
        })}</tbody>
      </table>
    </div>
    <footer><button type="button" onClick={() => setPaused(!paused)} aria-label={paused ? "继续小组件刷新" : "暂停小组件刷新"}>{paused ? <Play /> : <Pause />}</button><span>{paused ? "已暂停" : state.error ? "旧数据 · 自动退避" : state.loading ? "刷新中" : `${settings.interval}s 间隔 · 五档报价`}</span>{!native && <small title="普通浏览器窗口不能获得原生置顶或桌面透明能力">网页模式 · 无原生置顶</small>}</footer>
  </main>
}
