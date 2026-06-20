# 系統架構說明 (Architecture)

本文件說明 `taiwan-aqi-proxy` 的設計決策、模組職責與資料流。

## 1. 技術選型與理由

| 項目 | 選擇 | 理由 |
| --- | --- | --- |
| 語言 | Go 1.25 | 編譯為單一靜態執行檔,部署只需丟一個 binary,無需安裝 runtime,最貼合「現場看板/邊緣裝置」的部署情境。 |
| HTTP | 標準函式庫 `net/http` | Go 1.22 起 `ServeMux` 支援 `GET /api/v1/aqi/{siteid}` 路由樣式,無需引入 web 框架,維持輕量。 |
| 儲存 | SQLite (`modernc.org/sqlite`) | 純 Go 實作、免 CGo,可保留「完全靜態執行檔」優勢;單檔資料庫符合「輕量、免架設」需求,並能保留歷史資料。 |
| 排程 | 自製輕量排程器 | 只需「每小時某分鐘觸發」,自行實作數十行即可,避免額外相依;同時保留外部 Cron 選項。 |
| 日誌 | 標準函式庫 `log/slog` + 自製每日輪替 | 結構化日誌;每日輪替、舊檔壓縮、保留 30 天。 |

> 外部相依僅有 SQLite driver 一個,其餘皆為標準函式庫,維持專案輕量、好維護。

## 2. 模組職責

```
cmd/aqi-proxy        進入點,解析子命令 (serve / fetch / version),組裝相依、優雅關閉
internal/config      組態載入 (.env + 環境變數)
internal/logging     日誌設定 (slog + 每日輪替 + 壓縮)
internal/model       跨層共用資料結構與 API 回應模型
internal/storage     SQLite 儲存層 (UPSERT、查詢最新、保留歷史)
internal/moenv       環境部 API 用戶端 (HTTP 拉取 + JSON 解析)
internal/worker      更新核心:拉取 → 篩選目標測站 → 轉換 → 儲存 + 重試
internal/scheduler   每小時定時觸發器
internal/server      HTTP handlers (查詢、健康檢查、手動更新)
```

## 3. 資料流

```
                ┌──────────────────────────────────────────────┐
                │                  aqi-proxy serve                │
                │                                                 │
   每小時 :10   │   scheduler ──► worker.RunUpdate                │
  (或外部 Cron) │                    │                            │
                │                    ▼                            │
                │            moenv.FetchStations (HTTP GET)       │
                │                    │                            │
  環境部 API ◄──┼────────────────────┘                            │
  (aqx_p_432)   │                    │  篩選 siteid=67、轉換        │
                │                    ▼                            │
                │            storage.Save (UPSERT, 不刪舊資料)     │
                │                    │                            │
                │                    ▼                            │
                │              SQLite (data/aqi.db)               │
                │                    ▲                            │
                │                    │ 讀取最新一筆                 │
   終端看板 ────┼──► server: GET /api/v1/aqi/{siteid}             │
                │            (計算 is_stale 後回傳 JSON)           │
                └──────────────────────────────────────────────┘
```

## 4. 邊界條件與防呆設計

對應 PRD 第 3 節:

1. **上游 API 失敗 (Timeout / 5xx)**
   - `moenv` 將非 2xx 狀態一律視為失敗並回傳 error。
   - `worker.RunUpdate` 內含重試:最多 `MAX_RETRIES` 次,每次間隔 `RETRY_DELAY_SECONDS` (預設 180 秒);重試期間若收到關閉訊號 (context 取消) 會立即中止。
   - **絕不清空舊資料**:`storage.Save` 只執行 `INSERT ... ON CONFLICT DO UPDATE`,整個程式沒有任何 DELETE 路徑。

2. **資料過期 (Stale Data)**
   - API 讀取最新一筆後,以發佈時間與現在時間 (Asia/Taipei) 比較。
   - 距今超過 `STALE_THRESHOLD_HOURS` (預設 2 小時) 即在回應標記 `is_stale=true`。
   - 發佈時間無法解析時,保守地視為過期,避免顯示來路不明的舊資料。

3. **資料不重複又保留歷史**
   - 以 `(site_id, publish_time)` 為唯一鍵:同一整點重複拉取會更新該筆 (UPSERT),不同整點則各自保留為歷史。

## 5. 兩種排程部署模式

| 模式 | 設定 | 適用 |
| --- | --- | --- |
| 同進程排程 (預設) | `ENABLE_SCHEDULER=true` | 單一容器/單一程序即完成,最省事。 |
| 外部 Cron | `ENABLE_SCHEDULER=false` + crontab 呼叫 `aqi-proxy fetch` | 偏好由系統 Cron 統一管理排程、或希望更新與服務分離時。 |
