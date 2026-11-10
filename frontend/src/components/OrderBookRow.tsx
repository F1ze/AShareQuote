import { useState } from "react"
import type { RealtimeQuote } from "@/types"
import { priceChange } from "@/lib/realtime"

const number = new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 2 })

// 父级按股票设置 key。第一次有效盘口建立固定基准，后续更新不按当前最大档重新归一化。
// 使用单调压缩刻度，既不会让最大档始终占满，也不会因数量超过初始最大值而截断增长。
export function OrderBook({ quote, animated }: { quote?: RealtimeQuote; animated: boolean }) {
  const observed = Math.max(0, ...(quote?.bids || []).map((level) => level.volume), ...(quote?.asks || []).map((level) => level.volume))
  const [reference, setReference] = useState(observed)
  // 首帧无盘口时，第一次非零报价在提交前建立基准；之后不再随每轮最大档重置。
  if (reference === 0 && observed > 0) setReference(observed)
  const scale = reference || observed || 1
  const movement = priceChange(quote)
  return <>
    {[4, 3, 2, 1, 0].map((level) => <OrderBookRow key={`ask${level}`} label={`卖 ${level + 1}`} level={quote?.asks[level]} side="ask" reference={scale} animated={animated} />)}
    <div className={`rt-book-mid ${movement === null || movement === 0 ? "rt-neutral" : movement > 0 ? "rt-up" : "rt-down"}`}>{quote && quote.price > 0 ? quote.price.toFixed(2) : "—"}<span>{movement === null ? "—" : `${movement > 0 ? "+" : ""}${movement.toFixed(2)}%`}</span></div>
    {[0, 1, 2, 3, 4].map((level) => <OrderBookRow key={`bid${level}`} label={`买 ${level + 1}`} level={quote?.bids[level]} side="bid" reference={scale} animated={animated} />)}
  </>
}

export function OrderBookRow({ label, level, side, reference, animated }: { label: string; level?: RealtimeQuote["bids"][number]; side: "ask" | "bid"; reference: number; animated: boolean }) {
  const volume = level && level.price > 0 && Number.isFinite(level.volume) ? Math.max(0, level.volume) : 0
  const fraction = volume / (volume + reference)
  return <div className={`rt-book-row ${side}`} data-level={label}>
    <i className="book-quantity-bar" data-animated={animated} style={{ transform: `scaleX(${fraction})` }} title="条长使用固定基准的压缩刻度；准确数量见右侧数字" />
    <span>{label}</span><strong>{level && level.price > 0 ? level.price.toFixed(2) : "—"}</strong>
    <span className="book-volume">{level ? number.format(level.volume) : "—"}</span>
  </div>
}
