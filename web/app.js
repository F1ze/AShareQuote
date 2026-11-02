"use strict";

const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => Array.from(document.querySelectorAll(selector));
const number = new Intl.NumberFormat("zh-CN");
const stateNames = {
  idle: "待命", preparing: "准备中", running: "下载中", cancelling: "停止中",
  completed: "已完成", completed_with_errors: "部分完成", cancelled: "已取消", failed: "失败",
};
let scope = "markets";
let currentTask = null;
let lastLogKey = "";
let lastFileKey = "";
let speedHistory = [];
let lastSpeedTask = "";
let lastSpeedAt = 0;
let toastTimer;
let eventSource;
let fallbackTimer;

function showToast(message) {
  const toast = $("#toast");
  toast.textContent = message;
  toast.classList.add("visible");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => toast.classList.remove("visible"), 2800);
}

function setFormMessage(message, type = "") {
  const element = $("#form-message");
  element.textContent = message;
  element.className = `form-message ${type}`;
}

function setScope(next) {
  scope = next;
  $$("[data-scope]").forEach((button) => {
    const active = button.dataset.scope === next;
    button.classList.toggle("active", active);
    button.setAttribute("aria-pressed", String(active));
  });
  $("#market-options").hidden = next !== "markets";
  $("#code-options").hidden = next !== "codes";
}

function collectOptions() {
  const markets = $$('input[name="market"]:checked').map((input) => Number(input.value));
  return {
    markets: scope === "markets" ? markets : [],
    codes: scope === "codes" ? $("#codes").value.trim() : "",
    category: Number($("#category").value),
    start: Number($("#start").value),
    count: Number($("#count").value),
    workers: Number($("#workers").value),
    timeout: Number($("#timeout").value),
    outputDir: $("#output-dir").value.trim(),
    gbbqPath: $("#gbbq-path").value.trim(),
  };
}

function validate(options) {
  if (scope === "markets" && options.markets.length === 0) return "请至少选择一个市场";
  if (scope === "codes" && !options.codes) return "请填写至少一个证券代码";
  if (!options.outputDir) return "请填写保存目录";
  if (options.count < 1 || options.count > 800) return "每只条数须为 1–800";
  if (options.workers < 1 || options.workers > 64) return "并发 worker 须为 1–64";
  if (options.timeout < 2 || options.timeout > 60) return "超时须为 2–60 秒";
  if (options.start < 0) return "跳过的 K 线数量不能小于 0";
  return "";
}

async function api(url, options = {}) {
  const response = await fetch(url, {
    ...options,
    headers: options.body ? { "Content-Type": "application/json" } : undefined,
  });
  const data = await response.json();
  if (!response.ok) throw new Error(data.error || `请求失败（${response.status}）`);
  return data;
}

function isActive(task) {
  return task && ["preparing", "running", "cancelling"].includes(task.state);
}

function renderTask(task) {
  currentTask = task;
  const active = isActive(task);
  $("#form-fields").disabled = active;
  $("#start-task").disabled = active;
  $("#test-hosts").disabled = active;
  $("#cancel-task").disabled = !active || task.state === "cancelling";

  if (!task) {
    $("#task-state").dataset.state = "idle";
    $("#task-state span").textContent = stateNames.idle;
    return;
  }
  const percent = Math.max(0, Math.min(100, task.percent || 0));
  $("#task-state").dataset.state = task.state;
  $("#task-state span").textContent = stateNames[task.state] || task.state;
  $("#task-message").textContent = task.message || "正在运行";
  $("#task-id").textContent = task.id ? `TASK ${task.id}` : "";
  $("#progress-percent").textContent = task.total ? `${percent.toFixed(1)}%` : "—";
  $("#progress-fill").style.transform = `scaleX(${percent / 100})`;
  $(".progress-track").setAttribute("aria-valuenow", percent.toFixed(1));
  $("#progress-count").textContent = `${number.format(task.done || 0)} / ${number.format(task.total || 0)} SECURITIES`;
  $("#progress-bars").textContent = `${number.format(task.bars || 0)} BARS`;
  $("#stat-speed").textContent = (task.speed || 0).toFixed(1);
  $("#stat-success").textContent = number.format(task.succeeded || 0);
  $("#stat-failed").textContent = number.format(task.failed || 0);
  $("#stat-failed").classList.toggle("has-errors", (task.failed || 0) > 0);
  $("#stat-elapsed").textContent = formatDuration(task.elapsed || 0);
  $("#output-status").textContent = `OUTPUT: ${task.outputDir || "downloads"}`;
  $("#output-status").title = task.outputDir || "";
  renderFiles(task.recent || []);
  renderLogs(task.logs || []);
  recordSpeed(task);
}

function formatDuration(seconds) {
  const total = Math.max(0, Math.floor(seconds));
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const secs = total % 60;
  if (hours > 0) return `${String(hours).padStart(2, "0")}:${String(minutes).padStart(2, "0")}:${String(secs).padStart(2, "0")}`;
  return `${String(minutes).padStart(2, "0")}:${String(secs).padStart(2, "0")}`;
}

function renderFiles(files) {
  const key = files.map((file) => `${file.name}:${file.createdAt}`).join("|");
  if (key === lastFileKey) return;
  lastFileKey = key;
  $("#file-count").textContent = `${files.length} FILES`;
  $("#files-empty").hidden = files.length > 0;
  const tbody = $("#recent-files");
  tbody.replaceChildren();
  files.forEach((file) => {
    const row = document.createElement("tr");
    const values = [file.name, `${file.market} / ${file.code}`, number.format(file.bars), formatBytes(file.size), `${file.durationMs} ms`];
    values.forEach((value, index) => {
      const cell = document.createElement("td");
      if (index === 0) {
        cell.className = "file-name";
        const button = document.createElement("button");
        button.type = "button";
        button.className = "file-preview";
        button.textContent = value;
        button.title = "预览不复权、前复权和后复权行情";
        button.addEventListener("click", () => openPreview(file.name));
        cell.appendChild(button);
      } else {
        cell.textContent = value;
      }
      row.appendChild(cell);
    });
    tbody.appendChild(row);
  });
}

function formatBytes(bytes) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

function renderLogs(logs) {
  const last = logs[logs.length - 1];
  const key = `${logs.length}:${last ? last.time + last.message : ""}`;
  if (key === lastLogKey) return;
  lastLogKey = key;
  const container = $("#logs");
  const stickToBottom = container.scrollHeight - container.scrollTop - container.clientHeight < 60;
  container.replaceChildren();
  if (!logs.length) {
    const empty = document.createElement("div");
    empty.className = "logs-empty";
    empty.innerHTML = '<span class="mono">READY_</span><p>等待任务事件。线路、码表与写入状态会显示在这里。</p>';
    container.appendChild(empty);
    return;
  }
  const fragment = document.createDocumentFragment();
  logs.forEach((entry) => {
    const row = document.createElement("div");
    row.className = "log-entry";
    row.dataset.level = entry.level;
    const time = document.createElement("span");
    time.className = "log-time";
    time.textContent = entry.time;
    const message = document.createElement("span");
    message.className = "log-message";
    message.textContent = entry.message;
    row.append(time, message);
    fragment.appendChild(row);
  });
  container.appendChild(fragment);
  if (stickToBottom) container.scrollTop = container.scrollHeight;
}

function recordSpeed(task) {
  if (task.id !== lastSpeedTask) {
    lastSpeedTask = task.id;
    speedHistory = [];
    lastSpeedAt = 0;
  }
  const now = Date.now();
  if (now - lastSpeedAt < 900 || !isActive(task)) return;
  lastSpeedAt = now;
  speedHistory.push(task.speed || 0);
  if (speedHistory.length > 48) speedHistory.shift();
  drawChart();
}

function drawChart() {
  const hasData = speedHistory.length > 1;
  $("#chart-empty").hidden = hasData;
  if (!hasData) return;
  const width = 720;
  const baseline = 112;
  const top = 8;
  const max = Math.max(...speedHistory, 1);
  const points = speedHistory.map((value, index) => {
    const x = speedHistory.length === 1 ? 0 : (index / (speedHistory.length - 1)) * width;
    const y = baseline - (value / max) * (baseline - top);
    return [x, y];
  });
  const line = points.map(([x, y], index) => `${index ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)}`).join(" ");
  $("#chart-line").setAttribute("d", line);
  $("#chart-area").setAttribute("d", `${line} L${points[points.length - 1][0]},${baseline} L${points[0][0]},${baseline} Z`);
  const last = points[points.length - 1];
  $("#chart-dot").setAttribute("cx", last[0]);
  $("#chart-dot").setAttribute("cy", last[1]);
}

function connectEvents() {
  clearTimeout(fallbackTimer);
  if (eventSource) eventSource.close();
  eventSource = new EventSource("/api/events");
  eventSource.addEventListener("open", () => {
    $("#connection-label").textContent = "本地服务已连接";
  });
  eventSource.addEventListener("task", (event) => {
    renderTask(JSON.parse(event.data));
  });
  eventSource.addEventListener("error", () => {
    $("#connection-label").textContent = "正在重新连接";
  });
}

async function loadDefaults() {
  try {
    const defaults = await api("/api/defaults");
    $("#gbbq-path").value = defaults.gbbqPath || "";
    $("#output-dir").value = defaults.outputDir;
    $("#category").value = String(defaults.category);
    $("#count").value = defaults.count;
    $("#workers").value = defaults.workers;
    $("#timeout").value = defaults.timeout;
    $("#output-status").textContent = `OUTPUT: ${defaults.outputDir}`;
    renderTask(defaults.task);
    if (defaults.task) applyTaskOptions(defaults.task.options);
  } catch (error) {
    setFormMessage(error.message, "error");
    $("#connection-label").textContent = "服务连接失败";
  }
}

function applyTaskOptions(options) {
  if (!options) return;
  $("#gbbq-path").value = options.gbbqPath || "";
  $("#output-dir").value = options.outputDir || "";
  $("#category").value = String(options.category);
  $("#start").value = options.start;
  $("#count").value = options.count;
  $("#workers").value = options.workers;
  $("#timeout").value = options.timeout;
  if (options.codes) {
    setScope("codes");
    $("#codes").value = options.codes;
  } else {
    setScope("markets");
    $$('input[name="market"]').forEach((input) => { input.checked = (options.markets || []).includes(Number(input.value)); });
  }
}

$$("[data-scope]").forEach((button) => button.addEventListener("click", () => setScope(button.dataset.scope)));

$("#download-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const options = collectOptions();
  const validationError = validate(options);
  if (validationError) {
    setFormMessage(validationError, "error");
    return;
  }
  $("#start-task").disabled = true;
  $("#test-hosts").disabled = true;
  setFormMessage("正在创建任务…");
  try {
    const task = await api("/api/task", { method: "POST", body: JSON.stringify(options) });
    speedHistory = [];
    lastFileKey = "";
    lastLogKey = "";
    renderTask(task);
    setFormMessage("任务已启动，可安全关闭此页面；任务会继续运行。", "success");
  } catch (error) {
    setFormMessage(error.message, "error");
    $("#start-task").disabled = false;
    $("#test-hosts").disabled = false;
  }
});

$("#cancel-task").addEventListener("click", async () => {
  if (!currentTask || !isActive(currentTask)) return;
  if (!window.confirm("确定停止当前下载？已完成的 CSV 文件会保留。")) return;
  $("#cancel-task").disabled = true;
  try {
    renderTask(await api("/api/task", { method: "DELETE" }));
  } catch (error) {
    showToast(error.message);
  }
});

$("#test-hosts").addEventListener("click", async () => {
  const button = $("#test-hosts");
  button.disabled = true;
  button.textContent = "业务自检中…";
  setFormMessage("正在生成配置，并用码表与 600000 逐笔数据检查内置线路");
  try {
    const options = collectOptions();
    const result = await api("/api/hosts/test", {
      method: "POST",
      body: JSON.stringify({ timeout: options.timeout }),
    });
    const working = result.results.filter((item) => item.ok).sort((a, b) => a.latencyMs - b.latencyMs);
    const fastest = working.length ? `，最快 ${working[0].latencyMs} ms` : "";
    setFormMessage(`业务自检通过 ${result.available} / ${result.tested} 条线路${fastest}`, working.length ? "success" : "error");
  } catch (error) {
    setFormMessage(error.message, "error");
  } finally {
    button.disabled = false;
    button.textContent = "检测内置线路";
  }
});

async function openPathPicker(kind) {
  const configs = {
    folder: { input: "#output-dir", waiting: "等待选择保存目录…", success: "已选择保存目录" },
    gbbq: { input: "#gbbq-path", waiting: "等待选择复权因子 gbbq…", success: "已选择复权因子文件" },
  };
  const config = configs[kind];
  const buttons = [$("#pick-output"), $("#pick-gbbq")];
  buttons.forEach((button) => { button.disabled = true; });
  $("#start-task").disabled = true;
  $("#test-hosts").disabled = true;
  setFormMessage(config.waiting);
  try {
    const result = await api(`/api/dialog/${kind}`, { method: "POST" });
    if (result.path) {
      $(config.input).value = result.path;
      if (kind === "folder") {
        $("#output-status").textContent = `OUTPUT: ${result.path}`;
        $("#output-status").title = result.path;
      }
      setFormMessage(config.success, "success");
    } else {
      setFormMessage("已取消选择");
    }
  } catch (error) {
    setFormMessage(error.message, "error");
  } finally {
    const active = isActive(currentTask);
    buttons.forEach((button) => { button.disabled = active; });
    $("#start-task").disabled = active;
    $("#test-hosts").disabled = active;
  }
}

$("#pick-output").addEventListener("click", () => openPathPicker("folder"));
$("#pick-gbbq").addEventListener("click", () => openPathPicker("gbbq"));

let previewFile = "";
let previewMode = "raw";
let previewRequest = 0;

function setPreviewMode(mode) {
  previewMode = mode;
  $$('[data-adjust]').forEach((button) => {
    const active = button.dataset.adjust === mode;
    button.classList.toggle("active", active);
    button.setAttribute("aria-pressed", String(active));
  });
}

function openPreview(file) {
  previewFile = file;
  setPreviewMode("raw");
  $("#preview-title").textContent = file;
  const dialog = $("#price-preview");
  if (!dialog.open) dialog.showModal();
  loadPreview();
}

async function loadPreview() {
  const requestId = ++previewRequest;
  $("#preview-loading").hidden = false;
  $("#preview-loading").textContent = "正在实时计算行情…";
  $("#preview-error").hidden = true;
  $("#preview-rows").replaceChildren();
  clearPreviewChart();
  const query = new URLSearchParams({ file: previewFile, mode: previewMode, limit: "200" });
  try {
    const result = await api(`/api/preview?${query}`);
    if (requestId !== previewRequest) return;
    $("#preview-loading").hidden = true;
    $("#preview-factor").textContent = result.factorAvailable ? `${result.factorEvents || "READY"} EVENTS / READ ONLY` : "NO FACTOR FILE";
    renderPreviewChart(result.bars || []);
    renderPreviewRows(result.bars || []);
    if (!result.bars || !result.bars.length) {
      $("#preview-loading").hidden = false;
      $("#preview-loading").textContent = "文件中没有 K 线数据";
    }
  } catch (error) {
    if (requestId !== previewRequest) return;
    $("#preview-loading").hidden = true;
    $("#preview-factor").textContent = "PREVIEW ERROR";
    $("#preview-error").textContent = error.message;
    $("#preview-error").hidden = false;
  }
}

function clearPreviewChart() {
  $("#preview-chart-line").setAttribute("d", "");
  $("#preview-chart-area").setAttribute("d", "");
}

function renderPreviewChart(bars) {
  clearPreviewChart();
  if (!bars.length) return;
  const width = 800;
  const baseline = 190;
  const top = 10;
  const closes = bars.map((bar) => bar.close);
  const min = Math.min(...closes);
  const max = Math.max(...closes);
  const range = max - min || 1;
  const points = bars.map((bar, index) => {
    const x = bars.length === 1 ? 0 : index / (bars.length - 1) * width;
    const y = baseline - (bar.close - min) / range * (baseline - top);
    return [x, y];
  });
  const line = points.map(([x, y], index) => `${index ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)}`).join(" ");
  $("#preview-chart-line").setAttribute("d", line);
  $("#preview-chart-area").setAttribute("d", `${line} L${points[points.length - 1][0]},${baseline} L${points[0][0]},${baseline} Z`);
}

function renderPreviewRows(bars) {
  const tbody = $("#preview-rows");
  const fragment = document.createDocumentFragment();
  bars.slice(-15).reverse().forEach((bar) => {
    const row = document.createElement("tr");
    const values = [bar.date, formatPrice(bar.open), formatPrice(bar.high), formatPrice(bar.low), formatPrice(bar.close), number.format(Math.round(bar.volume))];
    values.forEach((value) => {
      const cell = document.createElement("td");
      cell.textContent = value;
      row.appendChild(cell);
    });
    fragment.appendChild(row);
  });
  tbody.appendChild(fragment);
}

function formatPrice(value) {
  return Number.isFinite(value) ? value.toFixed(3) : "—";
}

$$('[data-adjust]').forEach((button) => button.addEventListener("click", () => {
  if (button.dataset.adjust !== previewMode) {
    setPreviewMode(button.dataset.adjust);
    loadPreview();
  }
}));

$("#close-preview").addEventListener("click", () => {
  previewRequest++;
  $("#price-preview").close();
});
$("#price-preview").addEventListener("click", (event) => {
  if (event.target === event.currentTarget) {
    previewRequest++;
    event.currentTarget.close();
  }
});

function updateClock() {
  $("#clock").textContent = new Date().toLocaleTimeString("zh-CN", { hour12: false });
}

updateClock();
setInterval(updateClock, 1000);
loadDefaults();
connectEvents();
window.addEventListener("beforeunload", () => { if (eventSource) eventSource.close(); });
