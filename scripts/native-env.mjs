import { existsSync } from "node:fs";
import { dirname, join, delimiter, resolve } from "node:path";
import { spawnSync } from "node:child_process";

// 只修改当前构建进程环境；不执行 go env -w，也不修改系统 PATH。
export function nativeEnvironment() {
  const env = { ...process.env, CGO_ENABLED: "1" };
  // go.mjs 已经选定 go.exe；继承其他安装的 GOROOT/GOTOOLDIR 会混用编译器和标准库版本。
  // 删除覆盖值后，Go 会从所选可执行文件的位置推导匹配的工具链，不修改用户系统环境。
  delete env.GOROOT;
  delete env.GOTOOLDIR;
  if (env.CC) return env;
  const local = resolve(".tools/mingw64/bin/gcc.exe");
  const candidates = process.platform === "win32"
    ? [local, "C:/msys64/ucrt64/bin/gcc.exe", "C:/msys64/mingw64/bin/gcc.exe", "gcc"]
    : ["cc", "gcc", "clang"];
  for (const compiler of candidates) {
    if (compiler !== "gcc" && compiler !== "cc" && compiler !== "clang" && !existsSync(compiler)) continue;
    const probe = spawnSync(compiler, ["--version"], { stdio: "ignore", windowsHide: true });
    if (probe.status !== 0) continue;
    env.CC = compiler;
    if (compiler.endsWith("gcc.exe")) {
      env.CXX = env.CXX || join(dirname(compiler), "g++.exe");
      env.PATH = dirname(compiler) + delimiter + (env.PATH || env.Path || "");
    }
    return env;
  }
  throw new Error("DuckDB 原生 Go 驱动需要 C/C++ 编译器。请先运行 npm run setup:native，或通过 CC/CXX 指定已安装的编译器。");
}
