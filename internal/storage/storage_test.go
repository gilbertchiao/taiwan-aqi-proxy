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

	got, err := store.LatestBySiteName("三重")
	if err != nil {
		t.Fatalf("LatestBySiteName 失敗: %v", err)
	}
	if got == nil {
		t.Fatal("應查到資料,卻為 nil")
	}
	if got.AQI == nil || *got.AQI != 45 || got.Status != "良好" {
		t.Errorf("資料內容不符: %+v", got)
	}

	// 以測站編號查詢亦應成功。
	byID, err := store.LatestBySiteID("67")
	if err != nil || byID == nil {
		t.Fatalf("LatestBySiteID 失敗: %v, rec=%v", err, byID)
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

func TestLatest_NotFound(t *testing.T) {
	store := newTestStore(t)
	got, err := store.LatestBySiteName("不存在")
	if err != nil {
		t.Fatalf("非預期錯誤: %v", err)
	}
	if got != nil {
		t.Errorf("查無資料應回 nil,實際 %+v", got)
	}
}
