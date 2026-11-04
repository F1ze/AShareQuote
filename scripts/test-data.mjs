import { spawnSync } from "node:child_process"
import path from "node:path"
const native = spawnSync(process.execPath, ["scripts/go.mjs", "test", "./internal/repository", "-run", "NativeWarehouse"], {
  stdio: "inherit", windowsHide: true
})
if (native.error) throw native.error
if (native.status !== 0) process.exit(native.status ?? 1)

const python =
  process.env.QFLUX_PYTHON ||
  path.resolve(".venv", process.platform === "win32" ? "Scripts/python.exe" : "bin/python")
const result = spawnSync(python, ["-m", "unittest", "discover", "-s", "tests", "-p", "test_*.py"], {
  stdio: "inherit",
  windowsHide: true
})
if (result.error) {
  console.error(`${result.error.message}；请先运行 npm run setup:data`)
  process.exit(1)
}
process.exit(result.status ?? 1)
