// Package storage 提供基於 SQLite 的本地儲存層。
//
// 採用純 Go 的 modernc.org/sqlite driver (免 CGo),
// 以維持「單一靜態執行檔」的部署優勢。
//
// 設計重點:
//   - 以 (site_id, publish_time) 作為唯一鍵,確保同一整點資料不重複,
//     同時保留歷史資料 (每整點一筆)。
//   - 寫入一律採 UPSERT,絕不執行 DELETE,確保上游異常時舊資料不會被清空。
//   - 啟用 WAL 模式以提升 API 讀取與 Worker 寫入的併發。
package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"taiwan-aqi-proxy/internal/model"

	_ "modernc.org/sqlite"
)

// schema 為資料表與索引定義。
const schema = `
CREATE TABLE IF NOT EXISTS aqi_records (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    site_id       TEXT    NOT NULL,
    site_name     TEXT    NOT NULL,
    aqi           INTEGER,
    status        TEXT,
    pm25          REAL,
    publish_time  TEXT    NOT NULL,
    raw_json      TEXT,
    fetched_at    TEXT    NOT NULL,
    UNIQUE(site_id, publish_time)
);
CREATE INDEX IF NOT EXISTS idx_site_publish ON aqi_records(site_id, publish_time DESC);
`

// Store 封裝資料庫連線與相關操作。
type Store struct {
	db *sql.DB
}

// New 開啟 (或建立) SQLite 資料庫,套用 PRAGMA 並初始化資料表。
func New(dbPath string) (*Store, error) {
	if dir := filepath.Dir(dbPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("建立資料庫目錄失敗: %w", err)
		}
	}

	// _pragma 以連線字串參數設定,確保每條連線都套用。
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(15000)&_pragma=foreign_keys(ON)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("開啟資料庫失敗: %w", err)
	}

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("連線資料庫失敗: %w", err)
	}

	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("初始化資料表失敗: %w", err)
	}

	return &Store{db: db}, nil
}

// Close 關閉資料庫連線。
func (s *Store) Close() error {
	return s.db.Close()
}

// Save 以 UPSERT 寫入單筆資料。
// 同一 (site_id, publish_time) 已存在時會更新該筆,不會新增重複資料;
// 整個操作只 INSERT/UPDATE,絕不 DELETE,確保舊資料不被清空。
func (s *Store) Save(rec model.AQIRecord) error {
	const query = `
INSERT INTO aqi_records (site_id, site_name, aqi, status, pm25, publish_time, raw_json, fetched_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(site_id, publish_time) DO UPDATE SET
    site_name  = excluded.site_name,
    aqi        = excluded.aqi,
    status     = excluded.status,
    pm25       = excluded.pm25,
    raw_json   = excluded.raw_json,
    fetched_at = excluded.fetched_at;
`
	fetchedAt := time.Now().Format(time.RFC3339)
	_, err := s.db.Exec(query,
		rec.SiteID, rec.SiteName, nullableInt(rec.AQI), nullableStr(rec.Status),
		nullableFloat(rec.PM25), rec.PublishTime, rec.RawJSON, fetchedAt,
	)
	if err != nil {
		return fmt.Errorf("寫入資料失敗 (site_id=%s, publish_time=%s): %w", rec.SiteID, rec.PublishTime, err)
	}
	return nil
}

// Ping 確認資料庫連線正常。
func (s *Store) Ping() error {
	return s.db.Ping()
}

// LatestBySiteID 取得指定測站編號的最新一筆資料 (依 publish_time 降冪)。
// 查無資料時回傳 (nil, nil)。
func (s *Store) LatestBySiteID(siteID string) (*model.AQIRecord, error) {
	// 排序防呆:publish_time 以 TEXT 儲存,正常格式 (YYYY-MM-DD HH:MM:SS)
	// 字典序即等於時間序。但若上游格式異常導致存入非標準字串,單純字串排序
	// 可能讓畸形值被誤判為「最新」。因此先以 GLOB 判斷是否為標準格式,
	// 讓格式正確者永遠優先,再依 publish_time、最後以 fetched_at 為次序鍵。
	const query = `
SELECT site_id, site_name, aqi, status, pm25, publish_time, COALESCE(raw_json, '')
FROM aqi_records
WHERE site_id = ?
ORDER BY
    (publish_time GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9] [0-9][0-9]:[0-9][0-9]:[0-9][0-9]') DESC,
    publish_time DESC,
    fetched_at DESC
LIMIT 1;`

	row := s.db.QueryRow(query, siteID)

	var (
		rec    model.AQIRecord
		aqi    sql.NullInt64
		status sql.NullString
		pm25   sql.NullFloat64
	)
	err := row.Scan(&rec.SiteID, &rec.SiteName, &aqi, &status, &pm25, &rec.PublishTime, &rec.RawJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查詢最新資料失敗 (site_id=%s): %w", siteID, err)
	}

	if aqi.Valid {
		v := int(aqi.Int64)
		rec.AQI = &v
	}
	if status.Valid {
		rec.Status = status.String
	}
	if pm25.Valid {
		rec.PM25 = &pm25.Float64
	}
	return &rec, nil
}

// --- nullable 轉換輔助函式:將指標 / 空值轉為 driver 可接受的型別 ---

func nullableInt(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullableFloat(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullableStr(v string) any {
	if v == "" {
		return nil
	}
	return v
}
