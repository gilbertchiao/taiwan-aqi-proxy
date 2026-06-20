// Package server 實作 API 服務模組 (HTTP handlers)。
//
// 採用標準函式庫 net/http 與 Go 1.22 的 ServeMux 路由樣式,不依賴 web 框架。
package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"taiwan-aqi-proxy/internal/config"
	"taiwan-aqi-proxy/internal/model"
)

// store 為伺服器所需的儲存層讀取介面。
type store interface {
	LatestBySiteID(siteID string) (*model.AQIRecord, error)
	Ping() error
}

// updater 為手動觸發更新所需的介面。
type updater interface {
	RunUpdate(ctx context.Context) error
}

// Server 持有相依元件並提供 HTTP handler。
type Server struct {
	store   store
	updater updater
	cfg     *config.Config
	log     *slog.Logger
}

// New 建立 Server。
func New(s store, u updater, cfg *config.Config, log *slog.Logger) *Server {
	return &Server{store: s, updater: u, cfg: cfg, log: log}
}

// Handler 組裝路由並回傳含日誌中介層的 http.Handler。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/aqi/{siteid}", s.handleAQI)
	mux.HandleFunc("POST /api/v1/refresh", s.handleRefresh)
	return s.withLogging(mux)
}

// handleAQI 處理 GET /api/v1/aqi/{siteid}。
// 以測站編號 (例如三重為 67) 查詢最新一筆資料。
func (s *Server) handleAQI(w http.ResponseWriter, r *http.Request) {
	siteID := strings.TrimSpace(r.PathValue("siteid"))

	rec, err := s.store.LatestBySiteID(siteID)
	if err != nil {
		s.log.Error("查詢資料發生錯誤", "siteid", siteID, "error", err)
		s.writeJSON(w, http.StatusInternalServerError, model.APIResponse{
			Success: false, Message: "內部錯誤,查詢資料失敗",
		})
		return
	}

	if rec == nil {
		s.writeJSON(w, http.StatusNotFound, model.APIResponse{
			Success: false,
			Message: "查無此測站資料: " + siteID,
		})
		return
	}

	data := model.AQIData{
		SiteName:    rec.SiteName,
		AQI:         rec.AQI,
		Status:      rec.Status,
		PM25:        rec.PM25,
		PublishTime: rec.PublishTime,
		IsStale:     s.isStale(rec.PublishTime),
	}
	s.writeJSON(w, http.StatusOK, model.APIResponse{Success: true, Data: &data})
}

// handleHealth 處理 GET /health,確認服務與資料庫狀態。
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(); err != nil {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "error", "database": "unreachable",
		})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// handleRefresh 處理 POST /api/v1/refresh,提供手動觸發更新 (需權杖)。
//
// 安全考量:僅在設定了 REFRESH_TOKEN 時啟用;呼叫需於標頭帶
// X-Refresh-Token 且與設定相符。未設定權杖則一律回 404,不洩漏端點存在。
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if s.cfg.RefreshToken == "" {
		http.NotFound(w, r)
		return
	}
	if !tokenEqual(r.Header.Get("X-Refresh-Token"), s.cfg.RefreshToken) {
		s.writeJSON(w, http.StatusUnauthorized, model.APIResponse{
			Success: false, Message: "未授權",
		})
		return
	}

	// 設定一個有上限的逾時,避免請求無限期阻塞。
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	if err := s.updater.RunUpdate(ctx); err != nil {
		// 僅在伺服器端記錄詳細錯誤,對外回傳靜態訊息,避免洩漏內部細節。
		s.log.Error("手動觸發更新失敗", "error", err)
		s.writeJSON(w, http.StatusBadGateway, model.APIResponse{
			Success: false, Message: "更新失敗,請查看伺服器日誌",
		})
		return
	}
	s.writeJSON(w, http.StatusOK, model.APIResponse{Success: true, Message: "更新完成"})
}

// isStale 判斷資料是否過期 (距今超過設定門檻)。
//
// 無法解析發佈時間時,保守地視為過期,避免顯示來路不明的舊資料。
func (s *Server) isStale(publishTime string) bool {
	loc := taipeiLocation()
	t, err := time.ParseInLocation("2006-01-02 15:04:05", publishTime, loc)
	if err != nil {
		return true
	}
	return time.Since(t) > s.cfg.StaleThreshold
}

// writeJSON 以 JSON 格式輸出回應。
func (s *Server) writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		s.log.Error("輸出 JSON 失敗", "error", err)
	}
}

// withLogging 為每個請求記錄方法、路徑、狀態碼與耗時。
func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.log.Info("HTTP 請求",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration", time.Since(start).String(),
			"remote", r.RemoteAddr,
		)
	})
}

// statusRecorder 包裝 ResponseWriter 以擷取實際回應狀態碼。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// tokenEqual 以定時 (constant-time) 方式比對權杖,防止計時攻擊。
//
// 先將兩端各自雜湊為固定長度 (32 bytes) 再比對,如此即使長度不同,
// 比對本身仍為固定長度、固定時間,不會洩漏長度資訊。
func tokenEqual(provided, expected string) bool {
	p := sha256.Sum256([]byte(provided))
	e := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(p[:], e[:]) == 1
}

// taipeiLocation 回傳 Asia/Taipei 時區;載入失敗時退回固定 +08:00。
func taipeiLocation() *time.Location {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}
