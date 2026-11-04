import type { FormEvent, ReactNode } from "react"
import {
  ArrowRight,
  CalendarDays,
  Check,
  ChevronDown,
  FolderOpen,
  Info,
  LoaderCircle,
  Network,
  SlidersHorizontal
} from "lucide-react"
import type { DownloadOptions } from "@/types"
import { monthsAgo, periods, today, yesterday } from "@/lib/dates"

export type Scope = "markets" | "codes"
export type FormMessage = { type: "info" | "success" | "error"; text: string }
interface Props {
  options: DownloadOptions
  scope: Scope
  active: boolean
  starting: boolean
  testing: boolean
  ready: boolean
  message: FormMessage
  onOptionsChange: (options: DownloadOptions) => void
  onScopeChange: (scope: Scope) => void
  onSubmit: () => void
  onTest: () => void
  onBrowse: (kind: "folder" | "gbbq") => void
}
const markets = [
  { value: 1, name: "上海", code: "SH" },
  { value: 0, name: "深圳", code: "SZ" },
  { value: 2, name: "北京", code: "BJ" }
]
const presets = [
  { label: "近 1 月", months: 1 },
  { label: "近 3 月", months: 3 },
  { label: "近 1 年", months: 12 },
  { label: "近 3 年", months: 36 }
]

export function DownloadForm(props: Props) {
  const { options, scope, active, starting, testing, ready, message } = props
  const update = <K extends keyof DownloadOptions>(key: K, value: DownloadOptions[K]) =>
    props.onOptionsChange({ ...options, [key]: value })
  const submit = (event: FormEvent) => {
    event.preventDefault()
    props.onSubmit()
  }
  const busy = active || starting || testing || !ready
  return (
    <form onSubmit={submit} noValidate className="setup-form">
      <div className="panel-heading">
        <div className="eyebrow">下载配置</div>
        <h2>准备你的行情数据</h2>
        <p>选好证券和时间，剩下的交给 QFLUX。</p>
      </div>
      <fieldset disabled={busy} className="min-w-0 disabled:opacity-60">
        <div className="mb-5">
          <label htmlFor="data-source" className="form-label">
            数据来源
          </label>
          <select
            id="data-source"
            className="form-control select-control"
            value={options.source || "tdx"}
            onChange={(event) => {
              const source = event.target.value as "tdx" | "baostock"
              props.onOptionsChange({
                ...options,
                source,
                markets:
                  source === "baostock"
                    ? options.markets.filter((market) => market !== 2)
                    : options.markets,
                category:
                  source === "baostock" && [7, 8, 10, 11].includes(options.category)
                    ? 9
                    : options.category
              })
            }}
          >
            <option value="tdx">通达信 · 沪深北行情 / 1 分钟</option>
            <option value="baostock">BaoStock · 沪深历史 / 含退市名录</option>
          </select>
          <p className="help-text">
            {options.source === "baostock"
              ? "无需账号，顺序采集历史证券名单，并同步上市/退市日期与交易日历。"
              : "当前码表并非全部历史证券。沪深退市资料可用 BaoStock 补充，较早分钟线可本地补录。"}
          </p>
        </div>
        <FormSection step="01" title="选择证券" hint="沪 · 深 · 北 A 股">
          <div className="segmented" aria-label="证券选择方式">
            {(["markets", "codes"] as Scope[]).map((item) => (
              <button
                key={item}
                type="button"
                aria-pressed={scope === item}
                onClick={() => props.onScopeChange(item)}
              >
                {item === "markets" ? "按市场下载" : "指定代码"}
              </button>
            ))}
          </div>
          {scope === "markets" ? (
            <div className="market-grid">
              {markets.map((market) => {
                const checked = options.markets.includes(market.value)
                return (
                  <label
                    className={`market-option ${checked ? "is-selected" : ""}`}
                    key={market.value}
                  >
                    <input
                      type="checkbox"
                      disabled={options.source === "baostock" && market.value === 2}
                      checked={checked}
                      onChange={(event) =>
                        update(
                          "markets",
                          event.target.checked
                            ? [...options.markets, market.value].sort()
                            : options.markets.filter((item) => item !== market.value)
                        )
                      }
                    />
                    <span>
                      {market.name}
                      <small>{market.code}</small>
                    </span>
                    <Check aria-hidden="true" className="market-check" />
                  </label>
                )
              })}
            </div>
          ) : (
            <div className="mt-3">
              <label htmlFor="codes" className="sr-only">
                证券代码
              </label>
              <textarea
                id="codes"
                value={options.codes}
                onChange={(event) => update("codes", event.target.value)}
                placeholder="例如：600000，000001，920002"
                rows={3}
                className="form-control code-input"
              />
              <p className="help-text">支持中文逗号、空格或换行，也可填写 sh600000。</p>
            </div>
          )}
          {scope === "markets" && (
            <p className="help-text">
              仅采集 A 股，排除指数、基金、债券与 B 股。退市历史可通过本地补录加入。
            </p>
          )}
        </FormSection>

        <FormSection step="02" title="选择时间" hint="需要哪段历史？">
          <div className="segmented mb-3" aria-label="采集方式">
            <button
              type="button"
              aria-pressed={!options.incremental}
              onClick={() => update("incremental", false)}
            >
              历史建库
            </button>
            <button
              type="button"
              aria-pressed={options.incremental}
              onClick={() => update("incremental", true)}
            >
              增量维护
            </button>
          </div>
          <label htmlFor="category" className="form-label">
            数据周期
          </label>
          <select
            id="category"
            value={options.category === 4 ? 9 : options.category === 8 ? 7 : options.category}
            onChange={(event) => update("category", Number(event.target.value))}
            className="form-control select-control"
          >
            {periods.map((period) => (
              <option
                key={period.value}
                value={period.value}
                disabled={options.source === "baostock" && [7, 10, 11].includes(period.value)}
              >
                {period.label}
              </option>
            ))}
          </select>
          <button
            type="button"
            className="text-button mb-2 mt-3"
            onClick={() =>
              props.onOptionsChange({
                ...options,
                startDate: "1990-12-19",
                endDate: yesterday(),
                markets: options.source === "baostock" ? [0, 1] : [0, 1, 2],
                aSharesOnly: true,
                incremental: false,
                storage: "duckdb"
              })
            }
          >
            {options.source === "baostock" ? "使用沪深历史建库配置" : "使用全 A 股历史建库配置"}
            <ArrowRight className="size-3" />
          </button>
          <div className="date-presets">
            {presets.map((preset) => (
              <button
                key={preset.months}
                type="button"
                aria-pressed={
                  options.startDate === monthsAgo(preset.months) && options.endDate === today()
                }
                onClick={() =>
                  props.onOptionsChange({
                    ...options,
                    startDate: monthsAgo(preset.months),
                    endDate: today()
                  })
                }
              >
                {preset.label}
              </button>
            ))}
          </div>
          <div className="date-grid">
            <div>
              <label htmlFor="start-date" className="form-label">
                开始日期
              </label>
              <input
                id="start-date"
                type="date"
                lang="zh-CN"
                value={options.startDate}
                max={options.endDate || today()}
                onChange={(event) => update("startDate", event.target.value)}
                className="form-control"
              />
            </div>
            <div>
              <label htmlFor="end-date" className="form-label">
                结束日期
              </label>
              <input
                id="end-date"
                type="date"
                lang="zh-CN"
                value={options.endDate}
                min={options.startDate}
                max={today()}
                onChange={(event) => update("endDate", event.target.value)}
                className="form-control"
              />
            </div>
          </div>
          <div className="date-note">
            <CalendarDays className="size-4 shrink-0" />
            <span>
              包含开始与结束当天，按北京时间筛选。
              <br />
              {options.incremental
                ? "保留已有历史，回看最近 7 天以更新近期记录。"
                : "自动分批合并；源头历史不足时会明确提示。"}
            </span>
          </div>
          {(options.category < 4 || options.category === 7 || options.category === 8) && (
            <p className="notice notice-warn mt-3">
              分钟数据的历史长度取决于行情源。下载后请查看实际覆盖日期。
            </p>
          )}
        </FormSection>

        <FormSection step="03" title="存储与归档" hint="原始价 · 历史长期保留">
          <label htmlFor="storage" className="form-label">
            存储方式
          </label>
          <select
            id="storage"
            value={options.storage}
            onChange={(event) => update("storage", event.target.value as "duckdb" | "csv")}
            className="form-control select-control mb-3"
          >
            <option value="duckdb">DuckDB 数据仓库 + 原始 CSV</option>
            <option value="csv">仅归档原始 CSV</option>
          </select>
          <label htmlFor="output-dir" className="sr-only">
            保存目录
          </label>
          <div className="path-field">
            <input
              id="output-dir"
              value={options.outputDir}
              onChange={(event) => update("outputDir", event.target.value)}
              placeholder="选择一个保存目录"
              className="form-control"
            />
            <button
              type="button"
              onClick={() => props.onBrowse("folder")}
              className="icon-button"
              aria-label="浏览保存目录"
            >
              <FolderOpen className="size-4" />
            </button>
          </div>
          <p className="help-text">
            新数据按时间合并到已有历史，重叠记录以新值为准；空结果或失败保留原文件。
          </p>
        </FormSection>

        <details className="advanced-settings">
          <summary>
            <span className="flex items-center gap-2">
              <SlidersHorizontal className="size-4" />
              高级设置
            </span>
            <ChevronDown className="size-4" />
          </summary>
          <div className="advanced-body">
            <p className="help-text mb-4 mt-0">通常保持默认即可。行情线路会在下载前自动检测。</p>
            {options.source !== "baostock" ? (
              <div className="date-grid">
                <NumberField
                  id="workers"
                  label="同时下载数量"
                  value={options.workers}
                  min={1}
                  max={64}
                  onChange={(value) => update("workers", value)}
                />
                <NumberField
                  id="timeout"
                  label="等待超时（秒）"
                  value={options.timeout}
                  min={2}
                  max={60}
                  onChange={(value) => update("timeout", value)}
                />
              </div>
            ) : (
              <p className="help-text mb-3">
                BaoStock 使用单个顺序会话，自动节流；单次操作有独立超时与取消保护。
              </p>
            )}
            {options.source === "baostock" && (
              <label className="mb-4 flex items-center gap-2 text-xs text-slate-500">
                <input
                  type="checkbox"
                  className="size-4 accent-blue-600"
                  checked={Boolean(options.collectFactors)}
                  onChange={(event) => update("collectFactors", event.target.checked)}
                />
                同步 BaoStock 独立复权因子（不修改原始价）
              </label>
            )}
            <label htmlFor="gbbq-path" className="form-label mt-4">
              复权因子文件 <span className="text-slate-400">（可选）</span>
            </label>
            <div className="path-field">
              <input
                id="gbbq-path"
                value={options.gbbqPath}
                onChange={(event) => update("gbbqPath", event.target.value)}
                placeholder="选择通达信 gbbq 文件"
                className="form-control"
              />
              <button
                type="button"
                onClick={() => props.onBrowse("gbbq")}
                className="icon-button"
                aria-label="浏览 gbbq 文件"
              >
                <FolderOpen className="size-4" />
              </button>
            </div>
            <p className="help-text">用于预览前复权、后复权价格，原始行情始终保持不复权。</p>
            <button
              type="button"
              className="text-button mt-3"
              disabled={busy}
              onClick={props.onTest}
            >
              <Network className="size-4" />
              {testing ? "检测中…" : "检测行情线路"}
            </button>
          </div>
        </details>
      </fieldset>
      <div className="form-actions">
        <div
          className={`form-feedback ${message.type}`}
          role={message.type === "error" ? "alert" : "status"}
          aria-live="polite"
        >
          <Info className="mt-0.5 size-3.5 shrink-0" />
          <span>{message.text}</span>
        </div>
        <button type="submit" disabled={busy} className="primary-button">
          {starting || testing ? <LoaderCircle className="size-4 animate-spin" /> : null}
          {active
            ? "正在处理，可在任务概览查看"
            : starting
              ? "正在准备下载…"
              : testing
                ? "正在检测线路…"
                : !ready
                  ? "正在连接服务…"
                  : "开始下载"}
          {!busy && <ArrowRight className="size-4" />}
        </button>
        <p className="mt-3 text-center text-xs text-slate-400">
          下载开始后，可关闭网页；请保持程序运行。
        </p>
      </div>
    </form>
  )
}
function FormSection({
  step,
  title,
  hint,
  children
}: {
  step: string
  title: string
  hint: string
  children: ReactNode
}) {
  return (
    <section className="form-section">
      <div className="form-section-heading">
        <span className="step-number">{step}</span>
        <h3>{title}</h3>
        <span>{hint}</span>
      </div>
      {children}
    </section>
  )
}
function NumberField({
  id,
  label,
  value,
  min,
  max,
  onChange
}: {
  id: string
  label: string
  value: number
  min: number
  max: number
  onChange: (value: number) => void
}) {
  return (
    <div>
      <label htmlFor={id} className="form-label">
        {label}
      </label>
      <input
        id={id}
        type="number"
        inputMode="numeric"
        min={min}
        max={max}
        step={1}
        value={Number.isNaN(value) ? "" : value}
        onChange={(event) => onChange(event.target.valueAsNumber)}
        className="form-control"
      />
    </div>
  )
}
