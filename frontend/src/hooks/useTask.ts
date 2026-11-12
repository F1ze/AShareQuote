import { useCallback, useEffect, useRef, useState } from "react"
import { api } from "@/lib/api"
import type { TaskSnapshot } from "@/types"

export function isTaskActive(task: TaskSnapshot | null) {
  return Boolean(task && ["preparing", "running", "cancelling"].includes(task.state))
}

export function useTask() {
  const [task, setTaskState] = useState<TaskSnapshot | null>(null)
  const [connected, setConnected] = useState(false)

  const previousEvent = useRef<string | null>(null)
  const setTask = useCallback((snapshot: TaskSnapshot | null) => {
    // 用户操作可能先拿到 HTTP 响应。让下一帧 SSE 再与服务端对齐。
    previousEvent.current = null
    setTaskState(snapshot)
  }, [])

  useEffect(() => {
    const events = new EventSource(api.eventsURL)
    events.addEventListener("open", () => setConnected(true))
    events.addEventListener("task", (event) => {
      setConnected(true)
      const payload: string = (event as MessageEvent).data
      // SSE 每 400 ms 推完整快照。内容相同时跳过反序列化和任务状态更新。
      if (payload === previousEvent.current) return
      const snapshot = JSON.parse(payload) as TaskSnapshot | null
      previousEvent.current = payload
      setTaskState(snapshot)
    })
    events.addEventListener("error", () => setConnected(false))
    return () => events.close()
  }, [])

  return { task, setTask, connected }
}
