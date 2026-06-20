package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"taiwan-aqi-proxy/internal/config"
	"taiwan-aqi-proxy/internal/model"
)

// --- 測試替身 ---

type fakeFetcher struct {
	records []map[string]string
	err     error
}

func (f *fakeFetcher) FetchStations(ctx context.Context) ([]map[string]string, error) {
	return f.records, f.err
}

type fakeSaver struct {
	saved []model.AQIRecord
	err   error
}

func (s *fakeSaver) Save(rec model.AQIRecord) error {
	if s.err != nil {
		return s.err
	}
	s.saved = append(s.saved, rec)
	return nil
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testConfig() *config.Config {
	return &config.Config{
		SiteIDs:    []string{"67"},
		MaxRetries: 1,
		RetryDelay: 10 * time.Millisecond,
	}
}

// --- parseInt / parseFloat ---

func TestParseInt(t *testing.T) {
	cases := map[string]*int{
		"45":  intPtr(45),
		"0":   intPtr(0),
		"":    nil,
		"ND":  nil,
		"-":   nil,
		" 12": intPtr(12),
	}
	for in, want := range cases {
		got := parseInt(in)
		if !eqIntPtr(got, want) {
			t.Errorf("parseInt(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseFloat(t *testing.T) {
	cases := map[string]*float64{
		"12.5": floatPtr(12.5),
		"0":    floatPtr(0),
		"":     nil,
		"ND":   nil,
	}
	for in, want := range cases {
		got := parseFloat(in)
		if (got == nil) != (want == nil) {
			t.Errorf("parseFloat(%q) nil 狀態不符: got %v want %v", in, got, want)
			continue
		}
		if got != nil && *got != *want {
			t.Errorf("parseFloat(%q) = %v, want %v", in, *got, *want)
		}
	}
}

// --- normalizePublishTime ---

func TestNormalizePublishTime(t *testing.T) {
	cases := map[string]string{
		"2026-06-20 11:00:00": "2026-06-20 11:00:00",
		"2026-06-20 11:00":    "2026-06-20 11:00:00",
		"2026/06/20 11:00":    "2026-06-20 11:00:00",
		"":                    "",
		"無法解析的字串":             "無法解析的字串",
	}
	for in, want := range cases {
		if got := normalizePublishTime(in); got != want {
			t.Errorf("normalizePublishTime(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- fetchAndStore:成功篩選目標測站 ---

func TestFetchAndStore_FiltersTargetSite(t *testing.T) {
	fetcher := &fakeFetcher{records: []map[string]string{
		{"siteid": "67", "sitename": "三重", "aqi": "45", "status": "良好", "pm2.5": "12.5", "publishtime": "2026-06-20 11:00:00"},
		{"siteid": "1", "sitename": "基隆", "aqi": "30", "status": "良好", "publishtime": "2026-06-20 11:00:00"},
	}}
	saver := &fakeSaver{}
	w := New(fetcher, saver, testConfig(), quietLogger())

	count, err := w.fetchAndStore(context.Background())
	if err != nil {
		t.Fatalf("非預期錯誤: %v", err)
	}
	if count != 1 {
		t.Fatalf("應只儲存 1 筆 (三重),實際 %d", count)
	}
	got := saver.saved[0]
	if got.SiteName != "三重" || got.AQI == nil || *got.AQI != 45 {
		t.Errorf("儲存內容不符: %+v", got)
	}
	if got.PM25 == nil || *got.PM25 != 12.5 {
		t.Errorf("PM2.5 不符: %+v", got.PM25)
	}
}

// --- fetchAndStore:目標測站不存在時應回錯 ---

func TestFetchAndStore_NoTargetFound(t *testing.T) {
	fetcher := &fakeFetcher{records: []map[string]string{
		{"siteid": "1", "sitename": "基隆", "aqi": "30", "publishtime": "2026-06-20 11:00:00"},
	}}
	w := New(fetcher, &fakeSaver{}, testConfig(), quietLogger())

	if _, err := w.fetchAndStore(context.Background()); err == nil {
		t.Fatal("找不到目標測站時應回傳錯誤")
	}
}

// --- RunUpdate:重試後成功 ---

func TestRunUpdate_RetryThenFail(t *testing.T) {
	fetcher := &fakeFetcher{err: errors.New("模擬上游逾時")}
	w := New(fetcher, &fakeSaver{}, testConfig(), quietLogger())

	err := w.RunUpdate(context.Background())
	if err == nil {
		t.Fatal("上游持續失敗時 RunUpdate 應回傳錯誤")
	}
}

// --- RunUpdate:context 取消應提前中止 ---

func TestRunUpdate_ContextCancel(t *testing.T) {
	fetcher := &fakeFetcher{err: errors.New("模擬失敗")}
	cfg := testConfig()
	cfg.MaxRetries = 5
	cfg.RetryDelay = 10 * time.Second // 故意設長,驗證取消能立即中止
	w := New(fetcher, &fakeSaver{}, cfg, quietLogger())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	start := time.Now()
	if err := w.RunUpdate(ctx); err == nil {
		t.Fatal("context 取消後應回傳錯誤")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("取消後應立即返回,實際耗時 %v", elapsed)
	}
}

// --- 輔助函式 ---

func intPtr(v int) *int           { return &v }
func floatPtr(v float64) *float64 { return &v }
func eqIntPtr(a, b *int) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || *a == *b
}
