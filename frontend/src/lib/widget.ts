import type { WatchSettings } from "@/lib/realtime"

interface NativeWindow {
  close(): Promise<void>
  minimize(): Promise<void>
  setAlwaysOnTop(value: boolean): Promise<void>
  isVisible(): Promise<boolean>
  isMinimized(): Promise<boolean>
}
interface TauriBridge {
  core: { invoke<T>(command: string, args?: Record<string, unknown>): Promise<T> }
  window: { getCurrentWindow(): NativeWindow }
}
declare global { interface Window { __TAURI__?: TauriBridge } }

export const nativeWidget = () => window.__TAURI__
export const widgetInterval = (mainInterval: number) => [10, 15, 30, 60].find((n) => n >= Math.max(10, mainInterval * 2)) || 60

// Tauri 的本地 Rust 代理只允许两种固定 Go API，不给网页通用网络/文件/命令执行权限。
export async function widgetRequest<T>(path: string, signal?: AbortSignal): Promise<T> {
  const bridge = nativeWidget()
  if (!bridge) throw new Error("桌面桥接未启用")
  if (signal?.aborted) throw signal.reason
  if (path === "/realtime/watch") return bridge.core.invoke<T>("watch_settings")
  const params = new URLSearchParams(path.split("?")[1])
  if (!path.startsWith("/realtime?") || params.get("details") !== "false") throw new Error("小组件只允许轻量报价请求")
  const requestId = crypto.randomUUID()
  const abort = () => { void bridge.core.invoke("cancel_quote", { requestId }).catch(() => {}) }
  signal?.addEventListener("abort", abort, { once: true })
  try {
    const value = await bridge.core.invoke<T>("quotes", { requestId, codes: params.get("codes"), selected: params.get("selected") })
    if (signal?.aborted) throw signal.reason
    return value
  } catch (error) {
    if (signal?.aborted) throw signal.reason
    throw error instanceof Error ? error : new Error(String(error))
  } finally { signal?.removeEventListener("abort", abort) }
}

export function browserWidgetSettings(fallback: WatchSettings): WatchSettings {
  const query = new URLSearchParams(location.search)
  const codes = query.get("codes")?.split(",").filter((key) => /^(sh|sz|bj)\d{6}$/.test(key)).slice(0, 20)
  if (!codes?.length) return { ...fallback, interval: widgetInterval(fallback.interval) }
  const selected = query.get("selected") || ""
  const requested = Number(query.get("interval")) || 10
  return { codes: [...new Set(codes)], selected: codes.includes(selected) ? selected : codes[0], interval: [10, 15, 30, 60].find((value) => value >= requested) || 60 }
}
