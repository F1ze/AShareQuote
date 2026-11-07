import type { PreviewBar } from "../types/index"

export interface WindowRange { start: number; end: number }

// 二分查找包含起止当天的范围，分钟线不会遗漏结束日的盘中记录。
export function dateWindow(bars: PreviewBar[], from: string, to: string): WindowRange | null {
  if (!from || !to || from > to) return null
  const bound = (date: string, upper: boolean) => {
    let lo = 0, hi = bars.length
    while (lo < hi) {
      const mid = (lo + hi) >>> 1
      const day = bars[mid].date.slice(0, 10)
      if (day < date || (upper && day === date)) lo = mid + 1
      else hi = mid
    }
    return lo
  }
  const start = bound(from, false), end = bound(to, true) - 1
  return start <= end ? { start, end } : null
}

// 每桶保留收盘价极值及全局端点；仅减少绘制点，表格和日期索引始终使用原始记录。
export function chartPoints(bars: PreviewBar[], range: WindowRange, budget = 1200) {
  const { start, end } = range
  if (end < start || !bars.length) return []
  const indices = new Set<number>([start, end])
  const step = Math.max(1, Math.ceil((end - start + 1) / Math.max(1, Math.floor((budget - 2) / 2))))
  for (let i = start; i <= end; i += step) {
    let min = i, max = i
    for (let j = i + 1; j < Math.min(i + step, end + 1); j++) {
      if (bars[j].close < bars[min].close) min = j
      if (bars[j].close > bars[max].close) max = j
    }
    indices.add(min); indices.add(max)
  }
  return [...indices].sort((a, b) => a - b).map((index) => ({ ...bars[index], index }))
}
