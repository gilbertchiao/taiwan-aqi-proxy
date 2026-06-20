package storage

import (
	"path/filepath"
	"testing"

	"taiwan-aqi-proxy/internal/model"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("建立測試 Store 失敗: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestSaveAndLatest(t *testing.T) {
	store := newTestStore(t)

	aqi := 45
	pm := 12.5
	rec := model.AQIRecord{
		SiteID: "67", SiteName: "三重", AQI: &aqi, Status: "良好",
		PM25: &pm, PublishTime: "2026-06-20 11:00:00", RawJSON: "{}",
	}
	if err := store.Save(rec); err != nil {
		t.Fatalf("Save 失敗: %v", err)
	}

	got, err := store.LatestBySiteID("67")
	if err != nil {
		t.Fatalf("LatestBySiteID 失敗: %v", err)
	}
	if got == nil {
		t.Fatal("應查到資料,卻為 nil")
	}
	if got.AQI == nil || *got.AQI != 45 || got.Status != "良好" || got.SiteName != "三重" {
		t.Errorf("資料內容不符: %+v", got)
	}
}

func TestSave_KeepsHistoryAndUpserts(t *testing.T) {
	store := newTestStore(t)

	a1 := 45
	a2 := 50
	// 兩個不同整點 -> 應各自保留 (歷史資料)。
	_ = store.Save(model.AQIRecord{SiteID: "67", SiteName: "三重", AQI: &a1, PublishTime: "2026-06-20 11:00:00"})
	_ = store.Save(model.AQIRecord{SiteID: "67", SiteName: "三重", AQI: &a2, PublishTime: "2026-06-20 12:00:00"})

	latest, _ := store.LatestBySiteID("67")
	if latest == nil || latest.PublishTime != "2026-06-20 12:00:00" {
		t.Fatalf("最新一筆應為 12:00,實際 %+v", latest)
	}

	// 相同整點再次寫入 (UPSERT) -> 更新而非新增。
	a3 := 60
	if err := store.Save(model.AQIRecord{SiteID: "67", SiteName: "三重", AQI: &a3, PublishTime: "2026-06-20 12:00:00"}); err != nil {
		t.Fatalf("UPSERT 失敗: %v", err)
	}

	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM aqi_records WHERE site_id='67'").Scan(&count); err != nil {
		t.Fatalf("計數失敗: %v", err)
	}
	if count != 2 {
		t.Errorf("應為 2 筆 (11:00 與 12:00),實際 %d", count)
	}

	latest, _ = store.LatestBySiteID("67")
	if latest.AQI == nil || *latest.AQI != 60 {
		t.Errorf("UPSERT 後 12:00 的 AQI 應更新為 60,實際 %v", latest.AQI)
	}
}

// TestLatest_MalformedTimeNotPickedAsLatest 驗證:即使存在格式異常的
// publish_time (字典序可能排在前面),查詢「最新」仍會回傳格式正確的資料。
func TestLatest_MalformedTimeNotPickedAsLatest(t *testing.T) {
	store := newTestStore(t)

	valid := 45
	bad := 99
	_ = store.Save(model.AQIRecord{SiteID: "67", SiteName: "三重", AQI: &valid, PublishTime: "2026-06-20 11:00:00"})
	// "unknown-time" 開頭為 'u',字典序大於數字,若無防呆會被誤判為最新。
	_ = store.Save(model.AQIRecord{SiteID: "67", SiteName: "三重", AQI: &bad, PublishTime: "unknown-time"})

	latest, err := store.LatestBySiteID("67")
	if err != nil {
		t.Fatalf("查詢失敗: %v", err)
	}
	if latest.AQI == nil || *latest.AQI != 45 {
		t.Errorf("應回傳格式正確的最新資料 (aqi=45),實際 %+v", latest)
	}
}

func TestLatest_NotFound(t *testing.T) {
	store := newTestStore(t)
	got, err := store.LatestBySiteID("9999")
	if err != nil {
		t.Fatalf("非預期錯誤: %v", err)
	}
	if got != nil {
		t.Errorf("查無資料應回 nil,實際 %+v", got)
	}
}
