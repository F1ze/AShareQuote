#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::collections::HashMap;
use std::sync::Mutex;
use std::time::Duration;
use tauri::State;
use tokio::sync::oneshot;

// 原生壳只访问本机 Go 后端的两个固定端点，不直连行情站，不持有行情协议或数据库。
// 主工程不依赖此 Cargo 包；未安装桌面壳时，轻量网页仍可独立使用。
struct WidgetState {
    client: reqwest::Client,
    base: String,
    // 窗口隐藏、切股或关闭时由前端取消；新请求也会主动废弃旧请求，禁止后台堆积。
    pending: Mutex<HashMap<String, oneshot::Sender<()>>>,
}

async fn read_json(response: reqwest::Response) -> Result<serde_json::Value, String> {
    let status = response.status();
    let value: serde_json::Value = response.json().await.map_err(|e| e.to_string())?;
    if !status.is_success() {
        return Err(value["error"].as_str().unwrap_or("本机行情服务返回错误").to_owned());
    }
    Ok(value)
}

#[tauri::command]
async fn watch_settings(state: State<'_, WidgetState>) -> Result<serde_json::Value, String> {
    let response = state.client.get(format!("{}/realtime/watch", state.base))
        .send().await.map_err(|e| format!("请启动 QFLUX 后端：{e}"))?;
    read_json(response).await
}

#[tauri::command]
async fn quotes(request_id: String, codes: String, selected: String, state: State<'_, WidgetState>) -> Result<serde_json::Value, String> {
    if request_id.len() > 64 || codes.len() > 512 || selected.len() > 32 {
        return Err("小组件参数过长".into());
    }
    let (sender, receiver) = oneshot::channel();
    {
        let mut pending = state.pending.lock().map_err(|_| "请求状态不可用")?;
        for (_, old) in pending.drain() { let _ = old.send(()); }
        pending.insert(request_id.clone(), sender);
    }
    let fetch = async {
        let response = state.client.get(format!("{}/realtime", state.base))
            .query(&[("codes", codes.as_str()), ("selected", selected.as_str()), ("details", "false")])
            .send().await.map_err(|e| e.to_string())?;
        read_json(response).await
    };
    let result = tokio::select! {
        result = fetch => result,
        _ = receiver => Err("行情请求已取消".into()),
    };
    if let Ok(mut pending) = state.pending.lock() { pending.remove(&request_id); }
    result
}

#[tauri::command]
fn cancel_quote(request_id: String, state: State<'_, WidgetState>) {
    if let Ok(mut pending) = state.pending.lock() {
        if let Some(sender) = pending.remove(&request_id) { let _ = sender.send(()); }
    }
}

fn main() {
    let port = std::env::var("QFLUX_WIDGET_API_PORT").ok()
        .and_then(|s| s.parse::<u16>().ok()).filter(|p| *p > 0).unwrap_or(7101);
    let client = reqwest::Client::builder().timeout(Duration::from_secs(8))
        // 本机 API 不走系统代理；真正出站行情由 Go 全局拨号器统一管理。
        .no_proxy().build().expect("构造本机 API 客户端失败");
    tauri::Builder::default()
        .manage(WidgetState { client, base: format!("http://127.0.0.1:{port}/api"), pending: Mutex::new(HashMap::new()) })
        .invoke_handler(tauri::generate_handler![quotes, cancel_quote, watch_settings])
        .run(tauri::generate_context!())
        .expect("启动 QFLUX 小组件失败");
}
