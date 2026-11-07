import type {
  BrowseResult,
  Defaults,
  DownloadOptions,
  HostReport,
  PreviewResult,
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
  warehousePreview: async (dataset: WarehouseDataset, limit: number): Promise<PreviewResult> => {
    const rows = await request<
      {
        ts: string
        open: string
        high: string
        low: string
        close: string
        volume_shares: number
        amount_cny: string
      }[]
    >(
      `/warehouse/bars?${new URLSearchParams({ exchange: dataset.exchange, code: dataset.code, period: dataset.period, from: dataset.earliest.slice(0, 10), to: dataset.latest.slice(0, 10), limit: String(limit) })}`
    )
    return {
      file: `${dataset.exchange}${dataset.code}`,
      mode: "raw",
      from: dataset.earliest,
      to: dataset.latest,
      total: dataset.rows,
      factorAvailable: false,
      factorEvents: 0,
      bars: rows.map((row) => ({
        date: row.ts,
        open: Number(row.open),
        high: Number(row.high),
        low: Number(row.low),
        close: Number(row.close),
        volume: row.volume_shares,
        amount: Number(row.amount_cny)
      }))
    }
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
    }),
  preview: (file: string, mode: "raw" | "qfq" | "hfq", limit = 200) =>
    request<PreviewResult>(`/preview?${new URLSearchParams({ file, mode, limit: String(limit) })}`)
}
