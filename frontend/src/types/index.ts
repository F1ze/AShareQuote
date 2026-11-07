export type TaskState =
  | "idle"
  | "preparing"
  | "running"
  | "cancelling"
  | "completed"
  | "completed_with_errors"
  | "cancelled"
  | "failed"
  | "interrupted"

export interface DownloadOptions {
  source?: "tdx" | "baostock"
  collectFactors?: boolean
  aSharesOnly: boolean
  incremental: boolean
  storage: "csv" | "duckdb"
  importDir?: string
  csvVolumeUnit?: "lots" | "shares"
  markets: number[]
  codes: string
  category: number
  start: number
  count: number
  startDate: string
  endDate: string
  workers: number
  timeout: number
  outputDir: string
  gbbqPath: string
}

export interface FileResult {
  category: number
  name: string
  code: string
  market: string
  bars: number
  size: number
  durationMs: number
  createdAt: string
  from: string
  to: string
  warning?: string
}

export interface LogEntry {
  time: string
  level: "info" | "warn" | "error"
  message: string
}

export interface TaskSnapshot {
  id: string
  state: TaskState
  total: number
  done: number
  succeeded: number
  failed: number
  bars: number
  pages: number
  warnings: number
  skipped: number
  percent: number
  speed: number
  elapsed: number
  message: string
  outputDir: string
  startedAt: string
  endedAt?: string
  options: DownloadOptions
  recent: FileResult[]
  logs: LogEntry[]
}

export interface Defaults {
  warehouseEnabled: boolean
  outputDir: string
  gbbqPath: string
  category: number
  count: number
  workers: number
  timeout: number
  markets: number[]
  task: TaskSnapshot | null
}

export interface ProxySettings {
  proxy: string
}

export interface WarehouseDataset {
  exchange: string
  code: string
  name: string
  period: string
  rows: number
  earliest: string
  latest: string
  updatedAt: string
  warning?: string
}

export interface WarehouseStatus {
  ready: boolean
  path: string
  rows: number
  symbols: number
  earliest: string | null
  latest: string | null
  revisions: number
  calendarDays: number
  factorEvents: number
  instrumentsWithoutListingDate: number
  completeness: "unverified"
  periods: { period: string; rows: number; symbols: number }[]
  datasets: WarehouseDataset[]
}

export interface DatasetPage {
  total: number
  items: WarehouseDataset[]
  page: number
  pageSize: number
}

export interface HostStatus {
  address: string
  latencyMs: number
  ok: boolean
  securities?: number
  ticks?: number
  error?: string
}

export interface HostReport {
  tested: number
  available: number
  results: HostStatus[]
}

export interface BrowseEntry {
  name: string
  path: string
  directory: boolean
}

export interface BrowseResult {
  path: string
  parent?: string
  entries: BrowseEntry[]
  roots: { name: string; path: string }[]
}

export interface PreviewBar {
  date: string
  open: number
  high: number
  low: number
  close: number
  volume: number
  amount: number
}

export interface PreviewResult {
  from: string
  to: string
  file: string
  mode: "raw" | "qfq" | "hfq"
  total: number
  factorAvailable: boolean
  factorEvents: number
  bars: PreviewBar[]
}
