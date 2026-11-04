import { spawnSync } from "node:child_process"
import { existsSync } from "node:fs"
import path from "node:path"
const python = process.env.QFLUX_BOOTSTRAP_PYTHON || "python"
const executable = path.resolve(
  ".venv",
  process.platform === "win32" ? "Scripts/python.exe" : "bin/python"
)
function run(command, args) {
  const result = spawnSync(command, args, { stdio: "inherit", windowsHide: true })
  if (result.error) throw result.error
  if (result.status !== 0) process.exit(result.status || 1)
}
if (!existsSync(executable)) run(python, ["-m", "venv", ".venv"])
run(executable, ["-m", "pip", "install", "-r", "requirements-data.txt"])
run(executable, ["-c", "import baostock; print('BaoStock SDK ready')"])
