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
  if (appearance.serviceColor !== theme.online) throw new Error(`服务状态缺少清晰颜色反馈：${JSON.stringify({appearance, theme})}`)
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

  await evaluate(`document.querySelector('.advanced-settings').open = true`)
  const setTrafficField = async (id, value) => evaluate(`(() => {
    const element = document.getElementById(${JSON.stringify(id)})
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(element, ${JSON.stringify(String(value))})
    element.dispatchEvent(new Event('input',{bubbles:true})); element.dispatchEvent(new Event('change',{bubbles:true}))
  })()`)
  await setTrafficField("request-rate", 0)
  await clickText("开始下载")
  await waitFor(`document.body.innerText.includes('全局请求频率须为')`, "全局频率范围校验")
  await setTrafficField("request-rate", 40)

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
  // 悬停只显示已有结果，不自行探测；模拟返回用于稳定检查名称、IPv6、键盘和手机交互。
  await send("Emulation.setDeviceMetricsOverride", {width:1440,height:1000,deviceScaleFactor:1,mobile:false})
  await evaluate(`window.scrollTo(0,0); window.hostSmokeFetch = window.fetch; window.hostSmokeCalls = 0;
    window.fetch = async (input, init) => {
      if (new URL(String(input),location.href).pathname !== '/api/hosts/test') return window.hostSmokeFetch(input,init)
      window.hostSmokeCalls++
      await new Promise(resolve => setTimeout(resolve,500))
      return new Response(JSON.stringify({checkedAt:new Date().toISOString(),probe:"quote",tested:13,available:12,results:[
        {name:'银河证券上证云上海二',address:'114.141.177.40:7709',ok:true,latencyMs:82},
        {name:'银河证券IPV6行情',address:'[2408:4003:1f20::1e6]:7709',ok:true,latencyMs:128},
        {name:'失败测试站',address:'127.0.0.2:7709',ok:false,latencyMs:1000,error:'行情握手失败'},
        ...Array.from({length:10},(_,i)=>({name:'行情测试站 '+(i+1),address:'192.0.2.'+(i+1)+':7709',ok:true,latencyMs:150+i}))
      ]}), {headers:{'Content-Type':'application/json'}})
    }`)
  const hostButton = await evaluate(`document.querySelector('[data-testid="available-hosts-trigger"]').getBoundingClientRect().toJSON()`)
  await send("Input.dispatchMouseEvent", {type:"mouseMoved",x:hostButton.x+10,y:hostButton.y+10})
  await waitFor(`document.body.innerText.includes('尚无检测结果')`, "悬停线路空状态")
  if (await evaluate(`window.hostSmokeCalls !== 0`)) throw new Error("悬停不应发起网络探测")
  await clickText("重新检测")
  await waitFor(`Boolean(document.querySelector('[data-testid="available-hosts-trigger"] [aria-label="线路检测中"]'))`, "线路加载状态")
  await waitFor(`document.querySelector('[data-testid="available-hosts-trigger"]')?.innerText.includes('可用 12/13')`, "可用线路统计")
  await waitFor(`document.querySelector('table[aria-label="全部行情站点"]')?.innerText.includes('[2408:4003:1f20::1e6]:7709')`, "站点名称与 IPv6 地址")
  await waitFor(`document.body.innerText.includes("完整行情往返") && document.querySelector('table[aria-label="全部行情站点"] thead')?.innerText.includes("往返耗时")`, "完整行情测速口径说明")
  if (await evaluate(`document.querySelectorAll('table[aria-label="全部行情站点"] tbody tr').length !== 13 || !document.querySelector('table[aria-label="全部行情站点"]').innerText.includes('失败测试站')`)) throw new Error("站点状态未完整展示")
  await screenshot("available-hosts-desktop.png")
  await send("Input.dispatchKeyEvent", {type:"keyDown",key:"Escape",code:"Escape",windowsVirtualKeyCode:27})
  await send("Input.dispatchKeyEvent", {type:"keyUp",key:"Escape",code:"Escape",windowsVirtualKeyCode:27})
  await waitFor(`!document.querySelector('[data-slot="popover-content"]')`, "关闭固定站点面板")
  await send("Input.dispatchMouseEvent", {type:"mouseMoved",x:5,y:5})
  const hostButtonAfter = await evaluate(`document.querySelector('[data-testid="available-hosts-trigger"]').getBoundingClientRect().toJSON()`)
  await send("Input.dispatchMouseEvent", {type:"mouseMoved",x:hostButtonAfter.x+10,y:hostButtonAfter.y+10})
  await waitFor(`Boolean(document.querySelector('[data-slot="popover-content"]'))`, "再次悬停站点列表")
  await send("Input.dispatchMouseEvent", {type:"mouseMoved",x:5,y:5})
  await evaluate(`document.activeElement?.blur()`)
  await waitFor(`!document.querySelector('[data-slot="popover-content"]')`, "移出悬浮面板关闭")
  await evaluate(`document.querySelector('[data-testid="available-hosts-trigger"]').focus()`)
  await send("Input.dispatchKeyEvent", {type:"keyDown",key:"Enter",code:"Enter",text:"\r",windowsVirtualKeyCode:13})
  await send("Input.dispatchKeyEvent", {type:"keyUp",key:"Enter",code:"Enter",windowsVirtualKeyCode:13})
  await waitFor(`Boolean(document.querySelector('[data-slot="popover-content"]'))`, "键盘打开站点列表")
  await send("Input.dispatchKeyEvent", {type:"keyDown",key:"Escape",code:"Escape",windowsVirtualKeyCode:27})
  await send("Input.dispatchKeyEvent", {type:"keyUp",key:"Escape",code:"Escape",windowsVirtualKeyCode:27})
  await waitFor(`!document.querySelector('[data-slot="popover-content"]')`, "Escape 关闭站点列表")
  await send("Emulation.setDeviceMetricsOverride", {width:390,height:844,deviceScaleFactor:1,mobile:true})
  await evaluate(`window.scrollTo(0,0);document.querySelector('[data-testid="available-hosts-trigger"]').click()`)
  await waitFor(`Boolean(document.querySelector('[data-slot="popover-content"]'))`, "手机点击站点列表")
  if (await evaluate(`document.querySelector('[data-slot="popover-content"]').getBoundingClientRect().right > innerWidth || document.documentElement.scrollWidth > innerWidth`)) throw new Error("手机线路浮层横向溢出")
  await screenshot("available-hosts-mobile.png")
  await send("Input.dispatchKeyEvent", {type:"keyDown",key:"Escape",code:"Escape",windowsVirtualKeyCode:27})
  await send("Input.dispatchKeyEvent", {type:"keyUp",key:"Escape",code:"Escape",windowsVirtualKeyCode:27})
  await evaluate(`window.fetch = window.hostSmokeFetch`)
  // 离线构造超过两个分页的分钟历史，回归完整读取、日期边界和真实指针框选。
  // 仅替换当前浏览器的 fetch，服务端、用户仓库和下载文件均不改动。
  await send("Emulation.setDeviceMetricsOverride", { width: 1440, height: 1000, deviceScaleFactor: 1, mobile: false })
  await evaluate(`(async () => {
    window.previewOriginalFetch = window.fetch
    const status = await fetch('/api/warehouse').then(r => r.json())
    const rows = Array.from({length: 2301}, (_, i) => ({ts: new Date(Date.UTC(2024,0,2,9,30+i)).toISOString().replace('T',' ').slice(0,19), open:'10', high:'12', low:'9', close:String(10 + Math.sin(i/20)), volume_shares:95, amount_cny:'1000'}))
    const dataset = {exchange:'SH', code:'600000', name:'预览测试', period:'1m', rows:rows.length, earliest:rows[0].ts, latest:rows.at(-1).ts, updatedAt:rows.at(-1).ts}
    const options = {source:'tdx', markets:[0,1,2], codes:'', category:9, start:0, count:800, startDate:'2024-01-01', endDate:'2024-12-31', workers:32, requestRate:40, timeout:8, outputDir:'D:\\qflux', gbbqPath:'', storage:'csv', incremental:false, aSharesOnly:true}
    const job = {id:'retry-fixture', state:'completed_with_errors', total:3, done:3, succeeded:2, failed:1, bars:480, pages:3, warnings:0, skipped:0, percent:100, speed:1, elapsed:3, message:'1 只失败', outputDir:options.outputDir, startedAt:'2026-01-01T12:00:00+08:00', options, recent:[], logs:[]}
    const deleteJob = {...job, id:'delete-fixture', startedAt:'2026-01-02T12:00:00+08:00'}
    window.previewPageCalls = 0
    window.retryCalls = 0
    window.deleteCalls = 0
    window.fetch = async (input, init) => {
      const url = new URL(String(input), location.href)
      if (url.pathname === '/api/jobs/delete-fixture' && init?.method === 'DELETE') {
        window.deleteCalls++
        return new Response(null, {status:204})
      }
      let body
      if (url.pathname === '/api/warehouse') body = {...status, ready:true, rows:2301, symbols:1, earliest:dataset.earliest, latest:dataset.latest}
      if (url.pathname === '/api/warehouse/datasets') body = {items:[dataset], total:1, page:1, pageSize:50}
      if (url.pathname === '/api/jobs') body = [job, deleteJob]
      if (url.pathname === '/api/jobs/retry-fixture/resume') {
        window.retryCalls++
        body = {...job, id:'retry-run', state:'running', done:0, succeeded:0, failed:0, percent:0}
      }
      if (url.pathname === '/api/warehouse/bars') {
        window.previewPageCalls++
        const before = url.searchParams.get('before')
        body = rows.filter(row => !before || row.ts < before).slice(-Number(url.searchParams.get('limit')))
      }
      return body === undefined ? window.previewOriginalFetch(input, init) : new Response(JSON.stringify(body), {headers:{'Content-Type':'application/json'}})
    }
  })()`)
  await clickText("数据仓库")
  await waitFor(`document.querySelector('.dataset-browser tbody')?.innerText.includes('预览测试')`, "离线历史数据集")
  await clickText("查看")
  await waitFor(`document.querySelector('[data-testid="preview-coverage"]')?.innerText.includes('2,301')`, "加载超过 1000 条完整历史")
  if (await evaluate(`window.previewPageCalls < 3 || window.previewPageCalls > 6`)) throw new Error(`仓库分页异常：${await evaluate('window.previewPageCalls')}`)
  await clickText("最早")
  await waitFor(`document.querySelector('[data-slot="sheet-content"] tbody')?.innerText.includes('2024-01-02 09:30:00')`, "明细最早记录")
  const setPreviewDate = async (label, value) => evaluate(`(() => {
    const input = document.querySelector('[data-slot="sheet-content"] input[aria-label="${label}"]')
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(input, '${value}')
    input.dispatchEvent(new Event('input',{bubbles:true})); input.dispatchEvent(new Event('change',{bubbles:true}))
  })()`)
  await setPreviewDate("开始日期", "2024-01-03")
  await setPreviewDate("结束日期", "2024-01-03")
  await clickText("应用日期")
  await waitFor(`document.querySelector('[data-testid="preview-selection"]')?.innerText.includes('1,431')`, "结束日包含全部分钟记录")
  await setPreviewDate("开始日期", "2025-01-01")
  await setPreviewDate("结束日期", "2025-01-02")
  await clickText("应用日期")
  await waitFor(`document.querySelector('[data-slot="sheet-content"] [role="alert"]')?.innerText.includes('没有行情')`, "日期空范围反馈")
  await clickText("查看全部")
  await evaluate(`document.querySelector('[data-testid="preview-chart"]').scrollIntoView({block:'center'})`)
  const chart = await evaluate(`document.querySelector('[data-testid="preview-chart"] .recharts-wrapper').getBoundingClientRect().toJSON()`)
  const y = chart.y + chart.height / 2
  await send("Input.dispatchMouseEvent", {type:"mouseMoved", x:chart.x+chart.width*.3, y})
  await send("Input.dispatchMouseEvent", {type:"mousePressed", button:"left", clickCount:1, x:chart.x+chart.width*.3, y})
  for (let i=3; i<=7; i++) { await send("Input.dispatchMouseEvent", {type:"mouseMoved", buttons:1, x:chart.x+chart.width*i/10, y}); await sleep(50) }
  await send("Input.dispatchMouseEvent", {type:"mouseReleased", button:"left", clickCount:1, x:chart.x+chart.width*.7, y})
  await waitFor(`!document.querySelector('[data-testid="preview-selection"]')?.innerText.includes('2,301')`, "指针框选缩放")
  await screenshot("preview-selection.png")
  await clickText("查看全部")
  await waitFor(`document.querySelector('[data-testid="preview-selection"]')?.innerText.includes('2,301')`, "重置完整范围")
  await evaluate(`document.querySelector('.recharts-brush-traveller').focus()`)
  await send("Input.dispatchKeyEvent", {type:"keyDown", key:"ArrowRight", code:"ArrowRight", windowsVirtualKeyCode:39})
  await send("Input.dispatchKeyEvent", {type:"keyUp", key:"ArrowRight", code:"ArrowRight", windowsVirtualKeyCode:39})
  await waitFor(`!document.querySelector('[data-testid="preview-selection"]')?.innerText.includes('2,301')`, "键盘调整历史范围")
  await clickText("查看全部")
  await send("Emulation.setDeviceMetricsOverride", {width:390,height:844,deviceScaleFactor:1,mobile:true})
  await evaluate(`document.querySelector('[data-slot="sheet-content"]').scrollTo(0,0)`)
  await sleep(250)
  if (await evaluate(`document.querySelector('[data-slot="sheet-content"]').scrollWidth > innerWidth`)) throw new Error("手机行情预览横向溢出")
  await screenshot("preview-mobile-full.png")
  await evaluate(`document.querySelector('[data-slot="sheet-content"] button[aria-label="关闭"]').click()`)
  await send("Emulation.setDeviceMetricsOverride", {width:1440,height:1000,deviceScaleFactor:1,mobile:false})
  await sleep(200)
  await waitFor(`[...document.querySelectorAll('.job-row button')].some(button => button.textContent.includes('重试 1 只失败'))`, "失败任务重试入口")
  const jobRowHeight = await evaluate(`document.querySelector('.job-row').getBoundingClientRect().height`)
  if (jobRowHeight > 70) throw new Error(`任务记录行高未压缩：${jobRowHeight}`)
  await evaluate(`document.querySelector('.library-jobs').scrollIntoView({block:'center'})`)
  await screenshot("recovery-records.png")
  await send("Emulation.setDeviceMetricsOverride", {width:390,height:844,deviceScaleFactor:1,mobile:true})
  await evaluate(`document.querySelector('.library-jobs').scrollIntoView({block:'center'})`)
  await sleep(200)
  if (await evaluate(`document.documentElement.scrollWidth > innerWidth`)) throw new Error("手机任务记录横向溢出")
  await screenshot("recovery-records-mobile.png")
  await send("Emulation.setDeviceMetricsOverride", {width:1440,height:1000,deviceScaleFactor:1,mobile:false})
  const deleteTrigger = `document.querySelector('button[aria-label="删除任务记录 2026-01-02T12:00:00+08:00"]')`
  await evaluate(`${deleteTrigger}.click()`)
  await waitFor(`document.body.innerText.includes('不会删除已下载的 CSV')`, "删除记录确认弹窗")
  await clickText("保留记录")
  await waitFor(`!document.querySelector('[data-slot="alert-dialog-content"]')`, "取消删除记录")
  if (await evaluate(`window.deleteCalls !== 0`)) throw new Error("取消删除仍调用了接口")
  await evaluate(`${deleteTrigger}.click()`)
  await clickText("删除记录")
  await waitFor(`window.deleteCalls === 1 && !${deleteTrigger} && document.body.innerText.includes('行情文件未受影响')`, "删除记录并保留行情文件")
  await clickText("重试 1 只失败")
  await waitFor(`window.retryCalls === 1`, "重试请求接入现有恢复接口")
  await evaluate(`window.fetch = window.previewOriginalFetch`)
  await send("Page.navigate", {url:`${base}/`})
  await waitFor(`document.body.innerText.includes('已就绪，下载前') && Boolean(document.querySelector('.welcome-state'))`, "重置离线任务场景")
  await clickText("数据采集")
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
    const resultRowHeight = await evaluate(`document.querySelector('.result-row')?.getBoundingClientRect().height || 0`)
    if (resultRowHeight <= 0 || resultRowHeight > 60) throw new Error(`下载结果行高异常：${resultRowHeight}`)
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
    trafficLimitValidation: true,
    availableHostNames: true,
    hostsHoverKeyboardAndMobile: true,
    fullHistoryPreview: true,
    previewDateRange: true,
    previewBoxZoom: true,
    previewTablePagination: true,
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
