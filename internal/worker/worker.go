// Package worker 實作排程更新模組的核心邏輯:
// 拉取環境部資料 -> 篩選目標測站 -> 轉換 -> 寫入本地儲存,並涵蓋重試與防呆。
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"taiwan-aqi-proxy/internal/config"
	"taiwan-aqi-proxy/internal/model"
)

// fetcher 為資料來源介面,方便測試時以假資料替換。
type fetcher interface {
	FetchStations(ctx context.Context) ([]map[string]string, error)
}

// saver 為儲存層介面,方便測試。
type saver interface {
	Save(rec model.AQIRecord) error
}

// Worker 負責定期更新 AQI 資料。
type Worker struct {
	client fetcher
	store  saver
	cfg    *config.Config
	log    *slog.Logger
}

// New 建立 Worker。
func New(client fetcher, store saver, cfg *config.Config, log *slog.Logger) *Worker {
	return &Worker{client: client, store: store, cfg: cfg, log: log}
}

// RunUpdate 執行一次完整更新,內含重試機制。
//
// 防呆設計:
//   - 失敗時最多重試 cfg.MaxRetries 次,每次間隔 cfg.RetryDelay (預設 3 分鐘)。
//   - 重試期間若 context 被取消 (例如收到關閉訊號),立即中止並回傳。
//   - 即使全部失敗,也僅回傳錯誤、不會清空本地舊資料 (Save 只做 UPSERT)。
func (w *Worker) RunUpdate(ctx context.Context) error {
	attempts := w.cfg.MaxRetries + 1 // 首次嘗試 + 重試次數
	var lastErr error

	for attempt := 1; attempt <= attempts; attempt++ {
		saved, err := w.fetchAndStore(ctx)
		if err == nil {
			w.log.Info("AQI 資料更新成功", "saved_count", saved, "attempt", attempt)
			return nil
		}

		lastErr = err
		w.log.Error("AQI 資料更新失敗",
			"attempt", attempt, "max_attempts", attempts, "error", err)

		// 已是最後一次嘗試,不再等待。
		if attempt >= attempts {
			break
		}

		// 等待重試間隔,期間尊重 context 取消。
		w.log.Warn("將於稍後重試", "delay", w.cfg.RetryDelay.String())
		select {
		case <-ctx.Done():
			return fmt.Errorf("更新作業遭取消: %w", ctx.Err())
		case <-time.After(w.cfg.RetryDelay):
		}
	}

	return fmt.Errorf("AQI 資料更新最終失敗 (共嘗試 %d 次): %w", attempts, lastErr)
}

// fetchAndStore 執行單次「拉取 -> 篩選 -> 儲存」。
func (w *Worker) fetchAndStore(ctx context.Context) (int, error) {
	records, err := w.client.FetchStations(ctx)
	if err != nil {
		return 0, err
	}

	// 建立目標測站編號集合,加速篩選。
	targets := make(map[string]bool, len(w.cfg.SiteIDs))
	for _, id := range w.cfg.SiteIDs {
		targets[id] = true
	}

	found := make(map[string]bool)
	savedCount := 0

	for _, raw := range records {
		siteID := strings.TrimSpace(raw["siteid"])
		if !targets[siteID] {
			continue
		}

		rec := toRecord(siteID, raw)
		if err := w.store.Save(rec); err != nil {
			// 單筆寫入失敗只記錄並續處理其他測站,不中斷整體更新。
			w.log.Error("單筆資料寫入失敗", "site_id", siteID, "error", err)
			continue
		}
		found[siteID] = true
		savedCount++
		w.log.Debug("已寫入測站資料",
			"site_id", siteID, "site_name", rec.SiteName,
			"aqi", valueOrNil(rec.AQI), "publish_time", rec.PublishTime)
	}

	// 提醒:設定中要求的測站若未出現在回應中。
	for _, id := range w.cfg.SiteIDs {
		if !found[id] {
			w.log.Warn("回應中找不到指定測站資料", "site_id", id)
		}
	}

	if savedCount == 0 {
		return 0, fmt.Errorf("未取得任何指定測站 (%s) 的資料", strings.Join(w.cfg.SiteIDs, ","))
	}
	return savedCount, nil
}

// toRecord 將單筆原始資料轉換為內部 AQIRecord。
func toRecord(siteID string, raw map[string]string) model.AQIRecord {
	rawJSON, _ := json.Marshal(raw)
	return model.AQIRecord{
		SiteID:      siteID,
		SiteName:    strings.TrimSpace(raw["sitename"]),
		AQI:         parseInt(raw["aqi"]),
		Status:      strings.TrimSpace(raw["status"]),
		PM25:        parseFloat(raw["pm2.5"]),
		PublishTime: normalizePublishTime(raw["publishtime"]),
		RawJSON:     string(rawJSON),
	}
}

// parseInt 將字串轉為 *int;空值或無法解析 (例如 "ND"、"-") 時回傳 nil。
func parseInt(s string) *int {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	return &n
}

// parseFloat 將字串轉為 *float64;空值或無法解析時回傳 nil。
func parseFloat(s string) *float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &f
}

// publishTimeLayouts 為環境部 publishtime 可能出現的時間格式。
var publishTimeLayouts = []string{
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006/01/02 15:04:05",
	"2006/01/02 15:04",
}

// normalizePublishTime 將來源發佈時間標準化為 "YYYY-MM-DD HH:MM:SS"。
//
// 來源時間以台灣時間 (Asia/Taipei) 解讀;若所有格式皆無法解析,
// 則原樣回傳 (去除前後空白),確保至少保留原始資訊。
func normalizePublishTime(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	loc := taipeiLocation()
	for _, layout := range publishTimeLayouts {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t.Format("2006-01-02 15:04:05")
		}
	}
	return s
}

// taipeiLocation 回傳 Asia/Taipei 時區;載入失敗時退回固定 +08:00。
func taipeiLocation() *time.Location {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}

// valueOrNil 將 *int 轉為可記錄的值 (nil 時回傳字串 "nil")。
func valueOrNil(v *int) any {
	if v == nil {
		return "nil"
	}
	return *v
}
