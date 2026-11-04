import { useEffect, useState } from "react"
import { api } from "@/lib/api"
import type { TaskSnapshot } from "@/types"

export function isTaskActive(task: TaskSnapshot | null) {
  return Boolean(task && ["preparing", "running", "cancelling"].includes(task.state))
}

export function useTask() {
  const [task, setTask] = useState<TaskSnapshot | null>(null)
  const [connected, setConnected] = useState(false)

  useEffect(() => {
    const events = new EventSource(api.eventsURL)
    events.addEventListener("open", () => setConnected(true))
    events.addEventListener("task", (event) => {
      setConnected(true)
      setTask(JSON.parse((event as MessageEvent).data) as TaskSnapshot)
    })
    events.addEventListener("error", () => setConnected(false))
    return () => events.close()
  }, [])

  return { task, setTask, connected }
}
