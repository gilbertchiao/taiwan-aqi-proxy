# taiwan-aqi-proxy

[![CI](https://github.com/gilbertchiao/taiwan-aqi-proxy/actions/workflows/ci.yml/badge.svg)](https://github.com/gilbertchiao/taiwan-aqi-proxy/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/gilbertchiao/taiwan-aqi-proxy?sort=semver)](https://github.com/gilbertchiao/taiwan-aqi-proxy/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

輕量的空氣品質指標 (AQI) 中介代理服務。定時向**環境部開放資料平台**拉取 AQI 資料、寫入本地 SQLite,並提供 RESTful API 供終端設備 (如現場電子看板) 查詢最新數據。

- 避免終端設備頻繁打環境部 API 而觸發 Rate Limit / IP 封鎖。
- 上游異常時,終端仍可取得**最後一筆有效資料**並透過 `is_stale` 旗標判斷是否顯示,避免破版。

以 **Go** 開發,編譯為**單一靜態執行檔**,部署只需一個 binary,免安裝 runtime。

---

## 目錄結構

```
taiwan-aqi-proxy/
├── cmd/aqi-proxy/        # 進入點 (serve / fetch / version)
├── internal/
│   ├── config/           # 組態載入 (.env + 環境變數)
│   ├── logging/          # 日誌 (每日輪替 + 壓縮)
│   ├── model/            # 資料結構與 API 回應模型
│   ├── storage/          # SQLite 儲存層
│   ├── moenv/            # 環境部 API 用戶端
│   ├── worker/           # 更新核心 (拉取/篩選/儲存/重試)
│   ├── scheduler/        # 每小時定時觸發器
│   └── server/           # HTTP handlers
├── deploy/               # 部署設定 (systemd unit / timer / crontab 範例)
├── docs/                  # architecture.md (架構) / deployment-ubuntu.md (部署)
├── .env.example
├── Dockerfile
├── docker-compose.yml
├── Makefile
└── go.mod
```

詳細設計請見 [`docs/architecture.md`](docs/architecture.md)。

---

## 快速開始

### 前置作業:申請 API Key

至 [環境部開放資料平台](https://data.moenv.gov.tw/) 註冊會員,於會員中心取得 API Key。

### 方式一:Docker (推薦,最省事)

```bash
cp .env.example .env
# 編輯 .env,至少填入 MOENV_API_KEY
docker compose up -d --build
```

### 方式二:本機執行 (需 Go 1.25+)

```bash
cp .env.example .env        # 填入 MOENV_API_KEY
make build                  # 產出 bin/aqi-proxy
./bin/aqi-proxy serve       # 啟動 API 服務 (含內建排程器)
```

啟動後可直接測試 (以測站編號查詢,三重為 67):

```bash
curl http://localhost:8000/api/v1/aqi/67
```

### 方式三:Ubuntu systemd 服務

在 Ubuntu 上以 systemd 部署為開機自啟、異常自動重啟的系統服務:

```bash
make build
sudo cp bin/aqi-proxy /opt/taiwan-aqi-proxy/bin/
sudo cp deploy/taiwan-aqi-proxy.service /etc/systemd/system/
sudo systemctl enable --now taiwan-aqi-proxy
```

完整步驟 (建立使用者、權限、timer 模式、升級與移除) 見 [`docs/deployment-ubuntu.md`](docs/deployment-ubuntu.md)。

---

## 子命令

| 命令 | 說明 |
| --- | --- |
| `aqi-proxy serve` | 啟動 API 服務;依設定啟動內建排程器,並於啟動時先暖機更新一次 (預設)。 |
| `aqi-proxy fetch` | 執行單次資料更新後結束,適合搭配外部 Cron。 |
| `aqi-proxy version` | 顯示版本。 |

---

## API 端點

### `GET /api/v1/aqi/{siteid}`

`{siteid}` 為測站編號 (三重為 `67`)。測站對照表:<https://data.moenv.gov.tw/dataset/detail/AQX_P_07>

範例:`GET /api/v1/aqi/67`

成功回應 (`200`):

```json
{
  "success": true,
  "data": {
    "site_name": "三重",
    "aqi": 45,
    "status": "良好",
    "pm25": 12.5,
    "publish_time": "2026-06-20 11:00:00",
    "is_stale": false
  }
}
```

查無測站 (`404`):

```json
{ "success": false, "message": "查無此測站資料: 9999" }
```

> `is_stale`:當最新資料的發佈時間距今超過 `STALE_THRESHOLD_HOURS` (預設 2 小時) 時為 `true`,供終端判斷是否隱藏 AQI 顯示。

### `GET /health`

健康檢查;資料庫正常時回 `{"status":"ok"}`。

### `POST /api/v1/refresh`

手動觸發一次更新 (需於標頭帶 `X-Refresh-Token`,且與 `REFRESH_TOKEN` 相符)。
未設定 `REFRESH_TOKEN` 時此端點停用 (回 `404`)。

```bash
curl -X POST -H "X-Refresh-Token: <你的權杖>" http://localhost:8000/api/v1/refresh
```

---

## 環境變數

| 變數 | 預設 | 說明 |
| --- | --- | --- |
| `MOENV_API_KEY` | (必填) | 環境部開放資料平台 API Key |
| `MOENV_DATASET` | `aqx_p_432` | 資料集代碼 (AQI) |
| `MOENV_BASE_URL` | `https://data.moenv.gov.tw/api/v2` | API 基底網址 |
| `SITE_ID` | `67` | 測站編號;可逗號分隔多站 (例 `67,1`),亦為 API 查詢路徑參數 |
| `ENABLE_SCHEDULER` | `true` | 是否啟用內建排程器 |
| `SCHEDULE_MINUTE` | `10` | 每小時觸發的分鐘數 (0-59) |
| `MAX_RETRIES` | `3` | 上游失敗最大重試次數 |
| `RETRY_DELAY_SECONDS` | `180` | 重試間隔秒數 |
| `HTTP_TIMEOUT_SECONDS` | `30` | HTTP 逾時秒數 |
| `STALE_THRESHOLD_HOURS` | `2` | 資料過期門檻 (小時) |
| `DATABASE_PATH` | `data/aqi.db` | SQLite 檔案路徑 |
| `LOG_LEVEL` | `INFO` | 日誌等級 (DEBUG/INFO/WARN/ERROR) |
| `LOG_DIR` | `logs` | 日誌目錄 |
| `API_HOST` | `0.0.0.0` | 監聽位址 |
| `API_PORT` | `8000` | 監聽埠號 |
| `REFRESH_TOKEN` | (空) | 手動更新端點權杖;留空則停用該端點 |

---

## 排程的兩種方式

預設使用**內建排程器** (`ENABLE_SCHEDULER=true`),單一程序即完成定時更新。

若偏好由系統 Cron 管理,設 `ENABLE_SCHEDULER=false`,並參考 [`deploy/crontab.example`](deploy/crontab.example) 設定每小時呼叫 `aqi-proxy fetch`。

---

## 開發

```bash
make test     # 執行所有測試
make vet      # 靜態檢查
make fmt      # 格式化
make help     # 列出所有指令
```

---

## 異常處理重點

- **上游失敗會自動重試** (預設 3 次、間隔 3 分鐘),期間收到關閉訊號可立即中止。
- **絕不清空舊資料**:寫入只做 UPSERT,上游異常時本地仍保有最後有效資料。
- **資料過期標記**:超過門檻時數的資料會以 `is_stale=true` 標示,由終端決定是否顯示。
- **保留歷史**:以 `(site_id, publish_time)` 為唯一鍵,不同整點各自保留為歷史紀錄。

---

## 預編譯執行檔

每次發佈 (推送 `vX.Y.Z` tag) 會由 GitHub Actions 自動跨平台編譯,並附上各平台的單一執行檔與 SHA256 校驗碼,可於 [Releases](https://github.com/gilbertchiao/taiwan-aqi-proxy/releases) 頁面下載 (linux/darwin/windows,含 arm64)。

---

## 授權

本專案採用 [MIT License](LICENSE)。
