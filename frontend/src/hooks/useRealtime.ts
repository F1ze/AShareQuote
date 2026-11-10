import { useEffect, useState } from "react"
import { carryRealtimeQuotes, retainRealtimeNames } from "@/lib/realtime"
import { api } from "@/lib/api"
import type { RealtimeSnapshot } from "@/types"

// 用单个自调度 timeout，上一轮结束才排下一轮；切换证券/离开 Tab 时取消网络请求。
// 页面隐藏时暂停，恢复可见立即刷新；错误退避至最多 30 秒，不重叠堆积请求。
export function useRealtime(codes: string[], selected: string, interval: number, paused: boolean, revision: number, details = true) {
  const [state, setState] = useState<{ key: string; data: RealtimeSnapshot | null; names: Record<string, string>; loading: boolean; error: string }>({ key: "", data: null, names: {}, loading: false, error: "" })
  const key = codes.join(",") + "/" + selected
  useEffect(() => {
    let disposed = false
    let timer: ReturnType<typeof setTimeout> | undefined
    let controller: AbortController | undefined
    let running = false
    let failures = 0
    const poll = async () => {
      if (disposed || document.hidden || paused || running) return
      running = true
      const requestController = new AbortController()
      controller = requestController
      const signal = AbortSignal.any([requestController.signal, AbortSignal.timeout(10000)])
      setState((old) => ({ key, names: old.names, data: old.key === key ? old.data : carryRealtimeQuotes(old.data, codes, selected), loading: true, error: old.key === key ? old.error : "" }))
      try {
        const data = await api.realtime(codes, selected, signal, details)
        if (!disposed && !signal.aborted) { failures = 0; setState((old) => ({ key, ...retainRealtimeNames(old.names, data, codes), loading: false, error: "" })) }
      } catch (error) {
        if (!disposed && !requestController.signal.aborted) {
          failures++
          setState((old) => ({ ...old, key, loading: false, error: error instanceof Error ? error.message : "行情请求失败" }))
        }
      } finally {
        running = false
        if (!disposed && !document.hidden && !paused) timer = setTimeout(() => void poll(), Math.min(30, interval * 2 ** Math.min(failures, 4)) * 1000)
      }
    }
    const visibility = () => {
      clearTimeout(timer)
      if (document.hidden) controller?.abort()
      else if (!running) void poll()
    }
    const leaving = () => { disposed = true; clearTimeout(timer); controller?.abort() }
    document.addEventListener("visibilitychange", visibility)
    window.addEventListener("pagehide", leaving)
    void poll()
    return () => { disposed = true; clearTimeout(timer); controller?.abort(); document.removeEventListener("visibilitychange", visibility); window.removeEventListener("pagehide", leaving) }
  }, [key, codes, selected, interval, paused, revision, details])
  return state.key === key ? { ...state, loading: paused ? false : state.loading } : { key, names: state.names, data: carryRealtimeQuotes(state.data, codes, selected), loading: !paused, error: "" }
}
