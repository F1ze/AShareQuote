import { useEffect, useMemo, useRef, useState } from "react"
import { Area, AreaChart, Brush, CartesianGrid, ReferenceArea, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts"
import { api } from "@/lib/api"
import { chartPoints, dateWindow, type WindowRange } from "@/lib/preview"
import type { PreviewResult, WarehouseDataset } from "@/types"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"

const modes = [{ value: "raw" as const, label: "不复权" }, { value: "qfq" as const, label: "前复权" }, { value: "hfq" as const, label: "后复权" }]
const number = new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 2 })
const price = (value: number) => Number.isFinite(value) ? value.toFixed(3) : "—"

export function PricePreview({ file, onOpenChange, warehouseDataset }: { file: string | null; onOpenChange: (open: boolean) => void; warehouseDataset?: WarehouseDataset }) {
  return <Sheet open={Boolean(file)} onOpenChange={onOpenChange}>
    <SheetContent className="block w-full overflow-y-auto border-[var(--line)] bg-[var(--surface)] p-0 sm:max-w-5xl">
      <SheetHeader className="border-b border-[var(--line)] px-6 py-6 text-left sm:px-8">
        <div className="metadata">行情预览</div>
        <SheetTitle className="break-all font-mono text-lg font-medium">{file || "行情预览"}</SheetTitle>
        <SheetDescription className="text-sm text-[var(--text-secondary)]">
          {warehouseDataset ? "DuckDB 原始价 · 成交量单位：股 / 份 / 张" : "复权基于全部历史实时计算，原始 CSV 保持不变 · 成交量单位：百股 / 百份 / 百张"} · 价格与成交额单位：元
        </SheetDescription>
      </SheetHeader>
      {file && <PreviewLoader key={`${file}:${warehouseDataset?.period || "csv"}`} file={file} dataset={warehouseDataset} />}
    </SheetContent>
  </Sheet>
}

function PreviewLoader({ file, dataset }: { file: string; dataset?: WarehouseDataset }) {
  const [mode, setMode] = useState<"raw" | "qfq" | "hfq">("raw")
  const [result, setResult] = useState<PreviewResult | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [loaded, setLoaded] = useState(0)
  const [retry, setRetry] = useState(0)
  useEffect(() => {
    // 切换文件、复权模式或关闭面板时终止旧请求，防止迟到的响应覆盖当前预览。
    const controller = new AbortController()
    const query = dataset ? api.warehousePreviewAll(dataset, controller.signal, (count) => {
      if (!controller.signal.aborted) setLoaded(count)
    }) : api.previewAll(file, mode, controller.signal)
    void query.then((data) => { if (!controller.signal.aborted) setResult(data) })
      .catch((reason: unknown) => { if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : "读取行情失败") })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [file, dataset, mode, retry])
  return <>
    <div className="border-b border-[var(--line)] px-6 py-5 sm:px-8">
      <div className="segmented w-full sm:w-80" style={{ gridTemplateColumns: "repeat(3,minmax(0,1fr))" }}>
        {modes.map((item) => <button key={item.value} type="button" aria-pressed={mode === item.value}
          disabled={loading || (item.value !== "raw" && !result?.factorAvailable)}
          title={item.value !== "raw" && !result?.factorAvailable ? "当前数据没有可用于预览的复权事件" : undefined}
          onClick={() => { if (mode !== item.value) { setLoading(true); setError(""); setLoaded(0); setMode(item.value) } }}>{item.label}</button>)}
      </div>
    </div>
    {error && <div role="alert" className="m-6 rounded-lg bg-[var(--danger-surface)] p-4 text-sm text-[var(--danger)]">{error}
      <button className="ml-3 underline" onClick={() => { setError(""); setLoading(true); setLoaded(0); setRetry((value) => value + 1) }}>重新读取</button></div>}
    {loading && <div role="status" className="grid h-64 place-items-center px-6 text-sm text-[var(--text-secondary)]">正在读取完整历史行情{loaded ? `，已读取 ${number.format(loaded)} 条` : "…"}</div>}
    {!loading && !error && result && <PreviewExplorer key={mode} result={result} />}
  </>
}

function PreviewExplorer({ result }: { result: PreviewResult }) {
  const { bars } = result
  const [range, setRange] = useState<WindowRange>({ start: 0, end: bars.length - 1 })
  const [from, setFrom] = useState(bars[0]?.date.slice(0, 10) || "")
  const [to, setTo] = useState(bars.at(-1)?.date.slice(0, 10) || "")
  const [error, setError] = useState("")
  const [page, setPage] = useState(0)
  const [drag, setDrag] = useState<WindowRange | null>(null)
  const dragRef = useRef<WindowRange | null>(null)
  const points = useMemo(() => chartPoints(bars, range), [bars, range])
  const overview = useMemo(() => chartPoints(bars, { start: 0, end: bars.length - 1 }, 600), [bars])
  const count = Math.max(0, range.end - range.start + 1)
  const pages = Math.max(1, Math.ceil(count / 100))
  // 只截取当前页再反转；完整分钟历史不会产生同等数量的 DOM 行。
  const rows = bars.slice(Math.max(range.start, range.end - page * 100 - 99), range.end - page * 100 + 1).reverse()
  const select = (next: WindowRange) => {
    setRange(next); setFrom(bars[next.start].date.slice(0, 10)); setTo(bars[next.end].date.slice(0, 10)); setPage(0); setError("")
  }
  const finishDrag = () => {
    const selection = dragRef.current
    dragRef.current = null; setDrag(null)
    if (selection && selection.start !== selection.end) select({ start: Math.min(selection.start, selection.end), end: Math.max(selection.start, selection.end) })
  }
  const brushStart = Math.max(0, overview.findIndex((point) => point.index >= range.start))
  let brushEnd = overview.findIndex((point) => point.index >= range.end)
  if (brushEnd < 0) brushEnd = overview.length - 1
  return <div className="min-w-0 px-4 pb-8 pt-5 sm:px-8">
    <div className="text-xs leading-6 text-[var(--text-secondary)]" data-testid="preview-coverage">
      完整数据：{result.from || "—"} → {result.to || "—"} · 共 {number.format(bars.length)} 条
    </div>
    {!bars.length ? <p className="py-16 text-center text-sm text-[var(--text-muted)]">暂无行情数据</p> : <>
      <form className="my-5 grid grid-cols-2 items-end gap-3 sm:flex sm:flex-wrap" onSubmit={(event) => {
        event.preventDefault()
        const next = dateWindow(bars, from, to)
        if (next) select(next)
        else setError(from > to ? "开始日期不能晚于结束日期" : "所选日期范围没有行情数据，请调整日期")
      }}>
        <label className="min-w-0 flex-1 text-xs sm:flex-none">开始日期<input aria-label="开始日期" type="date" required className="form-control mt-2 w-full" value={from} onChange={(event) => setFrom(event.target.value)} /></label>
        <label className="min-w-0 flex-1 text-xs sm:flex-none">结束日期<input aria-label="结束日期" type="date" required className="form-control mt-2 w-full" value={to} onChange={(event) => setTo(event.target.value)} /></label>
        <button type="submit" className="secondary-button">应用日期</button>
        <button type="button" className="secondary-button" onClick={() => select({ start: 0, end: bars.length - 1 })}>查看全部</button>
      </form>
      {error && <p role="alert" className="mb-3 text-sm text-[var(--danger)]">{error}</p>}
      <div className="flex flex-wrap justify-between gap-2 text-xs text-[var(--text-muted)]">
        <span data-testid="preview-selection">当前范围 {number.format(count)} 条 · {bars[range.start].date} → {bars[range.end].date}</span>
        <span>拖动框选放大 · 下方滑块调整范围</span>
      </div>
      <div className="h-80 select-none py-4" data-testid="preview-chart" role="img" aria-label={`${result.file} ${modes.find((item) => item.value === result.mode)?.label}收盘价曲线，可拖动框选放大`}>
        <ResponsiveContainer width="100%" height="100%">
          <AreaChart data={points} margin={{ top: 8, right: 8, left: 0, bottom: 0 }}
            onMouseDown={(state) => {
              const index = Number(state?.activeLabel)
              if (state?.activeLabel == null || !Number.isFinite(index)) return
              dragRef.current = { start: index, end: index }; setDrag(dragRef.current)
            }}
            onMouseMove={(state) => {
              if (!dragRef.current || state?.activeLabel == null) return
              const index = Number(state.activeLabel)
              if (Number.isFinite(index)) { dragRef.current = { ...dragRef.current, end: index }; setDrag(dragRef.current) }
            }} onMouseUp={finishDrag} onMouseLeave={() => { dragRef.current = null; setDrag(null) }}>
            <CartesianGrid stroke="var(--line)" strokeDasharray="3 5" vertical={false} />
            <XAxis type="number" dataKey="index" domain={[range.start, range.end]} allowDataOverflow tickFormatter={(index) => bars[Math.round(Number(index))]?.date.slice(0, 10) || ""} tick={{ fill: "var(--text-muted)", fontSize: 10 }} axisLine={false} tickLine={false} minTickGap={48} />
            <YAxis domain={["auto", "auto"]} width={58} tick={{ fill: "var(--text-muted)", fontSize: 10 }} tickFormatter={(value) => Number(value).toFixed(2)} axisLine={false} tickLine={false} />
            <Tooltip labelFormatter={(index) => bars[Number(index)]?.date || ""} formatter={(value) => [price(Number(value)), "收盘价"]} contentStyle={{ border: "1px solid var(--line-strong)", borderRadius: 10, background: "var(--surface-raised)", color: "var(--text)", fontSize: 12 }} />
            <Area type="linear" dataKey="close" stroke="var(--brand)" fill="var(--brand)" fillOpacity={0.14} strokeWidth={1.5} isAnimationActive={false} />
            {drag && <ReferenceArea x1={Math.min(drag.start, drag.end)} x2={Math.max(drag.start, drag.end)} stroke="var(--brand)" fill="var(--brand)" fillOpacity={0.25} />}
          </AreaChart>
        </ResponsiveContainer>
      </div>
      <div className="h-20" aria-label="完整历史概览">
        <ResponsiveContainer width="100%" height="100%">
          <AreaChart data={overview} margin={{ left: 58, right: 8 }}>
            <Brush ariaLabel="历史范围手柄，使用左右方向键调整" dataKey="date" height={32} stroke="var(--brand)" fill="var(--surface-raised)" travellerWidth={12} startIndex={brushStart} endIndex={brushEnd} tickFormatter={(value) => String(value).slice(0, 10)} onChange={(selection) => {
              if (selection.startIndex == null || selection.endIndex == null) return
              select({ start: overview[selection.startIndex].index, end: overview[selection.endIndex].index })
            }}><AreaChart><Area dataKey="close" stroke="var(--brand)" fill="var(--brand)" fillOpacity={0.15} isAnimationActive={false} /></AreaChart></Brush>
          </AreaChart>
        </ResponsiveContainer>
      </div>
      <p className="mb-5 text-xs text-[var(--text-muted)]">{count > points.length ? `大范围概览绘制 ${number.format(points.length)} 个关键点，放大后显示细节；下方表格保留每条原始记录。` : "当前范围逐条绘制，悬停查看价格。"}</p>
      <div className="mb-3 flex flex-wrap items-center justify-between gap-3 text-xs">
        <span>行情明细 · 最新在前 · 每页 100 条</span>
        <div className="flex flex-wrap items-center gap-2">
          <button className="secondary-button" disabled={page === 0} onClick={() => setPage(0)}>最新</button>
          <button className="secondary-button" disabled={page === 0} onClick={() => setPage(page - 1)} aria-label="上一页行情">上一页</button>
          <span aria-live="polite">{page + 1} / {pages}</span>
          <button className="secondary-button" disabled={page + 1 >= pages} onClick={() => setPage(page + 1)} aria-label="下一页行情">下一页</button>
          <button className="secondary-button" disabled={page + 1 >= pages} onClick={() => setPage(pages - 1)}>最早</button>
        </div>
      </div>
      <div className="overflow-x-auto rounded-lg border border-[var(--line)]"><table className="data-table"><thead><tr><th>日期</th><th>开盘</th><th>最高</th><th>最低</th><th>收盘</th><th>成交量</th><th>成交额</th></tr></thead>
        <tbody>{rows.map((bar) => <tr key={bar.date}><td className="whitespace-nowrap">{bar.date}</td><td>{price(bar.open)}</td><td>{price(bar.high)}</td><td>{price(bar.low)}</td><td>{price(bar.close)}</td><td>{number.format(bar.volume)}</td><td>{number.format(bar.amount)}</td></tr>)}</tbody>
      </table></div>
    </>}
  </div>
}
