import { useCallback, useEffect, useRef, useState, type FormEvent } from "react"
import { ArrowLeft, File, Folder, FolderPlus, LoaderCircle, MapPin } from "lucide-react"
import { api } from "@/lib/api"
import type { BrowseResult } from "@/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle
} from "@/components/ui/sheet"

interface PathBrowserProps {
  kind: "folder" | "gbbq" | null
  initialPath: string
  onOpenChange: (open: boolean) => void
  onSelect: (path: string) => void
}

export function PathBrowser({ kind, initialPath, onOpenChange, onSelect }: PathBrowserProps) {
  const [result, setResult] = useState<BrowseResult | null>(null)
  const [pathInput, setPathInput] = useState("")
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")
  const [newFolder, setNewFolder] = useState("")
  const [creating, setCreating] = useState(false)
  const open = kind !== null
  const requestID = useRef(0)

  const load = useCallback(
    async (path: string) => {
      if (!kind) return
      const current = ++requestID.current
      setLoading(true)
      setError("")
      try {
        const data = await api.browse(path, kind)
        if (current !== requestID.current) return
        setResult(data)
        setPathInput(data.path)
      } catch (reason) {
        if (current === requestID.current)
          setError(reason instanceof Error ? reason.message : "无法读取目录")
      } finally {
        if (current === requestID.current) setLoading(false)
      }
    },
    [kind]
  )

  useEffect(() => {
    if (open) {
      setResult(null)
      setNewFolder("")
      void load(initialPath)
    }
  }, [open, initialPath, load])

  const choose = (path: string) => {
    onSelect(path)
    onOpenChange(false)
  }

  const jump = (event: FormEvent) => {
    event.preventDefault()
    void load(pathInput.trim())
  }

  const create = async (event: FormEvent) => {
    event.preventDefault()
    if (!result || !newFolder.trim()) return
    setCreating(true)
    setError("")
    try {
      const created = await api.createDirectory(result.path, newFolder.trim())
      setNewFolder("")
      await load(created.path)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "创建目录失败")
    } finally {
      setCreating(false)
    }
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        className="w-full gap-0 border-[#e5eaf2] bg-[#ffffff] p-0 sm:max-w-xl"
        aria-describedby="path-browser-description"
      >
        <SheetHeader className="border-b border-[#e5eaf2] px-6 py-5 text-left">
          <div className="font-mono text-[11px] tracking-[0.14em] text-[#4263eb]">本地文件</div>
          <SheetTitle className="text-xl font-semibold">
            {kind === "folder" ? "选择保存目录" : "查找复权因子 gbbq"}
          </SheetTitle>
          <SheetDescription id="path-browser-description" className="text-sm text-[#7c8ba5]">
            选择文件的保存位置，或新建一个文件夹。
          </SheetDescription>
        </SheetHeader>

        <div className="border-b border-[#e5eaf2] px-6 py-4">
          <div className="mb-3 flex flex-wrap gap-2">
            {result?.roots.map((root) => (
              <button
                key={root.path}
                type="button"
                onClick={() => void load(root.path)}
                className="min-h-9 cursor-pointer border border-[#e2e8f0] bg-transparent px-3 text-xs text-[#34343c] transition-colors hover:border-[#4263eb] hover:text-[#4263eb] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[#4263eb]"
              >
                {root.name}
              </button>
            ))}
          </div>
          <form onSubmit={jump} className="flex gap-2">
            <Input
              value={pathInput}
              onChange={(event) => setPathInput(event.target.value)}
              className="h-11 rounded-lg border-[#dfe5ef] bg-white font-mono text-xs shadow-none"
              aria-label="当前路径"
            />
            <Button
              type="submit"
              variant="outline"
              className="h-11 rounded-lg border-[#dfe5ef] bg-white shadow-none"
            >
              跳转
            </Button>
          </form>
        </div>

        <div className="flex min-h-0 flex-1 flex-col px-6 py-4">
          <div className="mb-3 flex items-center justify-between gap-3">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              disabled={!result?.parent || loading}
              onClick={() => result?.parent && void load(result.parent)}
              className="h-9 rounded-lg px-2 text-xs"
            >
              <ArrowLeft /> 上一级
            </Button>
            <span className="font-mono text-[11px] text-[#6a6a73]">
              {result ? `${result.entries.length} 项` : "读取中"}
            </span>
          </div>

          {error && (
            <div
              role="alert"
              className="mb-3 border border-[#c94141] bg-[#fff4f2] px-3 py-2 text-sm text-[#a42323]"
            >
              {error}
            </div>
          )}
          <div className="min-h-0 flex-1 overflow-y-auto border-y border-[#e5eaf2] bg-white">
            {loading && (
              <div className="flex h-32 items-center justify-center gap-2 text-sm text-[#7c8ba5]">
                <LoaderCircle className="animate-spin" /> 正在读取目录
              </div>
            )}
            {!loading && result?.entries.length === 0 && (
              <div className="flex h-32 items-center justify-center text-sm text-[#6a6a73]">
                这里没有可显示的项目
              </div>
            )}
            {!loading &&
              result?.entries.map((entry) => (
                <button
                  key={entry.path}
                  type="button"
                  onClick={() => (entry.directory ? void load(entry.path) : choose(entry.path))}
                  className="flex min-h-11 w-full cursor-pointer items-center gap-3 border-b border-[#ededf0] px-3 text-left text-sm transition-colors last:border-b-0 hover:bg-[#efefff] focus-visible:z-10 focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-[#4263eb]"
                >
                  {entry.directory ? (
                    <Folder className="size-4 text-[#4263eb]" />
                  ) : (
                    <File className="size-4 text-[#7c8ba5]" />
                  )}
                  <span className="min-w-0 flex-1 truncate">{entry.name}</span>
                  <span className="font-mono text-[10px] text-[#94a3b8]">
                    {entry.directory ? "文件夹" : "GBBQ"}
                  </span>
                </button>
              ))}
          </div>

          {kind === "folder" && (
            <form onSubmit={create} className="mt-4 flex gap-2">
              <Input
                value={newFolder}
                onChange={(event) => setNewFolder(event.target.value)}
                placeholder="新目录名称"
                className="h-10 rounded-lg border-[#e2e8f0] bg-white text-sm shadow-none"
              />
              <Button
                type="submit"
                variant="outline"
                disabled={!newFolder.trim() || creating || !result || loading || Boolean(error)}
                className="h-10 rounded-lg border-[#dfe5ef] bg-white shadow-none"
              >
                <FolderPlus /> 新建目录
              </Button>
            </form>
          )}
        </div>

        <SheetFooter className="border-t border-[#e5eaf2] px-6 py-4">
          {kind === "folder" ? (
            <Button
              type="button"
              disabled={!result || loading || creating || Boolean(error)}
              onClick={() => result && choose(result.path)}
              className="h-11 w-full rounded-lg bg-[#4263eb] text-white hover:bg-[#3455da]"
            >
              <MapPin /> 使用当前目录
            </Button>
          ) : (
            <p className="w-full text-center text-xs text-[#7c8ba5]">
              点击名为 gbbq 的文件即可完成选择
            </p>
          )}
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
