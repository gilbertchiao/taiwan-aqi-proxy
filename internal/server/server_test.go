package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"taiwan-aqi-proxy/internal/config"
	"taiwan-aqi-proxy/internal/model"
)

// --- 測試替身 ---

type fakeStore struct {
	byName map[string]*model.AQIRecord
	byID   map[string]*model.AQIRecord
}

func (f *fakeStore) LatestBySiteName(name string) (*model.AQIRecord, error) {
	return f.byName[name], nil
}
func (f *fakeStore) LatestBySiteID(id string) (*model.AQIRecord, error) {
	return f.byID[id], nil
}
func (f *fakeStore) Ping() error { return nil }

type fakeUpdater struct{}

func (fakeUpdater) RunUpdate(ctx context.Context) error { return nil }

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testServer(rec *model.AQIRecord) http.Handler {
	cfg := &config.Config{
		AliasMap:       map[string]string{"sanchong": "三重"},
		StaleThreshold: 2 * time.Hour,
	}
	fs := &fakeStore{
		byName: map[string]*model.AQIRecord{},
		byID:   map[string]*model.AQIRecord{},
	}
	if rec != nil {
		fs.byName[rec.SiteName] = rec
		fs.byID[rec.SiteID] = rec
	}
	return New(fs, fakeUpdater{}, cfg, quietLogger()).Handler()
}

func recentRecord() *model.AQIRecord {
	aqi := 45
	pm := 12.5
	// 以「現在時間」為發佈時間,確保非過期。
	now := time.Now().In(taipeiLocation()).Format("2006-01-02 15:04:05")
	return &model.AQIRecord{
		SiteID: "67", SiteName: "三重", AQI: &aqi, Status: "良好",
		PM25: &pm, PublishTime: now,
	}
}

// --- 測試:以中文名稱查詢 ---

func TestHandleAQI_ByChineseName(t *testing.T) {
	h := testServer(recentRecord())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/aqi/三重", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("狀態碼應為 200,實際 %d", rec.Code)
	}
	var resp model.APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析回應失敗: %v", err)
	}
	if !resp.Success || resp.Data == nil {
		t.Fatalf("回應應成功且帶資料: %+v", resp)
	}
	if resp.Data.SiteName != "三重" || resp.Data.IsStale {
		t.Errorf("資料不符或誤判過期: %+v", resp.Data)
	}
}

// --- 測試:以英文別名查詢 ---

func TestHandleAQI_ByAlias(t *testing.T) {
	h := testServer(recentRecord())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/aqi/sanchong", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("以別名查詢狀態碼應為 200,實際 %d", rec.Code)
	}
}

// --- 測試:過期資料應標記 is_stale=true ---

func TestHandleAQI_StaleData(t *testing.T) {
	aqi := 45
	old := time.Now().Add(-3 * time.Hour).In(taipeiLocation()).Format("2006-01-02 15:04:05")
	stale := &model.AQIRecord{SiteID: "67", SiteName: "三重", AQI: &aqi, PublishTime: old}

	h := testServer(stale)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/aqi/三重", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var resp model.APIResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Data == nil || !resp.Data.IsStale {
		t.Errorf("超過 2 小時的資料應標記 is_stale=true: %+v", resp.Data)
	}
}

// --- 測試:查無資料回 404 ---

func TestHandleAQI_NotFound(t *testing.T) {
	h := testServer(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/aqi/不存在", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("查無資料應回 404,實際 %d", rec.Code)
	}
}

// --- 測試:health 端點 ---

func TestHandleHealth(t *testing.T) {
	h := testServer(nil)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health 應回 200,實際 %d", rec.Code)
	}
}

// --- 測試:設定權杖後,正確/錯誤權杖的行為 ---

func TestHandleRefresh_TokenAuth(t *testing.T) {
	cfg := &config.Config{RefreshToken: "secret123"}
	fs := &fakeStore{byName: map[string]*model.AQIRecord{}, byID: map[string]*model.AQIRecord{}}
	h := New(fs, fakeUpdater{}, cfg, quietLogger()).Handler()

	// 錯誤權杖 -> 401
	req := httptest.NewRequest(http.MethodPost, "/api/v1/refresh", nil)
	req.Header.Set("X-Refresh-Token", "wrong")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("錯誤權杖應回 401,實際 %d", rec.Code)
	}

	// 正確權杖 -> 200
	req = httptest.NewRequest(http.MethodPost, "/api/v1/refresh", nil)
	req.Header.Set("X-Refresh-Token", "secret123")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("正確權杖應回 200,實際 %d", rec.Code)
	}
}

func TestTokenEqual(t *testing.T) {
	if !tokenEqual("abc", "abc") {
		t.Error("相同權杖應相等")
	}
	if tokenEqual("abc", "abcd") {
		t.Error("不同長度權杖不應相等")
	}
	if tokenEqual("abc", "xyz") {
		t.Error("不同內容權杖不應相等")
	}
}

// --- 測試:未設定權杖時 refresh 端點回 404 ---

func TestHandleRefresh_DisabledWhenNoToken(t *testing.T) {
	h := testServer(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/refresh", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未設定權杖時 refresh 應回 404,實際 %d", rec.Code)
	}
}
