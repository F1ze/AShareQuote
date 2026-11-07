import type {
  BrowseResult,
  Defaults,
  DownloadOptions,
  HostReport,
  PreviewResult,
  PreviewBar,
  WarehouseBar,
  ProxySettings,
  TaskSnapshot,
  WarehouseStatus,
  DatasetPage,
  WarehouseDataset
} from "@/types"

const API_BASE = (import.meta.env.VITE_API_BASE || "/api").replace(/\/$/, "")

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers: init?.body ? { "Content-Type": "application/json", ...init.headers } : init?.headers
  })
  const data = await response.json().catch(() => null)
  if (!response.ok) {
    throw new Error(data?.error || `请求失败（${response.status}）`)
  }
  return data as T
}

export const api = {
  eventsURL: `${API_BASE}/events`,
  defaults: () => request<Defaults>("/defaults"),
  proxy: () => request<ProxySettings>("/proxy"),
  saveProxy: (proxy: string) =>
    request<ProxySettings>("/proxy", { method: "POST", body: JSON.stringify({ proxy }) }),
  task: () => request<TaskSnapshot | null>("/task"),
  jobs: () => request<TaskSnapshot[]>("/jobs"),
  resume: (id: string) =>
    request<TaskSnapshot>(`/jobs/${encodeURIComponent(id)}/resume`, { method: "POST" }),
  warehouse: () => request<WarehouseStatus>("/warehouse"),
  datasets: (q: string, period: string, page: number) =>
    request<DatasetPage>(
      `/warehouse/datasets?${new URLSearchParams({ q, period, page: String(page) })}`
    ),
  exportParquet: () =>
    request<{ path: string; directory: string; bytes: number }>("/warehouse/export", {
      method: "POST"
    }),
  importReference: (path: string, kind: string, source: string) =>
    request<{ rows: number }>("/warehouse/reference", {
      method: "POST",
      body: JSON.stringify({ path, kind, source })
    }),
  gaps: () =>
    request<{ exchange: string; code: string; name: string; date: string }[]>("/warehouse/gaps"),
  templateURL: (kind: "bars" | "calendar" | "instruments") => `${API_BASE}/templates/${kind}`,
  previewAll: (file: string, mode: "raw" | "qfq" | "hfq", signal: AbortSignal) =>
    request<PreviewResult>(`/preview?${new URLSearchParams({ file, mode, all: "true" })}`, { signal }),
  warehousePreviewAll: async (dataset: WarehouseDataset, signal: AbortSignal, progress: (count: number) => void): Promise<PreviewResult> => {
    const pages: PreviewBar[][] = []
    let before = ""
    let count = 0
    if (dataset.earliest && dataset.latest) {
      for (;;) {
        signal.throwIfAborted()
        const rows = await request<WarehouseBar[]>(`/warehouse/bars?${new URLSearchParams({ exchange: dataset.exchange, code: dataset.code, period: dataset.period, from: dataset.earliest.slice(0, 10), to: dataset.latest.slice(0, 10), limit: "1000", before })}`, { signal })
        if (!rows.length) break
        const cursor = rows[0].ts
        if (before && cursor >= before) throw new Error("历史分页未前进，请重新读取")
        // 每页立即转换，避免全量数字记录之外再保留一份价格字符串。
        pages.push(rows.map((row) => ({
          date: row.ts, open: Number(row.open), high: Number(row.high),
          low: Number(row.low), close: Number(row.close),
          volume: row.volume_shares, amount: Number(row.amount_cny)
        })))
        count += rows.length
        progress(count)
        if (rows.length < 1000) break
        before = cursor
      }
    }
    signal.throwIfAborted()
    const bars = pages.reverse().flat()
    return { file: `${dataset.exchange}${dataset.code}`, mode: "raw", from: bars[0]?.date || "", to: bars.at(-1)?.date || "", total: bars.length, factorAvailable: false, factorEvents: 0, bars }
  },
  startTask: (options: DownloadOptions) =>
    request<TaskSnapshot>("/task", { method: "POST", body: JSON.stringify(options) }),
  cancelTask: () => request<TaskSnapshot>("/task", { method: "DELETE" }),
  testHosts: (timeout: number) =>
    request<HostReport>("/hosts/test", { method: "POST", body: JSON.stringify({ timeout }) }),
  browse: (path: string, kind: "folder" | "gbbq") =>
    request<BrowseResult>(`/fs/browse?${new URLSearchParams({ path, kind })}`),
  createDirectory: (path: string, name: string) =>
    request<{ path: string }>("/fs/directories", {
      method: "POST",
      body: JSON.stringify({ path, name })
    })
}
