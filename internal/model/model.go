// Package model 定義跨層共用的資料結構。
package model

// AQIRecord 為單筆空氣品質資料的內部表示 (儲存層與業務邏輯共用)。
//
// 可能缺值的數值欄位 (AQI、PM2.5) 使用指標型別,
// 以區分「值為 0」與「無資料 (null)」兩種情況。
type AQIRecord struct {
	SiteID      string   // 測站編號
	SiteName    string   // 測站中文名稱
	AQI         *int     // 空氣品質指標,無資料時為 nil
	Status      string   // 狀態文字 (例如「良好」),無資料時為空字串
	PM25        *float64 // PM2.5 濃度,無資料時為 nil
	PublishTime string   // 來源發佈時間,標準化為 "YYYY-MM-DD HH:MM:SS"
	RawJSON     string   // 原始資料 JSON,保留備查
}

// AQIData 為 API 回傳的資料內容,對應 PRD 指定的 data 欄位格式。
type AQIData struct {
	SiteName    string   `json:"site_name"`
	AQI         *int     `json:"aqi"`
	Status      string   `json:"status"`
	PM25        *float64 `json:"pm25"`
	PublishTime string   `json:"publish_time"`
	IsStale     bool     `json:"is_stale"`
}

// APIResponse 為 API 標準回應外層結構。
type APIResponse struct {
	Success bool     `json:"success"`
	Data    *AQIData `json:"data,omitempty"`
	Message string   `json:"message,omitempty"`
}
