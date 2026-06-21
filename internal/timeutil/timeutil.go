// Package timeutil 提供全專案統一的時區處理。
//
// 設計理念:
//
//	本服務所有面向使用者的時間 (資料發佈時間、抓取時間戳記、日誌日界線、
//	排程觸發點) 一律以台灣時間 (Asia/Taipei) 為準,「不」依賴執行環境的
//	系統時區。如此即使容器或主機未設定 TZ (Go 預設會落在 UTC),程式行為
//	仍與台灣一致,避免時間戳記、日誌切割日界線出現 8 小時偏移。
//
//	執行檔已於 main 套件內嵌 time/tzdata,故即使在不含系統 tzdata 的精簡
//	映像 (alpine/scratch) 中,LoadLocation("Asia/Taipei") 仍可成功。
package timeutil

import "time"

// taipei 為快取的 Asia/Taipei 時區,於套件載入時解析一次後重複使用,
// 避免每次取用都重新載入。
var taipei = mustTaipei()

// mustTaipei 載入 Asia/Taipei 時區;萬一載入失敗 (理論上不會,因已內嵌
// tzdata),則退回固定 +08:00 (CST) 作為保底,確保服務不致因時區問題中斷。
func mustTaipei() *time.Location {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}

// Location 回傳 Asia/Taipei 時區 (載入失敗時為固定 +08:00)。
func Location() *time.Location {
	return taipei
}

// Now 回傳「台灣時區」的目前時間。
//
// 等同 time.Now().In(Location()),但語意更明確:凡是需要「現在是台灣幾點」
// 的場合 (時間戳記、日誌輪替、排程計算) 都應使用本函式,而非直接 time.Now()。
func Now() time.Time {
	return time.Now().In(taipei)
}
