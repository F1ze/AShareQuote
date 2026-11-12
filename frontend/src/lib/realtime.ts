import type { RealtimeQuote, RealtimeSnapshot } from "@/types"

const clock = new Intl.DateTimeFormat("zh-CN", { hour12: false, hour: "2-digit", minute: "2-digit", second: "2-digit" })

export function realtimeClock(value?: string) {
  const time = value ? Date.parse(value) : NaN
  return Number.isFinite(time) ? clock.format(time) : "—"
}

export const quoteKey = (quote: Pick<RealtimeQuote, "market" | "code">) => `${["sz", "sh", "bj"][quote.market]}${quote.code}`
export const priceChange = (quote?: RealtimeQuote): number | null =>
  quote && quote.price > 0 && quote.lastClose > 0 ? (quote.price / quote.lastClose - 1) * 100 : null

// 与后端保持六位证券和显式市场约定；禁止用 Number 转换以免丢失前导零。
export function stockKey(input: string): string | null {
  const text = input.trim().toLowerCase()
  const match = /^(?:(sh|sz|bj))?(\d{6})$/.exec(text)
  if (!match) return null
  const code = match[2]
  const market = match[1] || (/^[689]/.test(code) ? (/^[89]/.test(code) ? "bj" : "sh") : /^4/.test(code) ? "bj" : "sz")
  return `${market}${code}`
}

export interface WatchSettings { codes: string[]; selected: string; interval: number }
export const defaultWatch: WatchSettings = { codes: ["sh600036", "sh600519", "sz000001", "sz300750"], selected: "sh600036", interval: 3 }

// 存储可能被禁用或内容被手动修改；只接受有限、规范化的自选，坏值回落到默认配置。
export function readWatch(): WatchSettings {
  try {
    const saved = JSON.parse(localStorage.getItem("qflux.realtime") || "null")
    if (!saved || !Array.isArray(saved.codes)) return defaultWatch
    const codes = [...new Set<string>(saved.codes.filter((x: unknown): x is string => typeof x === "string").map(stockKey).filter((x: string | null): x is string => x !== null))].slice(0, 20)
    if (!codes.length) return defaultWatch
    return { codes, selected: codes.includes(saved.selected) ? saved.selected : codes[0], interval: [2, 3, 5, 10, 30].includes(saved.interval) ? saved.interval : 3 }
  } catch { return defaultWatch }
}


// 切股只使主屏明细失效，整批快照仍然是各自代码的有效旧报价。
// 保留原接收时间并按新订阅过滤，不能把 A 的成交窗口显示在 B 的主屏。
export function carryRealtimeQuotes(previous: RealtimeSnapshot | null, codes: string[], selected: string): RealtimeSnapshot | null {
  if (!previous) return null
  const allowed = new Set(codes)
  return { ...previous, selected, quotes: previous.quotes.filter((quote) => allowed.has(quoteKey(quote))), trades: [], detailStatus: "not_requested", detailMessage: "正在读取当前股票的成交窗口…" }
}

// 名称是稳定元数据，不随某一轮报价缺失/名称查询失败而删除；按市场+代码匹配，禁止串股。
export function retainRealtimeNames(previous: Readonly<Record<string, string>>, next: RealtimeSnapshot, codes: string[]) {
  const names: Record<string, string> = {}
  for (const key of codes) if (previous[key]) names[key] = previous[key]
  for (const quote of next.quotes) if (codes.includes(quoteKey(quote)) && quote.name?.trim()) names[quoteKey(quote)] = quote.name.trim()
  return { names, data: { ...next, quotes: next.quotes.map((quote) => ({ ...quote, name: names[quoteKey(quote)] || "" })) } }
}

// 每次 JSON 响应都会创建新数组。成交窗口未变化时复用引用，让图表和明细跳过渲染。
export function retainRealtimeTrades(previous: RealtimeSnapshot | null, next: RealtimeSnapshot) {
  if (!previous || previous.selected !== next.selected || previous.trades.length !== next.trades.length) return next
  const unchanged = next.trades.every((trade, index) => {
    const old = previous.trades[index]
    return trade.time === old.time && trade.price === old.price && trade.volume === old.volume
      && trade.trades === old.trades && trade.side === old.side
  })
  return unchanged ? { ...next, trades: previous.trades } : next
}
