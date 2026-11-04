import { useEffect, useRef, useState } from "react"
import {
  Activity,
  ArrowDownToLine,
  ArrowRight,
  Check,
  ChevronDown,
  CircleAlert,
  Clock3,
  FileChartColumn,
  Files,
  FolderOpen,
  ScanLine,
  Square
} from "lucide-react"
import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip, YAxis } from "recharts"
import { isTaskActive } from "@/hooks/useTask"
import { periodName } from "@/lib/dates"
import type { TaskSnapshot } from "@/types"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger
} from "@/components/ui/alert-dialog"

const number = new Intl.NumberFormat("zh-CN")
const stateNames = {
  idle: "等待下载",
  preparing: "准备中",
  running: "正在下载",
  cancelling: "正在停止",
  completed: "下载完成",
  completed_with_errors: "完成 · 需留意",
  cancelled: "已停止",
  failed: "下载失败",
  interrupted: "曾中断"
}
interface Props {
  task: TaskSnapshot | null
  speedHistory: number[]
  onCancel: () => void
  onPreview: (file: string) => void
  onResume: () => void
}
export function TaskMonitor({ task, speedHistory, onCancel, onPreview, onResume }: Props) {
  const active = isTaskActive(task)
  const state = task?.state || "idle"
  const percent = Math.max(0, Math.min(100, task?.percent || 0))
  const logRef = useRef<HTMLDivElement>(null)
  const followLogs = useRef(true)
  const [logsOpen, setLogsOpen] = useState(false)
  useEffect(() => {
    if (logRef.current && followLogs.current) logRef.current.scrollTop = logRef.current.scrollHeight
  }, [task?.logs, logsOpen])
  const chartData = speedHistory.map((value, index) => ({ sample: index, value }))
  return (
    <div className="monitor-column">
      <section className="monitor-panel status-panel">
        <div className="panel-topline">
          <div>
            <div className="eyebrow">下载概览</div>
            <h2>{task ? "每一步，都看得见" : "你的下一份行情，准备就绪"}</h2>
          </div>
          <span className="state-badge" data-state={state}>
            <i />
            {stateNames[state]}
          </span>
        </div>
        {!task ? (
          <div className="welcome-state">
            <div className="welcome-visual" aria-hidden="true">
              <div className="visual-grid" />
              <div className="visual-bars">
                {[24, 38, 30, 49, 43, 61, 54, 76, 65, 91, 79, 104].map((height, index) => (
                  <i key={index} style={{ height }} />
                ))}
              </div>
              <div className="visual-icon">
                <FileChartColumn className="size-6" />
              </div>
              <span className="visual-tag">
                <Check className="size-3" />
                清晰 · 有序 · 可预览
              </span>
            </div>
            <h3>选一个时间段，开始探索</h3>
            <p>
              无需计算数据条数。系统会自动获取历史行情，
              <br className="hidden sm:block" />
              按时间合并为整洁的 CSV 文件。
            </p>
            <div className="welcome-steps">
              <span>
                <CalendarIcon />
                选择日期
              </span>
              <ArrowRight />
              <span>
                <Files />
                自动合并
              </span>
              <ArrowRight />
              <span>
                <ScanLine />
                查看行情
              </span>
            </div>
          </div>
        ) : (
          <div className="task-progress">
            <div className="task-summary">
              <span>
                {task.options.importDir ? "本地历史补录" : periodName(task.options.category)}
              </span>
              <span>
                {task.options.startDate
                  ? `${task.options.startDate} → ${task.options.endDate}`
                  : "历史行情"}
              </span>
            </div>
            <div className="progress-caption">
              <p>{task.message}</p>
              <strong>
                {task.total ? percent.toFixed(1) : "0"}
                <small>%</small>
              </strong>
            </div>
            <div
              className={`progress-track ${state === "preparing" ? "is-preparing" : ""}`}
              role="progressbar"
              aria-label="证券下载进度"
              aria-valuemin={0}
              aria-valuemax={100}
              aria-valuenow={percent}
            >
              <div style={{ width: `${percent}%` }} />
            </div>
            <div className="progress-meta">
              <span>
                已处理 {number.format(task.done)} / {number.format(task.total)} 只证券
              </span>
              <span>
                已读取 {number.format(task.pages || 0)} 批 · 断点复用 {task.skipped || 0} 个
              </span>
            </div>
            {!active && onResume && (
              <button type="button" className="secondary-button mt-4" onClick={onResume}>
                从已验证断点继续
                <ArrowRight className="size-3" />
              </button>
            )}
            {(task.warnings > 0 || task.failed > 0) && (
              <div className="notice notice-warn mt-4">
                <CircleAlert className="size-4 shrink-0" />
                <span>
                  {task.failed > 0 && `${task.failed} 只证券未成功。`}
                  {task.warnings > 0 && `${task.warnings} 个文件需核对日期覆盖。`}
                  详情见下方结果和运行记录。
                </span>
              </div>
            )}
            {active && (
              <div className="mt-5 flex items-center justify-between gap-3">
                <span className="text-xs text-slate-400">
                  {state === "cancelling"
                    ? "正在安全结束请求，请稍候"
                    : "下载在后台继续，你可以暂时离开页面"}
                </span>
                <AlertDialog>
                  <AlertDialogTrigger asChild>
                    <button type="button" className="stop-button" disabled={state === "cancelling"}>
                      <Square className="size-3" />
                      停止下载
                    </button>
                  </AlertDialogTrigger>
                  <AlertDialogContent className="rounded-2xl">
                    <AlertDialogHeader>
                      <AlertDialogTitle>停止当前下载？</AlertDialogTitle>
                      <AlertDialogDescription>
                        已完成的文件会保留。尚未完整下载的证券不会覆盖原有文件。
                      </AlertDialogDescription>
                    </AlertDialogHeader>
                    <AlertDialogFooter>
                      <AlertDialogCancel>继续下载</AlertDialogCancel>
                      <AlertDialogAction onClick={onCancel} className="bg-red-600 hover:bg-red-700">
                        停止下载
                      </AlertDialogAction>
                    </AlertDialogFooter>
                  </AlertDialogContent>
                </AlertDialog>
              </div>
            )}
          </div>
        )}
        <div className="stats-grid">
          <Stat
            icon={<ArrowDownToLine />}
            label="已保存文件"
            value={number.format(task?.succeeded || 0)}
            unit="个"
          />
          <Stat
            icon={<Files />}
            label="本次读取记录"
            value={number.format(task?.bars || 0)}
            unit="条"
          />
          <Stat
            icon={<Activity />}
            label="平均速度"
            value={task ? (task.speed || 0).toFixed(1) : "—"}
            unit="只/秒"
          />
          <Stat
            icon={<Clock3 />}
            label="已用时间"
            value={task ? formatDuration(task.elapsed) : "—"}
          />
        </div>
      </section>

      <section className="monitor-panel results-panel">
        <div className="panel-topline">
          <div className="flex items-center gap-2.5">
            <span className="section-icon">
              <FolderOpen className="size-4" />
            </span>
            <h2>下载结果</h2>
            <span className="count-pill">{task?.succeeded || 0}</span>
          </div>
          <span className="text-xs text-slate-400">最近 10 个文件</span>
        </div>
        {task?.recent?.length ? (
          <div className="results-list">
            {task.recent.map((file) => (
              <button
                className="result-row"
                key={`${file.name}-${file.createdAt}`}
                onClick={() => onPreview(file.name)}
                type="button"
                aria-label={`预览 ${file.name}`}
              >
                <span className="file-icon">
                  <FileChartColumn className="size-5" />
                </span>
                <span className="result-main">
                  <strong>{file.name}</strong>
                  <span>
                    {file.from?.slice(
                      0,
                      file.category < 4 || file.category === 7 || file.category === 8 ? 16 : 10
                    )}{" "}
                    <span className="text-slate-300">→</span>{" "}
                    {file.to?.slice(
                      0,
                      file.category < 4 || file.category === 7 || file.category === 8 ? 16 : 10
                    )}
                  </span>
                  {file.warning && (
                    <span className="result-warning">
                      <CircleAlert className="size-3 shrink-0" />
                      {file.warning}
                    </span>
                  )}
                </span>
                <span className="result-size">
                  <strong>
                    {number.format(file.bars)} <small>条</small>
                  </strong>
                  <span>{formatBytes(file.size)}</span>
                </span>
                <span className="preview-link">
                  预览
                  <ArrowRight className="size-3.5" />
                </span>
              </button>
            ))}
          </div>
        ) : (
          <div className="results-empty">
            <span className="empty-file-icon">
              <Files className="size-6" strokeWidth={1.4} />
            </span>
            <div>
              <h3>数据会在这里汇集</h3>
              <p>每只证券下载完成后，点击文件即可查看行情曲线。</p>
            </div>
          </div>
        )}
        <div className="results-footer">
          <ShieldIcon />
          <span>{task?.outputDir || "文件直接保存在本机，原始行情保持不复权"}</span>
        </div>
      </section>

      <section className="monitor-panel activity-panel">
        <details open={logsOpen} onToggle={(event) => setLogsOpen(event.currentTarget.open)}>
          <summary>
            <span className="flex items-center gap-2.5">
              <span className="section-icon">
                <Activity className="size-4" />
              </span>
              <span className="font-semibold">运行记录</span>
              <span className="text-xs text-slate-400">
                {task?.logs?.length ? `${task.logs.length} 条` : "暂无记录"}
              </span>
            </span>
            <span className="flex items-center gap-2 text-xs text-slate-400">
              {logsOpen ? "收起" : "查看详情"}
              <ChevronDown className="size-4" />
            </span>
          </summary>
          <div
            ref={logRef}
            onScroll={() => {
              const element = logRef.current
              if (element)
                followLogs.current =
                  element.scrollHeight - element.scrollTop - element.clientHeight < 40
            }}
            className="event-log"
            role="log"
            aria-label="下载运行记录"
          >
            {!task?.logs?.length && (
              <p className="py-6 text-center text-sm text-slate-400">
                任务开始后，会在这里记录下载进展。
              </p>
            )}
            {task?.logs?.map((entry, index) => (
              <div className={`log-entry log-${entry.level}`} key={`${entry.time}-${index}`}>
                <time>{entry.time}</time>
                <i />
                <span>{entry.message}</span>
              </div>
            ))}
          </div>
        </details>
        {chartData.length > 1 && (
          <div className="speed-chart">
            <div className="flex justify-between text-xs text-slate-400">
              <span>下载速度变化</span>
              <span>只 / 秒</span>
            </div>
            <div className="mt-3 h-20" role="img" aria-label="下载速度变化曲线">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={chartData} margin={{ top: 4, right: 0, left: 0, bottom: 0 }}>
                  <defs>
                    <linearGradient id="speed-fill" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="#4263eb" stopOpacity={0.2} />
                      <stop offset="100%" stopColor="#4263eb" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid stroke="#edf0f5" vertical={false} />
                  <YAxis hide domain={[0, "auto"]} />
                  <Tooltip
                    formatter={(value) => [`${Number(value).toFixed(1)} 只/秒`, "平均速度"]}
                    labelFormatter={() => "下载速度"}
                    contentStyle={{ border: "1px solid #e2e8f0", borderRadius: 10, fontSize: 12 }}
                  />
                  <Area
                    dataKey="value"
                    type="monotone"
                    stroke="#4263eb"
                    fill="url(#speed-fill)"
                    strokeWidth={2}
                    isAnimationActive={false}
                  />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          </div>
        )}
      </section>
    </div>
  )
}
function Stat({
  icon,
  label,
  value,
  unit
}: {
  icon: React.ReactNode
  label: string
  value: string
  unit?: string
}) {
  return (
    <div className="stat-item">
      <span>
        {icon}
        {label}
      </span>
      <strong>
        {value}
        <small>{unit}</small>
      </strong>
    </div>
  )
}
function CalendarIcon() {
  return <Clock3 className="size-4" />
}
function ShieldIcon() {
  return <Check className="size-3.5 shrink-0 text-emerald-600" />
}
function formatDuration(seconds: number) {
  const value = Math.max(0, Math.floor(seconds))
  return `${Math.floor(value / 3600) ? `${Math.floor(value / 3600)}:` : ""}${String(Math.floor((value % 3600) / 60)).padStart(2, "0")}:${String(value % 60).padStart(2, "0")}`
}
function formatBytes(bytes: number) {
  return bytes < 1024
    ? `${bytes} B`
    : bytes < 1024 * 1024
      ? `${(bytes / 1024).toFixed(1)} KB`
      : `${(bytes / 1024 / 1024).toFixed(1)} MB`
}
