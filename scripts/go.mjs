import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import { join } from "node:path";

const candidates = [process.env.GO_BINARY];
if (process.platform === "win32") {
  candidates.push(join(process.env.ProgramFiles || "C:\\Program Files", "Go", "bin", "go.exe"));
}
candidates.push("go");
const go = candidates.find((candidate) => candidate && (candidate === "go" || existsSync(candidate)));
if (!go) {
  console.error("未找到 Go。请安装 Go 1.23+，或设置 GO_BINARY 为 go.exe 的完整路径。");
  process.exit(1);
}

const [command, ...args] = process.argv.slice(2);
if (!command) {
  console.error("用法：node scripts/go.mjs <run|build|test|...> [参数]");
  process.exit(1);
}
const goArgs = command === "run" ? ["run", "./cmd/qflux", ...args] : [command, ...args];
const child = spawn(go, goArgs, { stdio: "inherit", windowsHide: true });
const forward = (signal) => {
  if (!child.killed) child.kill(signal);
};
process.on("SIGINT", () => forward("SIGINT"));
process.on("SIGTERM", () => forward("SIGTERM"));
child.on("error", (error) => {
  console.error(`无法启动 Go：${error.message}`);
  process.exit(1);
});
child.on("exit", (code, signal) => {
  process.exit(code ?? (signal ? 1 : 0));
});
