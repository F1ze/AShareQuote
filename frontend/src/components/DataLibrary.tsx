import { useCallback, useEffect, useState, type FormEvent } from "react"
import {
  ArrowDownToLine,
  ArrowRight,
  Check,
  CircleAlert,
  Database,
  FileInput,
  FolderOpen,
  History,
  LoaderCircle,
  RefreshCw,
  Search,
  ShieldCheck
} from "lucide-react"
import { api } from "@/lib/api"
import { isTaskActive } from "@/hooks/useTask"
import { PathBrowser } from "@/components/PathBrowser"
import { PricePreview } from "@/components/PricePreview"
import type { DatasetPage, TaskSnapshot, WarehouseDataset, WarehouseStatus } from "@/types"

interface Props {
  outputDir: string
  active: boolean
  onTaskStarted: (task: TaskSnapshot) => void
}
const number = new Intl.NumberFormat("zh-CN")
const periods = [
  { value: "", label: "所有周期" },
  { value: "day", label: "日线" },
  { value: "1m", label: "1 分钟" },
  { value: "5m", label: "5 分钟" },
  { value: "15m", label: "15 分钟" },
  { value: "30m", label: "30 分钟" },
  { value: "60m", label: "60 分钟" },
  { value: "week", label: "周线" },
  { value: "month", label: "月线" },
  { value: "quarter", label: "季线" },
  { value: "year", label: "年线" }
]
const periodLabel = (value: string) => periods.find((p) => p.value === value)?.label || value
const stateNames: Record<string, string> = {
  completed: "已完成",
  completed_with_errors: "需检查覆盖",
  cancelled: "已停止",
  interrupted: "曾中断",
  failed: "失败",
  preparing: "准备中",
  running: "运行中",
  cancelling: "停止中"
}

export function DataLibrary({ outputDir, active, onTaskStarted }: Props) {
  const [status, setStatus] = useState<WarehouseStatus | null>(null)
  const [jobs, setJobs] = useState<TaskSnapshot[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [action, setAction] = useState("")
  const [notice, setNotice] = useState("")
  const [revision, setRevision] = useState(0)
  const reload = useCallback(() => {
    setLoading(true)
    setError("")
    void Promise.all([api.warehouse(), api.jobs()])
      .then(([warehouse, history]) => {
        setStatus(warehouse)
        setJobs(history)
        setRevision((value) => value + 1)
      })
      .catch((reason: unknown) =>
        setError(reason instanceof Error ? reason.message : "读取数据仓库失败")
      )
      .finally(() => setLoading(false))
  }, [])
  useEffect(reload, [reload])
  const run = async (name: string, work: () => Promise<void>) => {
    setAction(name)
    setNotice("")
    setError("")
    try {
      await work()
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "操作失败")
    } finally {
      setAction("")
    }
  }
  return (
    <div className="library-page">
      <div className="library-toolbar">
        <div>
          <span className="eyebrow">RESEARCH DATA / 本地数据资产</span>
          <h2>每一条行情，都有来处。</h2>
          <p>原始价、统一单位、可重复入库。为研究保留长期可用的数据底座。</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <button
            className="secondary-button"
            disabled={loading || Boolean(action)}
            onClick={reload}
          >
            <RefreshCw className={`size-4 ${loading ? "animate-spin" : ""}`} />
            刷新
          </button>
          <button
            className="primary-button w-auto px-4"
            disabled={!status || Boolean(action)}
            onClick={() =>
              void run("export", async () => {
                const result = await api.exportParquet()
                setNotice(`已导出 Parquet：${result.directory}`)
              })
            }
          >
            {action === "export" ? (
              <LoaderCircle className="size-4 animate-spin" />
            ) : (
              <ArrowDownToLine className="size-4" />
            )}
            导出 Parquet
          </button>
        </div>
      </div>
      {error && (
        <div role="alert" className="library-error">
          <CircleAlert className="size-4 shrink-0" />
          <span>{error}</span>
        </div>
      )}
      {notice && (
        <div role="status" className="library-notice">
          <Check className="size-4 shrink-0" />
          <span>{notice}</span>
        </div>
      )}
      {loading && !status ? (
        <div className="monitor-panel grid min-h-48 place-items-center text-sm text-slate-400">
          <span className="flex items-center gap-2">
            <LoaderCircle className="size-4 animate-spin" />
            正在打开本地 DuckDB…
          </span>
        </div>
      ) : (
        <>
          <div className="library-stats">
            <LibraryStat
              label="已入库证券"
              value={number.format(status?.symbols || 0)}
              meta="仅计有行情的证券"
            />
            <LibraryStat
              label="原始行情记录"
              value={number.format(status?.rows || 0)}
              meta="主键去重 · 重跑不重复"
            />
            <LibraryStat
              label="历史修订"
              value={number.format(status?.revisions || 0)}
              meta="保留被替换的价格版本"
            />
            <LibraryStat
              label="实际时间跨度"
              value={status?.earliest?.slice(0, 10) || "等待首次采集"}
              meta={status?.latest ? `至 ${status.latest.slice(0, 10)}` : "建库后展示真实覆盖"}
            />
          </div>
          <section className="coverage-banner">
            <ShieldCheck className="size-5 shrink-0" />
            <div>
              <h3>已入库，不等于历史已完整</h3>
              <p>
                通达信当前码表不包含全部退市证券，BaoStock
                可补充沪深历史名录；各源分钟历史仍有限。缺少上市资料、完整交易日历和停牌记录时，不会将数据标记为“完整”。
              </p>
              <div className="coverage-tags">
                <span>交易日历 {number.format(status?.calendarDays || 0)} 天</span>
                <span>复权事件 {number.format(status?.factorEvents || 0)} 条</span>
                <span>
                  待补上市日期 {number.format(status?.instrumentsWithoutListingDate || 0)} 只
                </span>
                <span>成交量：股 · 价格/成交额：元</span>
              </div>
            </div>
          </section>
          <DatasetBrowser revision={revision} />
        </>
      )}
      <div className="library-bottom-grid">
        <ImportHistory
          outputDir={outputDir}
          active={active || Boolean(action)}
          onTaskStarted={onTaskStarted}
        />
        <ReferenceData disabled={!status || Boolean(action)} onComplete={reload} />
      </div>
      <section className="monitor-panel library-jobs">
        <div className="panel-topline">
          <div className="flex items-center gap-2">
            <History className="size-4 text-slate-400" />
            <h2>可恢复的任务记录</h2>
          </div>
          <span className="text-xs text-slate-400">最近 50 次</span>
        </div>
        {!jobs.length ? (
          <p className="library-empty">还没有任务。首次采集后会自动记录持久断点。</p>
        ) : (
          jobs.map((job) => (
            <div className="job-row" key={job.id}>
              <span>
                <strong>
                  {job.options.importDir
                    ? "本地历史补录"
                    : `${job.options.incremental ? "增量维护" : "历史采集"} · ${periodLabel(String(job.options.category === 9 ? "day" : job.options.category === 7 ? "1m" : job.options.category === 0 ? "5m" : "历史"))}`}
                </strong>
                <small>
                  {job.startedAt.slice(0, 19).replace("T", " ")} · {job.succeeded} 个文件 ·{" "}
                  {stateNames[job.state] || job.state}
                </small>
              </span>
              <button
                type="button"
                className="text-button shrink-0"
                disabled={active || Boolean(action) || isTaskActive(job)}
                onClick={() =>
                  void run(job.id, async () => onTaskStarted(await api.resume(job.id)))
                }
              >
                {action === job.id ? "正在恢复…" : "从断点继续"}
                <ArrowRight className="size-3" />
              </button>
            </div>
          ))
        )}
      </section>
      <div className="library-db-path">
        <Database className="size-3.5 shrink-0" />
        <span>{status?.path || "默认位置：data/quotes.duckdb"}</span>
        <span className="ml-auto hidden shrink-0 sm:inline">DuckDB · 单写者 · 事务入库</span>
      </div>
    </div>
  )
}

function LibraryStat({ label, value, meta }: { label: string; value: string; meta: string }) {
  return (
    <div className="monitor-panel library-stat">
      <span>{label}</span>
      <strong>{value}</strong>
      <small>{meta}</small>
    </div>
  )
}

function DatasetBrowser({ revision }: { revision: number }) {
  const [draft, setDraft] = useState("")
  const [query, setQuery] = useState("")
  const [period, setPeriod] = useState("")
  const [page, setPage] = useState(1)
  const [response, setResponse] = useState<{
    key: string
    data: DatasetPage | null
    error: string
  }>({ key: "", data: null, error: "" })
  const key = JSON.stringify([query, period, page, revision])
  const result = response.data
  const loading = response.key !== key
  const error = loading ? "" : response.error
  const [selected, setSelected] = useState<WarehouseDataset | null>(null)
  useEffect(() => {
    let cancelled = false
    void api
      .datasets(query, period, page)
      .then((data) => {
        if (!cancelled) setResponse({ key, data, error: "" })
      })
      .catch((reason: unknown) => {
        if (!cancelled)
          setResponse({
            key,
            data: null,
            error: reason instanceof Error ? reason.message : "无法读取数据集"
          })
      })
    return () => {
      cancelled = true
    }
  }, [query, period, page, revision, key])
  return (
    <section className="monitor-panel dataset-browser">
      <div className="panel-topline">
        <h2>行情数据集</h2>
        <span className="count-pill">{number.format(result?.total || 0)} 个</span>
      </div>
      <form
        className="dataset-filters"
        onSubmit={(event) => {
          event.preventDefault()
          setQuery(draft.trim())
          setPage(1)
        }}
      >
        <div className="search-field">
          <Search className="size-4" />
          <input
            className="form-control"
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            placeholder="按证券代码或名称查找"
            aria-label="查找仓库证券"
          />
        </div>
        <select
          className="form-control select-control"
          aria-label="仓库数据周期"
          value={period}
          onChange={(event) => {
            setPeriod(event.target.value)
            setPage(1)
          }}
        >
          {periods.map((item) => (
            <option key={item.value} value={item.value}>
              {item.label}
            </option>
          ))}
        </select>
        <button className="secondary-button" type="submit">
          查询
        </button>
      </form>
      {error && (
        <p role="alert" className="px-6 py-4 text-sm text-red-600">
          {error}
        </p>
      )}
      <div className="dataset-table-wrap">
        <table className="data-table">
          <thead>
            <tr>
              <th>证券 / 市场</th>
              <th>周期</th>
              <th>记录数</th>
              <th>最早日期</th>
              <th>最近日期</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {!loading &&
              result?.items.map((item) => (
                <tr key={`${item.exchange}${item.code}-${item.period}`}>
                  <td>
                    <strong className="text-slate-600">{item.name || item.code}</strong>
                    <span className="ml-2 text-[10px] text-slate-400">
                      {item.exchange} {item.code}
                    </span>
                  </td>
                  <td>{periodLabel(item.period)}</td>
                  <td>{number.format(item.rows)}</td>
                  <td>{item.earliest.slice(0, 10)}</td>
                  <td>{item.latest.slice(0, 10)}</td>
                  <td>
                    <button type="button" className="text-button" onClick={() => setSelected(item)}>
                      查看
                      <ArrowRight className="size-3" />
                    </button>
                  </td>
                </tr>
              ))}
          </tbody>
        </table>
        {loading ? (
          <p className="library-empty">正在读取数据集…</p>
        ) : (
          !result?.items.length && (
            <p className="library-empty">暂无匹配数据。先在采集工作台下载，或补录已有历史文件。</p>
          )
        )}
      </div>
      <div className="dataset-pagination">
        <span>
          第 {page} 页 / 共 {Math.max(1, Math.ceil((result?.total || 0) / 50))} 页
        </span>
        <div className="flex gap-2">
          <button
            className="secondary-button"
            disabled={loading || page === 1}
            onClick={() => setPage((value) => value - 1)}
          >
            上一页
          </button>
          <button
            className="secondary-button"
            disabled={loading || page * 50 >= (result?.total || 0)}
            onClick={() => setPage((value) => value + 1)}
          >
            下一页
          </button>
        </div>
      </div>
      {selected && (
        <PricePreview
          key={`${selected.exchange}${selected.code}${selected.period}`}
          file={`${selected.exchange}${selected.code} · ${periodLabel(selected.period)}`}
          warehouseDataset={selected}
          onOpenChange={(open) => !open && setSelected(null)}
        />
      )}
    </section>
  )
}

function ImportHistory({ outputDir, active, onTaskStarted }: Props) {
  const [path, setPath] = useState("")
  const [unit, setUnit] = useState<"lots" | "shares">("lots")
  const [browse, setBrowse] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")
  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (!path.trim()) {
      setError("请选择历史文件目录")
      return
    }
    setLoading(true)
    setError("")
    try {
      onTaskStarted(
        await api.startTask({
          markets: [],
          codes: "",
          category: 9,
          start: 0,
          count: 800,
          startDate: "",
          endDate: "",
          workers: 1,
          timeout: 8,
          outputDir,
          gbbqPath: "",
          storage: "duckdb",
          incremental: false,
          aSharesOnly: true,
          importDir: path.trim(),
          csvVolumeUnit: unit
        })
      )
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "无法启动补录")
    } finally {
      setLoading(false)
    }
  }
  return (
    <section className="monitor-panel library-card">
      <div className="flex items-center gap-2">
        <FileInput className="size-4 text-blue-500" />
        <h2>补录更早的历史</h2>
      </div>
      <p>支持通达信 .day / .lc1 / .lc5 和规范 CSV。可补充在线源没有的退市证券或较早分钟数据。</p>
      <form onSubmit={(event) => void submit(event)}>
        <label htmlFor="import-directory" className="form-label mt-4">
          历史文件所在目录
        </label>
        <div className="path-field">
          <input
            id="import-directory"
            value={path}
            onChange={(event) => setPath(event.target.value)}
            className="form-control"
            placeholder="选择 vipdoc 或历史 CSV 目录"
          />
          <button
            className="icon-button"
            type="button"
            aria-label="浏览历史文件目录"
            onClick={() => setBrowse(true)}
          >
            <FolderOpen className="size-4" />
          </button>
        </div>
        <label htmlFor="csv-unit" className="form-label mt-3">
          CSV 成交量单位
        </label>
        <select
          id="csv-unit"
          className="form-control select-control"
          value={unit}
          onChange={(event) => setUnit(event.target.value as "lots" | "shares")}
        >
          <option value="lots">手（QFLUX 原始 CSV，1 手 = 100 股）</option>
          <option value="shares">股（外部 CSV）</option>
        </select>
        <p className="help-text">
          二进制通达信文件会自动识别为股，不受此选项影响。CSV 文件名示例：sh600000_day.csv。
        </p>
        {error && (
          <p role="alert" className="mt-3 text-xs text-red-600">
            {error}
          </p>
        )}
        <button disabled={active || loading} type="submit" className="secondary-button mt-4 w-full">
          {loading ? "正在准备补录…" : "校验并导入 DuckDB"}
          <ArrowRight className="size-3.5" />
        </button>
      </form>
      <PathBrowser
        kind={browse ? "folder" : null}
        initialPath={path}
        onOpenChange={setBrowse}
        onSelect={setPath}
      />
    </section>
  )
}

function ReferenceData({ disabled, onComplete }: { disabled: boolean; onComplete: () => void }) {
  const [kind, setKind] = useState("instruments")
  const [path, setPath] = useState("")
  const [source, setSource] = useState("")
  const [loading, setLoading] = useState(false)
  const [message, setMessage] = useState("")
  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (!path.trim() || !source.trim()) {
      setMessage("请填写 CSV 完整路径和来源")
      return
    }
    setLoading(true)
    setMessage("")
    try {
      const result = await api.importReference(path.trim(), kind, source.trim())
      setMessage(`已导入 ${result.rows} 条参考记录`)
      onComplete()
    } catch (reason) {
      setMessage(reason instanceof Error ? reason.message : "导入失败")
    } finally {
      setLoading(false)
    }
  }
  return (
    <section className="monitor-panel library-card">
      <div className="flex items-center gap-2">
        <ShieldCheck className="size-4 text-emerald-600" />
        <h2>完善研究参考数据</h2>
      </div>
      <p>补充上市/退市日期与交易日历，帮助识别候选缺口。没有停牌资料时，缺口仍需人工核对。</p>
      <form onSubmit={(event) => void submit(event)}>
        <label htmlFor="reference-kind" className="form-label mt-4">
          参考类型
        </label>
        <select
          id="reference-kind"
          className="form-control select-control"
          value={kind}
          onChange={(event) => setKind(event.target.value)}
        >
          <option value="instruments">证券名录（含退市状态）</option>
          <option value="calendar">交易日历</option>
        </select>
        <div className="mt-3 grid gap-3">
          <input
            aria-label="参考数据 CSV 路径"
            className="form-control"
            value={path}
            onChange={(event) => setPath(event.target.value)}
            placeholder="参考 CSV 的完整路径"
          />
          <input
            aria-label="参考数据来源"
            className="form-control"
            value={source}
            onChange={(event) => setSource(event.target.value)}
            placeholder="来源名称或链接，用于追溯"
          />
        </div>
        <div className="mt-3 flex items-center justify-between gap-3">
          <a
            className="text-button"
            href={api.templateURL(kind as "calendar" | "instruments")}
            download
          >
            下载 CSV 模板
          </a>
          <button className="secondary-button" disabled={disabled || loading} type="submit">
            {loading ? "导入中…" : "导入参考数据"}
          </button>
        </div>
        {message && (
          <p role="status" className="mt-3 break-words text-xs text-slate-500">
            {message}
          </p>
        )}
      </form>
      <button
        type="button"
        className="text-button mt-4"
        disabled={disabled || loading}
        onClick={() => {
          setLoading(true)
          void api
            .gaps()
            .then((gaps) =>
              setMessage(
                gaps.length
                  ? `最近检测到 ${gaps.length} 个候选缺口（最多展示 200 个），例如 ${gaps[0].exchange}${gaps[0].code} ${gaps[0].date}。这也可能是停牌，不能直接补零。`
                  : "未发现候选缺口；若缺少完整日历或上市日期，这不代表历史完整。"
              )
            )
            .catch((reason: unknown) =>
              setMessage(reason instanceof Error ? reason.message : "检查失败")
            )
            .finally(() => setLoading(false))
        }}
      >
        <Search className="size-3.5" />
        检查候选日期缺口
      </button>
    </section>
  )
}
