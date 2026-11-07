from __future__ import annotations

import json
import os
from pathlib import Path
import shutil
import subprocess
import time
from urllib.parse import quote
from urllib.request import urlopen

ROOT = Path(__file__).resolve().parents[1]
ARTIFACTS = ROOT / "artifacts"
ARTIFACTS.mkdir(exist_ok=True)
RUN_DATA = ARTIFACTS / ("browser-data-" + str(time.time_ns()))
PORT = 7187
API_PORT = 7188
DEBUG_PORT = 9223
BASE = f"http://127.0.0.1:{PORT}"
CHROME = Path(os.environ.get("ProgramFiles", r"C:\Program Files")) / "Google/Chrome/Application/chrome.exe"

server_log = (ARTIFACTS / "preview-server.log").open("w", encoding="utf-8")
chrome_log = (ARTIFACTS / "chrome.log").open("w", encoding="utf-8")
server = subprocess.Popen(
    [
        str(ROOT / "bin/quotes.exe"),
        "--host", "127.0.0.1",
        "--port", str(PORT),
        "--api-port", str(API_PORT),
        "--output", str(ROOT / "downloads"),
        "--data-dir", str(RUN_DATA),
    ],
    cwd=ROOT,
    stdout=server_log,
    stderr=subprocess.STDOUT,
)
chrome = None


def read_json(path: str):
    with urlopen(BASE + path, timeout=3) as response:
        return json.load(response)


def stop_process(process: subprocess.Popen | None):
    if process is None or process.poll() is not None:
        return
    if os.name == "nt":
        subprocess.run(
            ["taskkill", "/PID", str(process.pid), "/T", "/F"],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            timeout=10,
        )
    else:
        process.terminate()
    try:
        process.wait(timeout=8)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)


try:
    for _ in range(100):
        if server.poll() is not None:
            raise RuntimeError("服务启动失败，请查看 artifacts/preview-server.log")
        try:
            with urlopen(BASE + "/api/health", timeout=1) as response:
                assert json.load(response)["status"] == "ok"
            with urlopen(BASE + "/", timeout=1) as response:
                html = response.read().decode("utf-8")
            if "QFLUX" in html and "/src/main.tsx" in html:
                break
        except Exception:
            time.sleep(0.15)
    else:
        raise RuntimeError("前后端未在限定时间内就绪")

    defaults = read_json("/api/defaults")
    assert defaults["workers"] == 12
    assert defaults["requestRate"] == 40
    assert defaults["hostRequestRate"] == 8
    assert defaults["count"] == 800
    assert defaults["markets"] == [0, 1, 2]
    assert read_json("/api/task") is None
    browse = read_json("/api/fs/browse?kind=folder&path=" + quote(str(ROOT)))
    assert browse["path"] == str(ROOT)
    assert any(entry["name"] == "frontend" for entry in browse["entries"])

    profile = ARTIFACTS / "chrome-profile"
    profile = profile.resolve()
    profile.relative_to(ARTIFACTS.resolve())
    shutil.rmtree(profile, ignore_errors=True)
    chrome = subprocess.Popen(
        [
            str(CHROME), "--headless=new", "--disable-gpu", "--no-first-run",
            "--no-default-browser-check", "--disable-background-networking",
            f"--remote-debugging-port={DEBUG_PORT}", "--remote-allow-origins=*",
            f"--user-data-dir={profile}", "about:blank",
        ],
        cwd=ROOT,
        stdout=chrome_log,
        stderr=subprocess.STDOUT,
    )
    node = shutil.which("node") or "node"
    result = subprocess.run(
        f'"{node}" scripts/browser_smoke.mjs "{BASE}" "{ARTIFACTS}" {DEBUG_PORT}',
        cwd=ROOT,
        timeout=240,
        shell=True,
    )
    if result.returncode != 0:
        raise RuntimeError("浏览器交互检查失败，请查看 artifacts/browser-smoke.json 和 chrome.log")
    print("HTTP health/defaults/task/fs + React browser interactions: OK")
finally:
    stop_process(chrome)
    stop_process(server)
    chrome_log.close()
    server_log.close()
    print(f"chrome stopped: {chrome is None or chrome.poll() is not None}")
    print(f"preview server stopped: {server.poll() is not None}")
