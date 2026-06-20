# ==========================================================
# 多階段建置:產出單一靜態執行檔,最終映像極小
# ==========================================================

# --- 階段一:建置 ---
# go.mod 要求 go 1.26,故 builder 使用對應版本。
FROM golang:1.26-alpine AS builder

WORKDIR /src

# 先複製 go.mod / go.sum 以善用 layer 快取 (相依未變時免重複下載)。
COPY go.mod go.sum ./
RUN go mod download

# 複製其餘原始碼。
COPY . .

# CGO_ENABLED=0 + 純 Go 的 SQLite driver = 完全靜態的單一執行檔。
# -ldflags 去除除錯資訊以縮小體積。
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/aqi-proxy ./cmd/aqi-proxy

# --- 階段二:執行 ---
# alpine 提供 shell 與 wget (供 HEALTHCHECK),體積仍小。
FROM alpine:3.24

# ca-certificates 供 HTTPS 連線環境部 API;時區資料已內嵌於執行檔。
RUN apk add --no-cache ca-certificates wget && \
    addgroup -S app && adduser -S app -G app

WORKDIR /app

# 從建置階段複製執行檔。
COPY --from=builder /out/aqi-proxy /app/aqi-proxy

# 建立資料與日誌目錄並設定擁有者 (供 volume 掛載與非 root 寫入)。
RUN mkdir -p /app/data /app/logs && chown -R app:app /app

USER app

EXPOSE 8000

# 健康檢查:輪詢 /health 端點。
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8000/health || exit 1

# 預設啟動 API 服務 (含內建排程器)。
ENTRYPOINT ["/app/aqi-proxy"]
CMD ["serve"]
