import test from "node:test"
import assert from "node:assert/strict"
import fs from "node:fs"
import ts from "../frontend/node_modules/typescript/lib/typescript.js"

const source = fs.readFileSync(new URL("../frontend/src/lib/realtime.ts", import.meta.url), "utf8")
const javascript = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText
const { carryRealtimeQuotes, retainRealtimeNames } = await import(`data:text/javascript;base64,${Buffer.from(javascript).toString("base64")}`)
const a = { market: 1, code: "600036", name: "招商银行", price: 43.01 }
const b = { market: 0, code: "000001", name: "平安银行", price: 11.89 }
const snapshot = { selected: "sh600036", quotes: [a, b], trades: [{ time: "10:00:03", price: 43.01 }], receivedAt: "2026-11-10T02:00:00Z", detailStatus: "available" }

test("切股保留各自股票名称与旧快照时间，清空前一股票成交窗口", () => {
  const next = carryRealtimeQuotes(snapshot, ["sh600036", "sz000001"], "sz000001")
  assert.equal(next.selected, "sz000001")
  assert.equal(next.quotes.find((quote) => quote.code === "000001").name, "平安银行")
  assert.equal(next.quotes.find((quote) => quote.code === "000001").price, 11.89)
  assert.equal(next.receivedAt, snapshot.receivedAt)
  assert.deepEqual(next.trades, [])
  assert.equal(snapshot.trades.length, 1)
  assert.deepEqual(carryRealtimeQuotes(snapshot, ["sz000001"], "sz000001").quotes, [b])
  assert.equal(carryRealtimeQuotes(null, ["sz000001"], "sz000001"), null)
})

test("瞬时空名称不覆盖已知名称，真实更新生效，不按六位代码串市场", () => {
  const next = { ...snapshot, quotes: [{ ...a, name: "" }, { ...b, name: "平安银行更新" }, { market: 1, code: "000001", name: "" }] }
  const result = retainRealtimeNames({ sh600036: "招商银行", sz000001: "平安银行", sh600519: "移除股票" }, next, ["sh600036", "sz000001", "sh000001"])
  assert.equal(result.data.quotes[0].name, "招商银行")
  assert.equal(result.data.quotes[1].name, "平安银行更新")
  assert.equal(result.data.quotes[2].name, "")
  assert.equal(result.names.sh600519, undefined)
  assert.equal(next.quotes[0].name, "")
})
