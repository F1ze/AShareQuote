// Dates always follow the exchange timezone, including on overseas computers.
export function today() {
  return new Intl.DateTimeFormat("en-CA", {
    timeZone: "Asia/Shanghai",
    year: "numeric",
    month: "2-digit",
    day: "2-digit"
  }).format(new Date())
}

export function monthsAgo(months: number, end = today()) {
  const date = new Date(`${end}T12:00:00Z`)
  const day = date.getUTCDate()
  date.setUTCDate(1)
  date.setUTCMonth(date.getUTCMonth() - months)
  const last = new Date(Date.UTC(date.getUTCFullYear(), date.getUTCMonth() + 1, 0)).getUTCDate()
  date.setUTCDate(Math.min(day, last))
  return date.toISOString().slice(0, 10)
}

export function yesterday() {
  const date = new Date(`${today()}T12:00:00Z`)
  date.setUTCDate(date.getUTCDate() - 1)
  return date.toISOString().slice(0, 10)
}

export const periods = [
  { value: 9, label: "日线" },
  { value: 5, label: "周线" },
  { value: 6, label: "月线" },
  { value: 7, label: "1 分钟" },
  { value: 0, label: "5 分钟" },
  { value: 1, label: "15 分钟" },
  { value: 2, label: "30 分钟" },
  { value: 3, label: "60 分钟" },
  { value: 10, label: "季线" },
  { value: 11, label: "年线" }
]

export function periodName(value: number) {
  return periods.find((item) => item.value === value)?.label || "日线"
}
