// Package server 實作 API 服務模組 (HTTP handlers)。
//
// 採用標準函式庫 net/http 與 Go 1.22 的 ServeMux 路由樣式,不依賴 web 框架。
package server

import (
	"context"
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
	LatestBySiteName(siteName string) (*model.AQIRecord, error)
	Ping() error
}

// updater 為手動觸發更新所需的介面。
type updater interface {
	RunUpdate(ctx context.Context) error
}

// Server 持有相依元件並提供 HTTP handler。
type Server struct {
	store store
	updom updater
	cfg   *config.Config
	log   *slog.Logger
}

// New 建立 Server。
func New(s store, u updater, cfg *config.Config, log *slog.Logger) *Server {
	return &Server{store: s, updom: u, cfg: cfg, log: log}
}

// Handler 組裝路由並回傳含日誌中介層的 http.Handler。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/aqi/{sitename}", s.handleAQI)
	mux.HandleFunc("POST /api/v1/refresh", s.handleRefresh)
	return s.withLogging(mux)
}

// handleAQI 處理 GET /api/v1/aqi/{sitename}。
func (s *Server) handleAQI(w http.ResponseWriter, r *http.Request) {
	siteName := r.PathValue("sitename")
	resolved := s.resolveSiteName(siteName)

	// 先以中文名稱查詢;查無再嘗試以測站編號查詢 (容許直接帶 siteid)。
	rec, err := s.store.LatestBySiteName(resolved)
	if err != nil {
		s.log.Error("查詢資料發生錯誤", "sitename", siteName, "error", err)
		s.writeJSON(w, http.StatusInternalServerError, model.APIResponse{
			Success: false, Message: "內部錯誤,查詢資料失敗",
		})
		return
	}
	if rec == nil {
		if byID, e := s.store.LatestBySiteID(resolved); e == nil && byID != nil {
			rec = byID
		}
	}

	if rec == nil {
		s.writeJSON(w, http.StatusNotFound, model.APIResponse{
			Success: false,
			Message: "查無此測站資料: " + siteName,
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
	if r.Header.Get("X-Refresh-Token") != s.cfg.RefreshToken {
		s.writeJSON(w, http.StatusUnauthorized, model.APIResponse{
			Success: false, Message: "未授權",
		})
		return
	}

	// 設定一個有上限的逾時,避免請求無限期阻塞。
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	if err := s.updom.RunUpdate(ctx); err != nil {
		s.writeJSON(w, http.StatusBadGateway, model.APIResponse{
			Success: false, Message: "更新失敗: " + err.Error(),
		})
		return
	}
	s.writeJSON(w, http.StatusOK, model.APIResponse{Success: true, Message: "更新完成"})
}

// resolveSiteName 將路徑參數轉為實際的測站中文名稱。
//
// 規則:先以小寫比對英文別名表 (例如 sanchong -> 三重);
// 若無對應,則視為已是中文名稱 (或測站編號) 直接回傳。
func (s *Server) resolveSiteName(input string) string {
	key := strings.ToLower(strings.TrimSpace(input))
	if chinese, ok := s.cfg.AliasMap[key]; ok {
		return chinese
	}
	return strings.TrimSpace(input)
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

// taipeiLocation 回傳 Asia/Taipei 時區;載入失敗時退回固定 +08:00。
func taipeiLocation() *time.Location {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}
