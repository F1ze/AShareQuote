import { useEffect, useState } from "react"
import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis
} from "recharts"
import { api } from "@/lib/api"
import type { PreviewResult, WarehouseDataset } from "@/types"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle
} from "@/components/ui/sheet"

const modes = [
  { value: "raw" as const, label: "不复权" },
  { value: "qfq" as const, label: "前复权" },
  { value: "hfq" as const, label: "后复权" }
]
const number = new Intl.NumberFormat("zh-CN")

export function PricePreview({
  file,
  onOpenChange,
  warehouseDataset
}: {
  file: string | null
  onOpenChange: (open: boolean) => void
  warehouseDataset?: WarehouseDataset
}) {
  const [mode, setMode] = useState<"raw" | "qfq" | "hfq">("raw")
  const [result, setResult] = useState<PreviewResult | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [limit, setLimit] = useState(200)
  const [factorAvailable, setFactorAvailable] = useState(false)
  const [retry, setRetry] = useState(0)

  useEffect(() => {
    if (!file) return
    let cancelled = false
    const query = warehouseDataset
      ? api.warehousePreview(warehouseDataset, limit)
      : api.preview(file, mode, limit)
    void query
      .then((data) => {
        if (!cancelled) {
          setResult(data)
          setFactorAvailable(data.factorAvailable)
        }
      })
      .catch((reason: unknown) => {
        if (!cancelled) setError(reason instanceof Error ? reason.message : "读取行情失败")
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [file, mode, limit, retry, warehouseDataset])

  const changeMode = (next: "raw" | "qfq" | "hfq") => {
    if (next === mode) return
    setLoading(true)
    setError("")
    setResult(null)
    setMode(next)
  }

  return (
    <Sheet open={Boolean(file)} onOpenChange={onOpenChange}>
      <SheetContent className="block w-full overflow-y-auto border-[var(--line)] bg-[var(--surface)] p-0 sm:max-w-4xl">
        <SheetHeader className="border-b border-[var(--line)] px-6 py-6 text-left sm:px-8">
          <div className="metadata">行情预览</div>
          <SheetTitle className="break-all font-mono text-lg font-medium">
            {file || "行情预览"}
          </SheetTitle>
          <SheetDescription className="text-sm text-[var(--text-secondary)]">
            {warehouseDataset
              ? "DuckDB 原始价预览 · 价格单位：元 · 成交量单位：股。复权事件独立存储，不改写原始价格。"
              : "复权结果在读取时实时计算，原始行情 CSV 始终不变。成交量单位：手。"}
          </SheetDescription>
        </SheetHeader>

        <div className="border-b border-[var(--line)] px-6 py-5 sm:px-8">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div
              className="segmented w-full sm:w-80"
              style={{ gridTemplateColumns: "repeat(3,minmax(0,1fr))" }}
            >
              {modes.map((item) => (
                <button
                  key={item.value}
                  type="button"
                  disabled={item.value !== "raw" && !factorAvailable}
                  title={
                    item.value !== "raw" && !factorAvailable
                      ? "下载时选择复权因子文件后可用"
                      : undefined
                  }
                  aria-pressed={mode === item.value}
                  onClick={() => changeMode(item.value)}
                >
                  {item.label}
                </button>
              ))}
            </div>
            <span className="font-mono text-[10px] text-[var(--text-muted)]">
              {warehouseDataset
                ? "仓库预览仅显示原始价"
                : result?.factorAvailable
                  ? "已配置复权因子"
                  : "未配置复权因子"}
            </span>
          </div>
        </div>

        {error && (
          <div
            role="alert"
            className="m-6 rounded-lg border border-[var(--danger-surface)] bg-[var(--danger-surface)] px-4 py-3 text-sm text-[var(--danger)] sm:mx-8"
          >
            {error}
            <button
              type="button"
              className="ml-3 underline"
              onClick={() => {
                setError("")
                setLoading(true)
                setRetry((value) => value + 1)
              }}
            >
              重新读取
            </button>
          </div>
        )}
        {loading && (
          <div className="grid h-64 place-items-center text-sm text-[var(--text-secondary)]">
            正在读取并实时计算行情…
          </div>
        )}
        {!loading && result && (
          <>
            <div className="px-6 pt-5 text-xs leading-6 text-muted-foreground sm:px-8">
              <span className="text-foreground">
                文件实际覆盖：{result.from} → {result.to}
              </span>
              <div className="mt-2 flex flex-wrap items-center justify-between gap-2">
                <span>
                  共 {number.format(result.total)} 条 · 曲线显示最近 {result.bars.length} 条
                </span>
                <label className="flex items-center gap-2">
                  查看范围
                  <select
                    aria-label="预览记录范围"
                    className="rounded-md border border-border bg-card p-1.5 text-xs"
                    value={limit}
                    onChange={(event) => {
                      setLoading(true)
                      setError("")
                      setLimit(Number(event.target.value))
                    }}
                  >
                    <option value={200}>最近 200 条</option>
                    <option value={800}>最近 800 条</option>
                  </select>
                </label>
              </div>
            </div>
            <div
              className="h-64 px-4 py-6 sm:px-6"
              role="img"
              aria-label={`${file} ${modes.find((item) => item.value === mode)?.label}收盘价曲线`}
            >
              {result.bars.length ? (
                <ResponsiveContainer width="100%" height="100%">
                  <AreaChart data={result.bars} margin={{ top: 8, right: 8, left: 4, bottom: 0 }}>
                    <CartesianGrid stroke="var(--line)" strokeDasharray="3 5" vertical={false} />
                    <XAxis
                      dataKey="date"
                      tick={{ fill: "var(--text-muted)", fontSize: 10 }}
                      axisLine={false}
                      tickLine={false}
                      minTickGap={48}
                    />
                    <YAxis
                      domain={["auto", "auto"]}
                      tick={{ fill: "var(--text-muted)", fontSize: 10, fontFamily: "monospace" }}
                      axisLine={false}
                      tickLine={false}
                      width={58}
                      tickFormatter={(value) => Number(value).toFixed(2)}
                    />
                    <Tooltip
                      formatter={(value) => [formatPrice(Number(value)), "收盘价"]}
                      contentStyle={{
                        border: "1px solid var(--line-strong)",
                        borderRadius: 10,
                        background: "var(--surface-raised)",
                        color: "var(--text)",
                        fontSize: 12
                      }}
                    />
                    <Area
                      type="monotone"
                      dataKey="close"
                      stroke="var(--brand)"
                      fill="var(--brand)"
                      fillOpacity={0.14}
                      strokeWidth={1.5}
                      isAnimationActive={false}
                    />
                  </AreaChart>
                </ResponsiveContainer>
              ) : (
                <div className="grid h-full place-items-center text-sm text-[var(--text-muted)]">
                  文件中没有 K 线数据
                </div>
              )}
            </div>
            <div className="px-6 pb-8 sm:px-8">
              <div className="mb-3 flex justify-between font-mono text-[10px] text-[var(--text-muted)]">
                <span>最近 15 根 / 共 {result.total} 根</span>
                <span>{mode.toUpperCase()}</span>
              </div>
              <div className="overflow-x-auto bg-card">
                <table className="data-table">
                  <thead>
                    <tr>
                      <th>日期</th>
                      <th>开盘</th>
                      <th>最高</th>
                      <th>最低</th>
                      <th>收盘</th>
                      <th>成交量</th>
                    </tr>
                  </thead>
                  <tbody>
                    {result.bars
                      .slice(-15)
                      .reverse()
                      .map((bar) => (
                        <tr key={bar.date}>
                          <td>{bar.date}</td>
                          <td>{formatPrice(bar.open)}</td>
                          <td>{formatPrice(bar.high)}</td>
                          <td>{formatPrice(bar.low)}</td>
                          <td>{formatPrice(bar.close)}</td>
                          <td>{number.format(Math.round(bar.volume))}</td>
                        </tr>
                      ))}
                  </tbody>
                </table>
              </div>
            </div>
          </>
        )}
      </SheetContent>
    </Sheet>
  )
}

function formatPrice(value: number) {
  return Number.isFinite(value) ? value.toFixed(3) : "—"
}
