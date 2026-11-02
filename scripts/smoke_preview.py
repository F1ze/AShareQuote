from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import sys
import time
from urllib.request import urlopen

ROOT = Path(__file__).resolve().parents[1]
ARTIFACTS = ROOT / "artifacts"
ARTIFACTS.mkdir(exist_ok=True)
PORT = 7187
BASE = f"http://127.0.0.1:{PORT}"
CHROME = Path(os.environ.get("ProgramFiles", r"C:\Program Files")) / "Google/Chrome/Application/chrome.exe"

server_log = (ARTIFACTS / "preview-server.log").open("w", encoding="utf-8")
server = subprocess.Popen(
    [str(ROOT / "bin/quotes.exe"), "--host", "127.0.0.1", "--port", str(PORT), "--output", str(ROOT / "downloads")],
    cwd=ROOT,
    stdout=server_log,
    stderr=subprocess.STDOUT,
)
try:
    for _ in range(50):
        if server.poll() is not None:
            raise RuntimeError("预览服务器启动失败，请查看 artifacts/preview-server.log")
        try:
            with urlopen(BASE + "/api/defaults", timeout=1) as response:
                defaults = json.load(response)
            break
        except Exception:
            time.sleep(0.1)
    else:
        raise RuntimeError("预览服务器未在限定时间内就绪")

    assert defaults["workers"] == 12
    assert defaults["count"] == 800
    with urlopen(BASE + "/", timeout=3) as response:
        html = response.read().decode("utf-8")
    assert "QFLUX" in html and "下载监视器" in html
    with urlopen(BASE + "/api/task", timeout=3) as response:
        assert json.load(response) is None

    for name, size in [("desktop", "1440,1000"), ("mobile", "390,844")]:
        screenshot = ARTIFACTS / f"{name}.png"
        result = subprocess.run(
            [
                str(CHROME), "--headless=new", "--disable-gpu", "--hide-scrollbars",
                "--no-first-run", "--no-default-browser-check", f"--window-size={size}",
                f"--screenshot={screenshot}", BASE + "/",
            ],
            cwd=ROOT,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            timeout=40,
        )
        if result.returncode != 0 or not screenshot.exists():
            raise RuntimeError(f"Chrome 截图失败：{result.stdout}")
        print(f"{name}: {screenshot.name} ({screenshot.stat().st_size} bytes)")
    print("HTTP defaults/task/index: OK")
finally:
    server.terminate()
    try:
        server.wait(timeout=5)
    except subprocess.TimeoutExpired:
        server.kill()
        server.wait(timeout=5)
    server_log.close()
    print(f"preview server stopped: {server.poll() is not None}")
