import { useCallback, useEffect, useRef, useState } from "react"
import {
  Activity,
  ArrowUpRight,
  CircleCheck,
  LoaderCircle,
  RefreshCw,
  ShieldCheck
} from "lucide-react"
import { toast, Toaster } from "sonner"
import { DownloadForm, type FormMessage, type ProxyState, type Scope } from "@/components/DownloadForm"
import { PathBrowser } from "@/components/PathBrowser"
import { PricePreview } from "@/components/PricePreview"
import { DataLibrary } from "@/components/DataLibrary"
import { TaskMonitor } from "@/components/TaskMonitor"
import { isTaskActive, useTask } from "@/hooks/useTask"
import { api } from "@/lib/api"
import { today, yesterday } from "@/lib/dates"
import type { DownloadOptions, TaskSnapshot } from "@/types"

const initialOptions: DownloadOptions = {
  markets: [0, 1, 2],
  source: "tdx",
  collectFactors: true,
  aSharesOnly: true,
  incremental: false,
  storage: "duckdb",
  codes: "",
  category: 9,
  start: 0,
  count: 800,
  startDate: "1990-12-19",
  endDate: yesterday(),
  workers: 12,
  timeout: 8,
  outputDir: "",
  gbbqPath: ""
}

export default function App() {
  const { task, setTask, connected } = useTask()
  const [page, setPage] = useState<"collect" | "library">("collect")
  const [options, setOptions] = useState<DownloadOptions>(initialOptions)
  const [scope, setScope] = useState<Scope>("markets")
  const [message, setMessage] = useState<FormMessage>({ type: "info", text: "正在连接行情服务…" })
  const [ready, setReady] = useState(false)
  const [starting, setStarting] = useState(false)
  const [testing, setTesting] = useState(false)
  const [browser, setBrowser] = useState<"folder" | "gbbq" | null>(null)
  const [previewFile, setPreviewFile] = useState<string | null>(null)
  const [proxy, setProxy] = useState<ProxyState>({ value: "", active: "", saving: false })
  const [speedHistory, setSpeedHistory] = useState<number[]>([])
  const speedTask = useRef("")
  const lastSpeedAt = useRef(0)
  const active = isTaskActive(task)

  const loadDefaults = useCallback(() => {
    setMessage({ type: "info", text: "正在连接行情服务…" })
    void api
      .defaults()
      .then((defaults) => {
        const restored = defaults.task?.options
        setOptions({
          ...initialOptions,
          outputDir: defaults.outputDir,
          gbbqPath: defaults.gbbqPath,
          category: defaults.category,
          workers: defaults.workers,
          timeout: defaults.timeout,
          markets: defaults.markets,
          ...restored,
          importDir: undefined,
          source: restored?.source === "baostock" ? "baostock" : "tdx",
          storage: restored?.storage === "csv" ? "csv" : "duckdb",
          aSharesOnly: true,
          startDate: restored?.startDate || "1990-12-19",
          endDate: restored?.endDate || yesterday()
        })
        if (restored?.codes) setScope("codes")
        setReady(true)
        setMessage({ type: "info", text: "已就绪，下载前会自动检测行情线路。" })
      })
      .catch((reason: unknown) => {
        setMessage({
          type: "error",
          text: reason instanceof Error ? reason.message : "服务连接失败，请点击右上角重试"
        })
      })
    // 代理是服务级设置而非任务参数；读取失败不阻塞页面就绪。
    void api
      .proxy()
      .then((settings) => setProxy({ value: settings.proxy, active: settings.proxy, saving: false }))
      .catch(() => {
        // 服务未启用代理端点时保持直连显示，不额外打扰用户。
      })
  }, [])
  useEffect(loadDefaults, [loadDefaults])

  useEffect(() => {
    if (!task) return
    if (task.id !== speedTask.current) {
      speedTask.current = task.id
      lastSpeedAt.current = 0
      setSpeedHistory([])
    }
    const now = Date.now()
    if (!isTaskActive(task) || now - lastSpeedAt.current < 900) return
    lastSpeedAt.current = now
    setSpeedHistory((history) => [...history.slice(-47), task.speed || 0])
  }, [task])

  const validate = () => {
    if (scope === "markets" && options.markets.length === 0) return "请至少选择一个市场"
    if (scope === "codes" && !options.codes.trim()) return "请填写至少一个证券代码"
    if (!options.startDate || !options.endDate) return "请选择开始日期和结束日期"
    if (options.startDate > options.endDate) return "开始日期不能晚于结束日期"
    if (options.endDate > today()) return "结束日期不能晚于今天（北京时间）"
    if (!options.outputDir.trim()) return "请选择或填写保存目录"
    if (!Number.isInteger(options.workers) || options.workers < 1 || options.workers > 64)
      return "同时下载数量须为 1–64 的整数，请在高级设置中修改"
    if (!Number.isInteger(options.timeout) || options.timeout < 2 || options.timeout > 60)
      return "等待超时须为 2–60 秒的整数，请在高级设置中修改"
    return ""
  }
  const start = async () => {
    if (!ready || active || starting || testing) return
    const invalid = validate()
    if (invalid) {
      setMessage({ type: "error", text: invalid })
      return
    }
    setStarting(true)
    setPreviewFile(null)
    setMessage({ type: "info", text: "正在准备下载…" })
    try {
      const payload = {
        ...options,
        importDir: undefined,
        start: 0,
        count: 800,
        outputDir: options.outputDir.trim(),
        gbbqPath: options.gbbqPath.trim(),
        markets: scope === "markets" ? options.markets : [],
        codes: scope === "codes" ? options.codes.trim() : ""
      }
      setTask(await api.startTask(payload))
      setSpeedHistory([])
      setMessage({ type: "success", text: "下载已开始，文件完成后可直接预览。" })
    } catch (reason) {
      setMessage({ type: "error", text: reason instanceof Error ? reason.message : "创建任务失败" })
    } finally {
      setStarting(false)
    }
  }
  const testHosts = async () => {
    if (!Number.isInteger(options.timeout) || options.timeout < 2 || options.timeout > 60) {
      setMessage({ type: "error", text: "等待超时须为 2–60 秒的整数" })
      return
    }
    setTesting(true)
    setMessage({ type: "info", text: "正在检测行情线路，请稍候…" })
    try {
      const report = await api.testHosts(options.timeout)
      setMessage({
        type: report.available ? "success" : "error",
        text: `可用行情线路 ${report.available} / ${report.tested} 条${report.available ? "，可以开始下载" : "，请检查网络后重试"}。`
      })
    } catch (reason) {
      setMessage({ type: "error", text: reason instanceof Error ? reason.message : "线路检测失败" })
    } finally {
      setTesting(false)
    }
  }
  const saveProxy = async () => {
    if (proxy.saving) return
    const next = proxy.value.trim()
    setProxy((current) => ({ ...current, saving: true }))
    try {
      const result = await api.saveProxy(next)
      setProxy({ value: result.proxy, active: result.proxy, saving: false })
      toast.success(result.proxy ? "代理已保存，新连接将立即经代理出站" : "已恢复直连，新连接立即生效")
    } catch (reason) {
      setProxy((current) => ({ ...current, saving: false }))
      toast.error(reason instanceof Error ? reason.message : "保存代理设置失败")
    }
  }
  const cancel = async () => {
    try {
      setTask(await api.cancelTask())
      toast.success("正在停止，已完成的文件会保留")
    } catch (reason) {
      toast.error(reason instanceof Error ? reason.message : "停止任务失败")
    }
  }
  const acceptTask = (snapshot: TaskSnapshot) => {
    setTask(snapshot)
    setOptions((current) =>
      snapshot.options.importDir
        ? current
        : { ...current, ...snapshot.options, importDir: undefined }
    )
    if (snapshot.options.codes) setScope("codes")
    else setScope("markets")
    setPage("collect")
  }
  const resume = async () => {
    if (!task) return
    try {
      acceptTask(await api.resume(task.id))
    } catch (reason) {
      toast.error(reason instanceof Error ? reason.message : "恢复失败")
    }
  }
  return (
    <div className="app-shell">
      <header className="app-header">
        <div className="header-inner">
          <a href="/" className="brand" aria-label="QFLUX 首页">
            <span className="brand-mark">
              <Activity className="size-5" />
            </span>
            <span>
              QFLUX<small>A 股研究数据平台</small>
            </span>
          </a>
          <nav className="workspace-nav" aria-label="工作区">
            <button
              type="button"
              aria-current={page === "collect" ? "page" : undefined}
              onClick={() => setPage("collect")}
            >
              数据采集
            </button>
            <button
              type="button"
              aria-current={page === "library" ? "page" : undefined}
              onClick={() => setPage("library")}
            >
              数据仓库
            </button>
          </nav>
          <div className="connection-status" data-connected={connected}>
            {connected ? (
              <CircleCheck className="size-3.5" />
            ) : (
              <LoaderCircle className="size-3.5 animate-spin" />
            )}
            <span>{connected ? "服务已连接" : "正在连接"}</span>
            {!ready && (
              <button
                type="button"
                onClick={loadDefaults}
                aria-label="重新连接服务"
                title="重新连接服务"
              >
                <RefreshCw className="size-3.5" />
              </button>
            )}
          </div>
        </div>
      </header>
      <div className="workspace">
        <div className="workspace-heading">
          <div>
            <div className="eyebrow">市场数据 / WORKSPACE</div>
            <h1>
              {page === "collect" ? "构建你的 A 股数据底座" : "你的量化数据仓库"}
              <span className="title-dot">.</span>
            </h1>
            <p>从历史采集到持续维护，让研究建立在可靠的数据之上。</p>
          </div>
          <span className="privacy-note">
            <ShieldCheck className="size-4" />
            文件保存在你的电脑
          </span>
        </div>
        {page === "collect" ? (
          <main className="workspace-grid">
            <aside className="setup-panel">
              <DownloadForm
                options={options}
                scope={scope}
                active={active}
                starting={starting}
                testing={testing}
                ready={ready}
                message={message}
                proxy={proxy}
                onOptionsChange={setOptions}
                onScopeChange={setScope}
                onProxyChange={(value) => setProxy((current) => ({ ...current, value }))}
                onProxySave={() => void saveProxy()}
                onSubmit={() => void start()}
                onTest={() => void testHosts()}
                onBrowse={setBrowser}
              />
            </aside>
            <TaskMonitor
              task={task}
              speedHistory={speedHistory}
              onCancel={() => void cancel()}
              onPreview={setPreviewFile}
              onResume={() => void resume()}
            />
          </main>
        ) : (
          <DataLibrary outputDir={options.outputDir} active={active} onTaskStarted={acceptTask} />
        )}
        <footer className="workspace-footer">
          <span>
            <Activity className="size-3.5" />
            QFLUX · 让数据触手可及
          </span>
          <span>
            通达信行情源
            <ArrowUpRight className="size-3" /> 原始 CSV · 实时复权预览
          </span>
        </footer>
      </div>
      <PathBrowser
        kind={browser}
        initialPath={browser === "gbbq" ? options.gbbqPath : options.outputDir}
        onOpenChange={(open) => !open && setBrowser(null)}
        onSelect={(path) => {
          if (browser)
            setOptions((current) => ({
              ...current,
              [browser === "folder" ? "outputDir" : "gbbqPath"]: path
            }))
        }}
      />
      {previewFile && (
        <PricePreview
          key={previewFile}
          file={previewFile}
          onOpenChange={(open) => !open && setPreviewFile(null)}
        />
      )}
      <Toaster
        position="bottom-center"
        toastOptions={{ style: { borderRadius: 12, fontFamily: "inherit" } }}
      />
    </div>
  )
}
