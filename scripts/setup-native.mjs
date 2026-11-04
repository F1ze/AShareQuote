import { createHash } from "node:crypto";
import { createReadStream, existsSync } from "node:fs";
import { mkdir, rename, rm } from "node:fs/promises";
import { resolve } from "node:path";
import { spawnSync } from "node:child_process";
import { nativeEnvironment } from "./native-env.mjs";

try {
  const env = nativeEnvironment();
  console.log(`原生编译器可用：${env.CC}`);
} catch (error) {
  if (process.platform !== "win32" || process.arch !== "x64") {
    console.error(`${error.message}\nLinux 请安装 build-essential；macOS 请安装 Xcode Command Line Tools。`);
    process.exit(1);
  }
  // 固定版本和 SHA-256；下载到项目目录，不需要管理员权限或修改系统设置。
  const name = "winlibs-x86_64-posix-seh-gcc-14.2.0-mingw-w64ucrt-12.0.0-r3.zip";
  const url = `https://github.com/brechtsanders/winlibs_mingw/releases/download/14.2.0posix-12.0.0-ucrt-r3/${name}`;
  const checksum = "88868d745b807f083a117ff69348d8bc021ad7389aa503379dbed1866efcaeb9";
  await mkdir(".tools", { recursive: true });
  const archive = resolve(".tools", name);
  const partial = archive + ".part";
  const run = (command, args) => {
    const result = spawnSync(command, args, { stdio: "inherit", windowsHide: true });
    if (result.error) throw result.error;
    if (result.status !== 0) throw new Error(`${command} 失败：${result.status}`);
  };
  try {
    if (!existsSync(archive)) {
      console.log("下载项目本地 GCC（约 236 MB）……");
      run("curl.exe", ["--fail", "--location", "--retry", "2", "--output", partial, url]);
      await rename(partial, archive);
    }
    const hash = createHash("sha256");
    for await (const chunk of createReadStream(archive)) hash.update(chunk);
    if (hash.digest("hex") !== checksum) {
      await rm(archive, { force: true });
      throw new Error("GCC 压缩包 SHA-256 不匹配，已删除，请重新执行 setup:native");
    }
    // 使用环境变量传路径，避免把目录名作为 PowerShell 代码拼接。
    const result = spawnSync("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command", "Expand-Archive -LiteralPath $env:QFLUX_NATIVE_ARCHIVE -DestinationPath $env:QFLUX_NATIVE_DEST -Force"], {
      stdio: "inherit", windowsHide: true,
      env: { ...process.env, QFLUX_NATIVE_ARCHIVE: archive, QFLUX_NATIVE_DEST: resolve(".tools") }
    });
    if (result.error) throw result.error;
    if (result.status !== 0) throw new Error("解压 GCC 失败");
    console.log(`原生编译器已就绪：${nativeEnvironment().CC}`);
  } finally {
    await rm(partial, { force: true });
  }
}
