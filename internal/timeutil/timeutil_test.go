package timeutil

import (
	"testing"
	"time"
)

// TestLocationIsTaipei 確認 Location 回傳的時區固定為 +08:00,
// 不受測試執行環境的系統時區影響。
func TestLocationIsTaipei(t *testing.T) {
	loc := Location()
	if loc == nil {
		t.Fatal("Location 不應回傳 nil")
	}

	// 以一個固定時刻檢查偏移量是否為 +8 小時 (台灣全年無日光節約時間)。
	ref := time.Date(2026, 6, 21, 12, 0, 0, 0, loc)
	_, offset := ref.Zone()
	if want := 8 * 3600; offset != want {
		t.Errorf("時區偏移 = %d 秒,預期 %d 秒 (+08:00)", offset, want)
	}
}

// TestNowUsesTaipei 確認 Now() 回傳的時間掛在台灣時區下,
// 即使系統 TZ 為 UTC 也應如此。
func TestNowUsesTaipei(t *testing.T) {
	_, offset := Now().Zone()
	if want := 8 * 3600; offset != want {
		t.Errorf("Now() 時區偏移 = %d 秒,預期 %d 秒 (+08:00)", offset, want)
	}
}
