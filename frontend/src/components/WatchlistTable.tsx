import { useMemo, useRef, useState } from "react"
import { ArrowDown, ArrowUp, Crosshair, Search, X } from "lucide-react"
import type { RealtimeQuote } from "@/types"
import { priceChange } from "@/lib/realtime"

type SortColumn = "name" | "price" | "change" | "absolute" | "volume" | "amount"
interface Props {
  names?: Readonly<Record<string, string>>
  codes: string[]
  selected: string
  quotes: Map<string, RealtimeQuote>
  receivedAt?: string
  paused: boolean
  loading: boolean
  error: string
  onSelect: (key: string) => void
  onRemove: (key: string) => void
}
const numeric = new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 2 })
const money = (value?: number) => value && Number.isFinite(value) ? value.toFixed(2) : "—"
const compact = (value?: number) => value === undefined ? "—" : value >= 1e8 ? `${(value / 1e8).toFixed(2)}亿` : value >= 1e4 ? `${(value / 1e4).toFixed(2)}万` : numeric.format(value)
const signed = (value: number | null, suffix = "") => value === null ? "—" : `${value > 0 ? "+" : ""}${value.toFixed(2)}${suffix}`
const tone = (value: number | null) => value === null || value === 0 ? "rt-neutral" : value > 0 ? "rt-up" : "rt-down"
const clock = (value?: string) => value ? new Date(value).toLocaleTimeString("zh-CN", { hour12: false }) : "—"

// 排序和筛选仅改变展示顺序，不修改订阅数组，不触发额外行情请求。
export function WatchlistTable({ names, codes, selected, quotes, receivedAt, paused, loading, error, onSelect, onRemove }: Props) {
  const [query, setQuery] = useState("")
  const [sort, setSort] = useState<{ column: SortColumn; descending: boolean } | null>(null)
  const region = useRef<HTMLDivElement>(null)
  const visible = useMemo(() => {
    const term = query.trim().toLowerCase()
    const rows = codes.filter((key) => `${key} ${quotes.get(key)?.name || names?.[key] || ""}`.toLowerCase().includes(term))
    if (!sort) return rows
    const value = (key: string): string | number | null => {
      const quote = quotes.get(key)
      if (sort.column === "name") return quote?.name || names?.[key] || key
      if (sort.column === "change") return priceChange(quote)
      if (sort.column === "absolute") return quote && priceChange(quote) !== null ? quote.price - quote.lastClose : null
      if (sort.column === "price") return quote && quote.price > 0 ? quote.price : null
      return quote?.[sort.column] ?? null
    }
    return [...rows].sort((a, b) => {
      const left = value(a), right = value(b)
      if (left === null || right === null) return left === right ? a.localeCompare(b) : left === null ? 1 : -1
      const comparison = typeof left === "number" && typeof right === "number" ? left - right : String(left).localeCompare(String(right), "zh-CN")
      return comparison ? comparison * (sort.descending ? -1 : 1) : a.localeCompare(b)
    })
  }, [codes, query, quotes, sort, names])
  const header = (label: string, column: SortColumn, className = "") => <th className={className} scope="col" aria-sort={sort?.column === column ? sort.descending ? "descending" : "ascending" : "none"}>
    <button type="button" onClick={() => setSort({ column, descending: sort?.column === column ? !sort.descending : column !== "name" })} aria-label={`按${label}排序`}>{label}<span aria-hidden="true">{sort?.column === column ? sort.descending ? "↓" : "↑" : "↕"}</span></button>
  </th>
  const locate = () => {
    setQuery("")
    requestAnimationFrame(() => region.current?.querySelector<HTMLElement>('tr[data-selected="true"] button')?.focus())
  }
  return <>
    <div className="rt-watch-tools">
      <label className="rt-watch-filter"><Search className="size-3.5" /><span className="sr-only">筛选自选股票</span><input type="search" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="名称 / 代码" aria-label="筛选自选股票" /></label>
      <span className="rt-watch-count">{visible.length}/{codes.length} 只</span>
      <button type="button" className="text-button" onClick={locate}><Crosshair className="size-3" />定位当前</button>
    </div>
    <div className="rt-watch-scroll" ref={region} tabIndex={0} role="region" aria-label="自选行情表，支持排序和键盘操作">
      <table className="rt-watch-table">
        <colgroup><col className="watch-symbol" /><col className="watch-price" /><col className="watch-change" /><col className="watch-absolute" /><col className="watch-volume" /><col className="watch-amount" /><col className="watch-turnover" /><col className="watch-time" /><col className="watch-state" /><col className="watch-action" /></colgroup>
        <thead><tr>{header("股票 / 代码", "name", "watch-symbol")}{header("最新价", "price", "watch-price")}{header("涨跌幅", "change", "watch-change")}{header("涨跌额", "absolute", "watch-absolute")}{header("成交量·手", "volume", "watch-volume")}{header("成交额·元", "amount", "watch-amount")}<th scope="col" className="watch-turnover" title="当前行情未提供流通股本，无法计算真实换手率">换手率 ⓘ</th><th scope="col" className="watch-time" title="本机收到该报价的时间，不是交易所行情时间">接收时间</th><th scope="col" className="watch-state">状态</th><th scope="col" className="watch-action"><span className="sr-only">移除</span></th></tr></thead>
        <tbody>{visible.map((key) => {
          const quote = quotes.get(key), delta = priceChange(quote)
          const absolute = quote && delta !== null ? quote.price - quote.lastClose : null
          const status = paused ? "已暂停" : error ? "已过期" : loading ? "刷新中" : quote ? "已接收" : "未返回"
          const info = `${quote?.name || names?.[key] || "名称待补充"} ${key.toUpperCase()}；${status}；本机接收 ${quote ? clock(receivedAt) : "—"}；开 ${money(quote?.open)} / 高 ${money(quote?.high)} / 低 ${money(quote?.low)} / 昨收 ${money(quote?.lastClose)}；涨跌额 ${signed(absolute)} 元；成交量 ${quote ? numeric.format(quote.volume) : "—"} 手；成交额 ${quote ? numeric.format(quote.amount) : "—"} 元；换手率未提供（缺少流通股本）`
          return <tr key={key} className={`rt-watch-row ${selected === key ? "is-selected" : ""}`} data-selected={selected === key} title={info}>
            <td className="watch-symbol"><button type="button" aria-pressed={selected === key} aria-label={`查看 ${key.toUpperCase()}`} onClick={() => onSelect(key)}><strong>{quote?.name || names?.[key] || "名称待补充"}</strong><small>{key.toUpperCase()}</small><span className="watch-status-dot" data-status={status} aria-label={status} /></button></td>
            <td className={`watch-price ${tone(delta)}`}>{money(quote?.price)}</td>
            <td className={`watch-change ${tone(delta)}`}><span className="watch-change-value">{delta !== null && delta !== 0 ? delta > 0 ? <ArrowUp aria-label="上涨" /> : <ArrowDown aria-label="下跌" /> : null}{signed(delta, "%")}</span></td>
            <td className={`watch-absolute ${tone(delta)}`}>{signed(absolute)}</td>
            <td className="watch-volume">{compact(quote?.volume)}</td><td className="watch-amount">{compact(quote?.amount)}</td>
            <td className="watch-turnover" title="缺少流通股本，未计算换手率">—</td>
            <td className="watch-time"><time dateTime={quote ? receivedAt : undefined}>{quote ? clock(receivedAt) : "—"}</time></td>
            <td className="watch-state"><span data-status={status}>{status}</span></td>
            <td className="watch-action"><button type="button" className="rt-remove" aria-label={`移除 ${key.toUpperCase()}`} onClick={() => onRemove(key)}><X className="size-3" /></button></td>
          </tr>
        })}</tbody>
      </table>
      {!visible.length && <p className="rt-watch-no-match" role="status">未找到匹配股票。<button type="button" className="text-button" onClick={() => setQuery("")}>清除筛选</button></p>}
    </div>
    <div className="rt-watch-foot"><span>悬浮行查看完整数值；换手率缺少流通股本，未计算。</span><span>时间为本机接收 · 休市可能返回上个交易日行情</span></div>
  </>
}
