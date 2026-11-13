import test from "node:test"
import assert from "node:assert/strict"
import fs from "node:fs"
import ts from "../frontend/node_modules/typescript/lib/typescript.js"

const source = fs.readFileSync(new URL("../frontend/src/lib/realtime.ts", import.meta.url), "utf8")
const javascript = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText
const { carryRealtimeQuotes, retainRealtimeNames, retainRealtimeTrades, stockKey, realtimePrice } = await import(`data:text/javascript;base64,${Buffer.from(javascript).toString("base64")}`)
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

test("仅成交字段变化或切股时替换成交窗口引用", () => {
  const trade = { time: "10:00:03", price: 43.01, volume: 20, trades: 2, side: 0 }
  const before = { ...snapshot, trades: [trade] }
  const after = { ...before, receivedAt: "2026-11-10T02:00:03Z", trades: [{ ...trade }] }
  const retained = retainRealtimeTrades(before, after)
  assert.equal(retained.trades, before.trades)
  assert.equal(retained.receivedAt, after.receivedAt)
  for (const patch of [{ time: "10:00:06" }, { price: 44 }, { volume: 21 }, { trades: 3 }, { side: 1 }]) {
    const changed = { ...after, trades: [{ ...trade, ...patch }] }
    assert.equal(retainRealtimeTrades(before, changed), changed)
  }
  const switched = { ...after, selected: "sz000001" }
  assert.equal(retainRealtimeTrades(before, switched), switched)
})

test("实时自选自动识别沪深 ETF 和转债，价格保留有效小数位", () => {
  for (const [code, key] of [["111024", "sh111024"], ["589020", "sh589020"], ["159915", "sz159915"], ["123001", "sz123001"], ["920002", "bj920002"]]) {
    assert.equal(stockKey(code), key)
  }
  assert.equal(realtimePrice(0.882), "0.882")
  assert.equal(realtimePrice(218.4557), "218.4557")
  assert.equal(realtimePrice(42.6), "42.60")
})

test("没有报价的自选证券仍可显示异步补齐的名称", () => {
  const codes = ["sh111024", "sh589020"]
  const result = retainRealtimeNames({}, { ...snapshot, quotes: [], names: { sh111024: "澳弘转债", sh589020: "KC半导体" } }, codes)
  assert.deepEqual(result.names, { sh111024: "澳弘转债", sh589020: "KC半导体" })
  assert.deepEqual(result.data.quotes, [])
})
