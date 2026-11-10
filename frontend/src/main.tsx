import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import "./index.css"


const root = createRoot(document.getElementById("root")!)
if (new URLSearchParams(location.search).has("widget")) {
  void import("./components/QuoteWidget").then(({ QuoteWidget }) => root.render(<StrictMode><QuoteWidget /></StrictMode>))
} else {
  void import("./App").then(({ default: App }) => root.render(<StrictMode><App /></StrictMode>))
}
