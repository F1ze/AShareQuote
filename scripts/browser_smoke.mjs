import fs from "node:fs"
import path from "node:path"

const [base, artifacts, debugPort = "9223"] = process.argv.slice(2)
if (!base || !artifacts)
  throw new Error("用法：node scripts/browser_smoke.mjs <base-url> <artifacts-dir> [debug-port]")
const root = path.resolve(artifacts, "..")
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

async function target() {
  for (let attempt = 0; attempt < 100; attempt++) {
    try {
      const pages = await fetch(`http://127.0.0.1:${debugPort}/json/list`).then((response) =>
        response.json()
      )
      const page = pages.find((item) => item.type === "page")
      if (page) return page
    } catch {}
    await sleep(100)
  }
  throw new Error("Chrome DevTools 未在限定时间内就绪")
}

const page = await target()
const socket = new WebSocket(page.webSocketDebuggerUrl)
await new Promise((resolve, reject) => {
  socket.addEventListener("open", resolve, { once: true })
  socket.addEventListener("error", reject, { once: true })
})

let sequence = 0
const pending = new Map()
const consoleErrors = []
const httpErrors = []
const requestSizes = new Map()

socket.addEventListener("message", (event) => {
  const message = JSON.parse(event.data)
  if (message.id) {
    const item = pending.get(message.id)
    if (!item) return
    pending.delete(message.id)
    clearTimeout(item.timer)
    if (message.error) item.reject(new Error(`${item.method}: ${message.error.message}`))
    else item.resolve(message.result)
    return
  }
  if (message.method === "Runtime.exceptionThrown") {
    consoleErrors.push(
      message.params.exceptionDetails.exception?.description || message.params.exceptionDetails.text
    )
  }
  if (message.method === "Runtime.consoleAPICalled" && message.params.type === "error") {
    consoleErrors.push(
      message.params.args.map((item) => item.value || item.description || "").join(" ")
    )
  }
  if (message.method === "Log.entryAdded" && message.params.entry.level === "error") {
    consoleErrors.push(message.params.entry.text)
  }
  if (message.method === "Network.responseReceived" && message.params.response.status >= 400) {
    httpErrors.push(`${message.params.response.status} ${message.params.response.url}`)
  }
  if (message.method === "Network.loadingFinished") {
    requestSizes.set(message.params.requestId, message.params.encodedDataLength || 0)
  }
})

function send(method, params = {}) {
  const id = ++sequence
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      pending.delete(id)
      reject(new Error(`${method} 执行超时`))
    }, 15000)
    pending.set(id, { resolve, reject, timer, method })
    socket.send(JSON.stringify({ id, method, params }))
  })
}

async function evaluate(expression) {
  const response = await send("Runtime.evaluate", {
    expression,
    returnByValue: true,
    awaitPromise: true
  })
  if (response.exceptionDetails)
    throw new Error(
      response.exceptionDetails.exception?.description || response.exceptionDetails.text
    )
  return response.result.value
}

async function waitFor(expression, label, timeout = 15000) {
  const started = Date.now()
  while (Date.now() - started < timeout) {
    if (await evaluate(`Boolean(${expression})`)) return
    await sleep(120)
  }
  throw new Error(`等待超时：${label}`)
}

async function clickText(text) {
  const clicked = await evaluate(`(() => {
    const element = [...document.querySelectorAll('button')].find((item) => item.textContent.trim() === ${JSON.stringify(text)} && !item.disabled)
    if (!element) return false
    element.scrollIntoView({ block: "nearest" })
    element.click()
    return true
  })()`)
  if (!clicked) throw new Error(`找不到可点击按钮：${text}`)
}

async function screenshot(name) {
  const options = { format: "png", captureBeyondViewport: false }
  if (name === "desktop.png") {
    const metrics = await send("Page.getLayoutMetrics")
    options.captureBeyondViewport = true
    options.clip = {
      x: 0,
      y: 0,
      width: metrics.cssContentSize.width,
      height: metrics.cssContentSize.height,
      scale: 1
    }
  }
  const capture = await send("Page.captureScreenshot", options)
  fs.writeFileSync(path.join(artifacts, name), Buffer.from(capture.data, "base64"))
}

try {
  await Promise.all([
    send("Page.enable"),
    send("Runtime.enable"),
    send("Network.enable"),
    send("Log.enable")
  ])
  await send("Emulation.setDeviceMetricsOverride", {
    width: 1440,
    height: 1000,
    deviceScaleFactor: 1,
    mobile: false
  })
  await send("Page.navigate", { url: `${base}/` })
  await waitFor(`document.body.innerText.includes('已就绪，下载前')`, "React 与 Echo 默认配置")
  await waitFor(`document.body.innerText.includes('下载概览')`, "任务监视器渲染")
  await waitFor(
    `document.querySelector('.connection-status').textContent.includes('服务已连接')`,
    "状态连接"
  )
  await waitFor(
    `document.querySelector('.connection-status')?.dataset.connected === 'true'`,
    "服务在线状态颜色"
  )
  const appearance = await evaluate(`({
    selectedMarkets: [...document.querySelectorAll('.market-option')].map((e) => ({ selected: e.querySelector('input').checked, color: getComputedStyle(e).backgroundColor })),
    serviceColor: getComputedStyle(document.querySelector('.connection-status')).color,
    startButton: document.querySelector('.primary-button').getBoundingClientRect().toJSON()
  })`)
  if (appearance.selectedMarkets.filter((item) => item.selected).length !== 3)
    throw new Error(`默认市场未正确选中：${JSON.stringify(appearance)}`)
  const theme = await evaluate(`(() => {
    const token = (name) => {
      const probe = document.createElement('span')
      probe.style.color = 'var(' + name + ')'
      document.body.append(probe)
      const value = getComputedStyle(probe).color
      probe.remove()
      return value
    }
    const dark = (value) => {
      const rgb = value.match(/[0-9.]+/g)?.slice(0,3).map(Number) || []
      return rgb.length === 3 && Math.max(...rgb) < 80
    }
    return {
      dark: document.documentElement.classList.contains('dark') && getComputedStyle(document.documentElement).colorScheme === 'dark',
      canvas: dark(getComputedStyle(document.querySelector('.app-shell')).backgroundColor),
      panel: dark(getComputedStyle(document.querySelector('.setup-panel')).backgroundColor),
      selected: token('--surface-selected'),
      online: token('--positive')
    }
  })()`)
  if (!theme.dark || !theme.canvas || !theme.panel) throw new Error('页面未完整应用暗色主题：' + JSON.stringify(theme))
  if (appearance.selectedMarkets.some((item) => item.color !== theme.selected))
    throw new Error(`选中市场缺少视觉反馈：${JSON.stringify(appearance)}`)
  if (appearance.serviceColor !== theme.online) throw new Error('服务状态缺少清晰颜色反馈')
  console.log(JSON.stringify({ appearance }))
  await screenshot("desktop.png")
  const controls =
    await evaluate(`['#category', '#start-date', '#end-date', '#output-dir'].map((selector) => {
    const box = document.querySelector(selector).getBoundingClientRect()
    return { selector, height: box.height }
  })`)
  if (controls.some((control) => control.height !== 44))
    throw new Error(`表单控件高度不一致：${JSON.stringify(controls)}`)
  await clickText("近 3 年")
  const range = await evaluate(
    `({from: document.querySelector('#start-date').value, to: document.querySelector('#end-date').value})`
  )
  if (!range.from || !range.to || range.from >= range.to) throw new Error("日期快捷选择未生效")
  await clickText("近 1 年")
  await evaluate(`document.querySelector('.market-option input').click()`)
  if ((await evaluate(`document.querySelectorAll('.market-option.is-selected').length`)) !== 2)
    throw new Error("市场选择未同步")
  await evaluate(`document.querySelector('.market-option input').click()`)
  await evaluate(`document.querySelector('.market-option input').focus()`)
  for (const expected of [2, 3]) {
    await send("Input.dispatchKeyEvent", {
      type: "keyDown",
      key: " ",
      code: "Space",
      windowsVirtualKeyCode: 32
    })
    await send("Input.dispatchKeyEvent", {
      type: "keyUp",
      key: " ",
      code: "Space",
      windowsVirtualKeyCode: 32
    })
    if (
      (await evaluate(`document.querySelectorAll('.market-option input:checked').length`)) !==
      expected
    )
      throw new Error("市场选框键盘操作失败")
  }

  await evaluate(`document.querySelector('button[aria-label="浏览保存目录"]').click()`)
  await waitFor(`document.body.innerText.includes('选择保存目录')`, "网页目录选择侧栏")
  await waitFor(
    `document.querySelector('input[aria-label="当前路径"]')?.value.length > 0`,
    "目录回退与读取"
  )
  await screenshot("directory.png")
  await clickText("项目目录")
  await waitFor(
    `document.querySelector('input[aria-label="当前路径"]')?.value === ${JSON.stringify(root)}`,
    "快捷目录跳转"
  )
  await clickText("使用当前目录")
  await waitFor(`!document.body.innerText.includes('选择文件的保存位置')`, "关闭目录侧栏")
  const selectedOutput = await evaluate(`document.querySelector('#output-dir').value`)
  if (selectedOutput !== root) throw new Error(`目录选择未回填：${selectedOutput}`)

  await clickText("指定代码")
  await waitFor(`Boolean(document.querySelector('textarea'))`, "指定代码输入区")
  await clickText("开始下载")
  await waitFor(`document.body.innerText.includes('请填写至少一个证券代码')`, "空代码表单校验")
  await clickText("按市场下载")

  await send("Page.navigate", { url: `${base}/` })
  await waitFor(`document.body.innerText.includes('已就绪，下载前')`, "重新载入页面")
  await send("Emulation.setDeviceMetricsOverride", {
    width: 390,
    height: 844,
    deviceScaleFactor: 1,
    mobile: true
  })
  await sleep(300)
  const mobileOverflow = await evaluate(`document.documentElement.scrollWidth > window.innerWidth`)
  await evaluate(`window.scrollTo(0, 0)`)
  await screenshot("mobile.png")

  let realDownload = false
  let pricePreview = false
  let minutePagination = false
  let warehouseVerified = false
  let localImportVerified = false
  const downloads = []
  if (process.env.QFLUX_SMOKE_DOWNLOAD === "1") {
    await send("Emulation.setDeviceMetricsOverride", {
      width: 1440,
      height: 1000,
      deviceScaleFactor: 1,
      mobile: false
    })
    const setField = async (selector, value) => {
      const updated = await evaluate(`(() => {
        const element = document.querySelector(${JSON.stringify(selector)})
        if (!element) return false
        const prototype = element.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : element.tagName === 'SELECT' ? HTMLSelectElement.prototype : HTMLInputElement.prototype
        Object.getOwnPropertyDescriptor(prototype, 'value').set.call(element, ${JSON.stringify(String(value))})
        element.dispatchEvent(new Event('input', { bubbles: true }))
        element.dispatchEvent(new Event('change', { bubbles: true }))
        return true
      })()`)
      if (!updated) throw new Error(`找不到输入框：${selector}`)
    }
    await evaluate(`document.querySelector('.advanced-settings').open = true`)
    await clickText("指定代码")
    await setField("textarea", "600000")
    await setField("#output-dir", path.join(artifacts, "smoke-download"))
    await setField("#start-date", "2020-01-01")
    await setField("#timeout", 2)
    await setField("#workers", "")
    await clickText("开始下载")
    await waitFor(`document.body.innerText.includes('同时下载数量须为')`, "高级参数空值校验")
    await setField("#workers", 2)
    await setField("#start-date", "2999-01-01")
    await clickText("开始下载")
    await waitFor(`document.body.innerText.includes('开始日期不能晚于结束日期')`, "日期先后校验")
    const feedbackVisible = await evaluate(
      `(() => { const r = document.querySelector('.form-feedback').getBoundingClientRect(); return r.top >= 0 && r.bottom <= innerHeight })()`
    )
    if (!feedbackVisible) throw new Error("表单校验错误未展示在可见区域")
    await setField("#start-date", "2020-01-01")
    await clickText("开始下载")
    await waitFor(
      `['下载完成', '完成 · 需留意', '下载失败', '已停止'].includes(document.querySelector('.state-badge')?.textContent.trim())`,
      "真实股票下载任务",
      180000
    )
    const finalState = await evaluate(`document.querySelector('.state-badge')?.textContent.trim()`)
    if (!["下载完成", "完成 · 需留意"].includes(finalState)) {
      const taskMessage = await evaluate(
        `document.querySelector('.state-badge')?.parentElement?.parentElement?.innerText`
      )
      throw new Error(`真实下载未完成：${finalState} ${taskMessage}`)
    }
    realDownload = true
    const downloaded = await fetch(`${base}/api/task`).then((r) => r.json())
    if (downloaded.bars <= 800) throw new Error(`真实日线拼接未超过 800 条：${downloaded.bars}`)
    downloads.push({
      category: "daily",
      bars: downloaded.bars,
      pages: downloaded.pages,
      from: downloaded.recent[0].from,
      to: downloaded.recent[0].to
    })
    await evaluate(`window.scrollTo(0, 0)`)
    await screenshot("download-results.png")
    await evaluate(`document.querySelector('button[aria-label="预览 sh600000_day.csv"]').click()`)
    await waitFor(
      `document.querySelectorAll('[data-slot="sheet-content"] tbody tr').length > 0`,
      "行情预览表格"
    )
    await screenshot("price-preview.png")
    await send("Emulation.setDeviceMetricsOverride", {
      width: 390,
      height: 844,
      deviceScaleFactor: 1,
      mobile: true
    })
    await sleep(200)
    if (await evaluate(`document.documentElement.scrollWidth > window.innerWidth`))
      throw new Error("手机预览页面横向溢出")
    await waitFor(
      `document.querySelector('[data-slot="sheet-content"] .recharts-surface')?.getBoundingClientRect().height > 100`,
      "手机行情图表可见"
    )
    await screenshot("mobile-preview.png")
    await evaluate(
      `document.querySelector('[data-slot="sheet-content"] button[aria-label="关闭"]').click()`
    )
    await sleep(250)
    if (await evaluate(`document.documentElement.scrollWidth > window.innerWidth`))
      throw new Error("手机结果页面横向溢出")
    await screenshot("mobile-results.png")
    pricePreview = true
    await send("Emulation.setDeviceMetricsOverride", {
      width: 1440,
      height: 1000,
      deviceScaleFactor: 1,
      mobile: false
    })
    const monthStart = await evaluate(
      `(() => { const d = new Date(document.querySelector('#end-date').value + 'T12:00:00Z'); d.setUTCMonth(d.getUTCMonth()-1); return d.toISOString().slice(0,10) })()`
    )
    await setField("#category", 7)
    await setField("#start-date", monthStart)
    await clickText("开始下载")
    await waitFor(
      `document.querySelector('.task-summary > span')?.textContent === '1 分钟'`,
      "分钟线任务启动"
    )
    await waitFor(
      `['下载完成', '完成 · 需留意', '下载失败', '已停止'].includes(document.querySelector('.state-badge')?.textContent.trim())`,
      "真实分钟线拼接",
      180000
    )
    const minuteTask = await fetch(`${base}/api/task`).then((r) => r.json())
    if (minuteTask.options.category !== 7 || minuteTask.bars <= 800 || minuteTask.failed > 0)
      throw new Error(`真实分钟线拼接失败：${JSON.stringify(minuteTask)}`)
    minutePagination = true
    downloads.push({
      category: "minute",
      bars: minuteTask.bars,
      pages: minuteTask.pages,
      from: minuteTask.recent[0].from,
      to: minuteTask.recent[0].to
    })
    await evaluate(`window.scrollTo(0, 0)`)
    await screenshot("minute-results.png")
    const beforeLibrary = await fetch(`${base}/api/warehouse`).then((r) => r.json())
    if (beforeLibrary.rows !== downloaded.bars + minuteTask.bars || beforeLibrary.symbols !== 1)
      throw new Error(`DuckDB 入库数量错误：${JSON.stringify(beforeLibrary)}`)
    await clickText("数据仓库")
    await waitFor(
      `document.querySelectorAll('.dataset-browser tbody tr').length === 2`,
      "DuckDB 数据集列表"
    )
    await screenshot("warehouse-desktop.png")
    await clickText("查看")
    await waitFor(
      `document.querySelectorAll('[data-slot="sheet-content"] tbody tr').length > 0`,
      "DuckDB 行情预览"
    )
    await screenshot("warehouse-preview.png")
    await evaluate(
      `document.querySelector('[data-slot="sheet-content"] button[aria-label="关闭"]').click()`
    )
    await sleep(250)
    await clickText("导出 Parquet")
    await waitFor(`document.body.innerText.includes('已导出 Parquet')`, "Parquet 导出")
    await send("Emulation.setDeviceMetricsOverride", {
      width: 390,
      height: 844,
      deviceScaleFactor: 1,
      mobile: true
    })
    await sleep(200)
    if (await evaluate(`document.documentElement.scrollWidth > innerWidth`))
      throw new Error("手机仓库页面横向溢出")
    await evaluate(`window.scrollTo(0,0)`)
    await screenshot("warehouse-mobile.png")
    warehouseVerified = true
    await send("Emulation.setDeviceMetricsOverride", {
      width: 1440,
      height: 1000,
      deviceScaleFactor: 1,
      mobile: false
    })
    const importDir = path.join(artifacts, "browser-import-fixture")
    fs.mkdirSync(importDir, { recursive: true })
    fs.copyFileSync(
      path.join(artifacts, "smoke-download/sh600000_day.csv"),
      path.join(importDir, "sh600000_day.csv")
    )
    fs.writeFileSync(
      path.join(importDir, "sh600001_day.csv"),
      "date,open,high,low,close,volume,amount\n2024-01-02,10,8,9,11,1,100\n"
    )
    await setField("#import-directory", importDir)
    await clickText("校验并导入 DuckDB")
    await waitFor(
      `document.querySelector('.task-summary > span')?.textContent === '本地历史补录'`,
      "离线历史补录启动"
    )
    await waitFor(
      `document.querySelector('.state-badge')?.textContent.trim() === '完成 · 需留意'`,
      "质量隔离与补录完成"
    )
    const imported = await fetch(`${base}/api/task`).then((r) => r.json())
    if (imported.succeeded !== 1 || imported.failed !== 1)
      throw new Error("补录没有隔离无效 OHLC 文件")
    const afterImport = await fetch(`${base}/api/warehouse`).then((r) => r.json())
    if (afterImport.rows !== beforeLibrary.rows)
      throw new Error("重复历史补录造成重数或无效数据入库")
    await screenshot("import-results.png")
    localImportVerified = true
  }

  const summary = {
    reactRender: true,
    darkTheme: true,
    pathBrowser: true,
    scopeSwitch: true,
    formValidation: true,
    realDownload,
    pricePreview,
    minutePagination,
    warehouseVerified,
    localImportVerified,
    downloads,
    uniformControlHeight: true,
    datePresets: true,
    keyboardCheckboxes: true,
    mobileOverflow,
    requestCount: requestSizes.size,
    transferredBytes: [...requestSizes.values()].reduce((total, value) => total + value, 0),
    consoleErrors,
    httpErrors
  }
  fs.writeFileSync(path.join(artifacts, "browser-smoke.json"), JSON.stringify(summary, null, 2))
  console.log(JSON.stringify(summary, null, 2))
  if (mobileOverflow || consoleErrors.length || httpErrors.length) process.exitCode = 1
} finally {
  socket.close()
}
