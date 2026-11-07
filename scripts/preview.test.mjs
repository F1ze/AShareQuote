import test from "node:test"
import assert from "node:assert/strict"
import fs from "node:fs"
import ts from "../frontend/node_modules/typescript/lib/typescript.js"
const source = fs.readFileSync(new URL("../frontend/src/lib/preview.ts", import.meta.url), "utf8")
const javascript = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText
const { chartPoints, dateWindow } = await import(`data:text/javascript;base64,${Buffer.from(javascript).toString("base64")}`)

const bars = Array.from({ length: 20001 }, (_, i) => ({ date: `2024-01-${i < 10000 ? "02" : "03"} ${String(Math.floor(i / 60) % 24).padStart(2, "0")}:${String(i % 60).padStart(2, "0")}`, open: 10, high: 20, low: 1, close: i === 1923 ? 999 : i === 9021 ? -999 : 10, volume: 0.95, amount: 100 }))
test("日期范围包含结束日全部分钟记录，并拒绝无数据与倒置范围", () => {
  assert.deepEqual(dateWindow(bars, "2024-01-02", "2024-01-02"), { start: 0, end: 9999 })
  assert.deepEqual(dateWindow(bars, "2024-01-02", "2024-01-03"), { start: 0, end: 20000 })
  assert.equal(dateWindow(bars, "2024-01-04", "2024-01-05"), null)
  assert.equal(dateWindow(bars, "2024-01-03", "2024-01-02"), null)
  assert.equal(dateWindow([], "2024-01-02", "2024-01-03"), null)
})
test("大范围采样保留端点和尖峰，放大后完整绘制，原始序列不变", () => {
  const points = chartPoints(bars, { start: 0, end: bars.length - 1 })
  assert.ok(points.length <= 1200)
  assert.equal(points[0].index, 0)
  assert.equal(points.at(-1)?.index, 20000)
  assert.ok(points.some((point) => point.close === 999))
  assert.ok(points.some((point) => point.close === -999))
  assert.equal(chartPoints(bars, { start: 100, end: 130 }).length, 31)
  assert.equal(bars.length, 20001)
  assert.deepEqual(chartPoints([], { start: 0, end: -1 }), [])
})
