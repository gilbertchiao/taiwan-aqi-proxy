# Ubuntu 部署指南 (systemd)

本文件說明如何在 Ubuntu 上以 systemd 將 `taiwan-aqi-proxy` 部署為系統服務,
開機自動啟動、異常自動重啟。

提供兩種模式,擇一即可:

| 模式 | 組成 | 說明 |
| --- | --- | --- |
| **A. 內建排程 (推薦)** | `taiwan-aqi-proxy.service` | 單一服務同時提供 API 與每小時更新。最簡單。 |
| **B. systemd timer** | `taiwan-aqi-proxy.service` (API,關閉內建排程) + `*-fetch.service` + `*-fetch.timer` | 由 systemd timer 管理更新排程。偏好集中管理排程時採用。 |

---

## 1. 前置:建立使用者與目錄

```bash
# 建立專用系統使用者 (無登入權限),提升安全性
sudo useradd --system --no-create-home --shell /usr/sbin/nologin aqi

# 建立部署目錄
sudo mkdir -p /opt/taiwan-aqi-proxy/bin
sudo mkdir -p /opt/taiwan-aqi-proxy/data
sudo mkdir -p /opt/taiwan-aqi-proxy/logs
```

## 2. 編譯並佈署執行檔

在專案目錄編譯出單一靜態執行檔,複製到部署目錄:

```bash
make build                                   # 產出 bin/aqi-proxy
sudo cp bin/aqi-proxy /opt/taiwan-aqi-proxy/bin/
```

> 若部署機器沒有 Go,可在開發機 `make build` 後,只把 `bin/aqi-proxy`
> 這一個檔案複製過去即可 (純靜態執行檔,免安裝任何 runtime)。

## 3. 設定環境變數

```bash
sudo cp .env.example /opt/taiwan-aqi-proxy/.env
sudo nano /opt/taiwan-aqi-proxy/.env          # 至少填入 MOENV_API_KEY
```

- **模式 A**:確認 `.env` 內 `ENABLE_SCHEDULER=true` (預設值)。
- **模式 B**:將 `.env` 內 `ENABLE_SCHEDULER=false`。

`.env` 含 API 金鑰,請限制權限:

```bash
sudo chown -R aqi:aqi /opt/taiwan-aqi-proxy
sudo chmod 640 /opt/taiwan-aqi-proxy/.env
```

---

## 4A. 模式 A:內建排程 (推薦)

```bash
sudo cp deploy/taiwan-aqi-proxy.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now taiwan-aqi-proxy
```

檢查狀態與日誌:

```bash
systemctl status taiwan-aqi-proxy
journalctl -u taiwan-aqi-proxy -f
curl http://localhost:8000/api/v1/aqi/67
```

---

## 4B. 模式 B:systemd timer

先安裝 API 服務 (記得 `.env` 已設 `ENABLE_SCHEDULER=false`):

```bash
sudo cp deploy/taiwan-aqi-proxy.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now taiwan-aqi-proxy
```

再安裝更新用的 oneshot 服務與 timer:

```bash
sudo cp deploy/taiwan-aqi-proxy-fetch.service /etc/systemd/system/
sudo cp deploy/taiwan-aqi-proxy-fetch.timer   /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now taiwan-aqi-proxy-fetch.timer
```

驗證 timer 與手動觸發一次更新:

```bash
systemctl list-timers taiwan-aqi-proxy-fetch.timer
sudo systemctl start taiwan-aqi-proxy-fetch.service   # 立即跑一次
journalctl -u taiwan-aqi-proxy-fetch -n 30
```

---

## 5. 升級

```bash
make build
sudo systemctl stop taiwan-aqi-proxy
sudo cp bin/aqi-proxy /opt/taiwan-aqi-proxy/bin/
sudo systemctl start taiwan-aqi-proxy
```

## 6. 常用維運指令

```bash
sudo systemctl restart taiwan-aqi-proxy      # 重啟
sudo systemctl stop taiwan-aqi-proxy         # 停止
sudo systemctl disable taiwan-aqi-proxy      # 取消開機啟動
journalctl -u taiwan-aqi-proxy --since today # 查看今日日誌
```

## 7. 移除

```bash
sudo systemctl disable --now taiwan-aqi-proxy
# 模式 B 另需:
sudo systemctl disable --now taiwan-aqi-proxy-fetch.timer
sudo rm /etc/systemd/system/taiwan-aqi-proxy*.service /etc/systemd/system/taiwan-aqi-proxy*.timer
sudo systemctl daemon-reload
sudo rm -rf /opt/taiwan-aqi-proxy        # 含資料庫,請先備份
sudo userdel aqi
```

---

## 安全性說明

`*.service` 已套用 systemd 沙箱強化:`NoNewPrivileges`、`ProtectSystem=strict`、
`ProtectHome`,並以 `ReadWritePaths` 僅開放 `data/` 與 `logs/` 可寫,其餘檔案系統唯讀。
服務以無登入權限的 `aqi` 系統使用者執行,降低風險。
