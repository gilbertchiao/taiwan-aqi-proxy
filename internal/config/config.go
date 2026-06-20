// Package config 負責載入與管理應用程式組態。
//
// 設計原則 (12-factor):所有可調整的參數皆透過環境變數提供,
// 並支援從專案根目錄的 .env 檔載入預設值。
// 真實的環境變數 (os 環境) 優先權高於 .env 檔,方便容器化部署時覆寫。
package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config 保存所有應用程式設定。
type Config struct {
	// === 環境部開放資料平台 ===
	MOENVAPIKey  string // API 金鑰 (MOENV_API_KEY)
	MOENVDataset string // 資料集代碼 (MOENV_DATASET),預設 aqx_p_432
	MOENVBaseURL string // API 基底網址 (MOENV_BASE_URL)

	// === 目標測站 ===
	SiteIDs  []string          // 測站編號清單 (SITE_ID,逗號分隔)
	AliasMap map[string]string // 英文別名 -> 中文名 (SITE_ALIASES)

	// === 排程 ===
	EnableScheduler bool // 是否啟用內建排程器 (ENABLE_SCHEDULER)
	ScheduleMinute  int  // 每小時觸發的分鐘數 (SCHEDULE_MINUTE)

	// === 重試 / 逾時 ===
	MaxRetries  int           // 上游失敗最大重試次數 (MAX_RETRIES)
	RetryDelay  time.Duration // 重試間隔 (RETRY_DELAY_SECONDS)
	HTTPTimeout time.Duration // HTTP 逾時 (HTTP_TIMEOUT_SECONDS)

	// === 資料過期判斷 ===
	StaleThreshold time.Duration // 過期門檻 (STALE_THRESHOLD_HOURS)

	// === 儲存 ===
	DatabasePath string // SQLite 檔案路徑 (DATABASE_PATH)

	// === 日誌 ===
	LogLevel string // 日誌等級 (LOG_LEVEL)
	LogDir   string // 日誌目錄 (LOG_DIR)

	// === API 服務 ===
	APIHost      string // 監聽位址 (API_HOST)
	APIPort      int    // 監聽埠號 (API_PORT)
	RefreshToken string // 手動觸發更新端點的權杖 (REFRESH_TOKEN),留空則停用
}

// Load 從 .env 檔 (若存在) 與環境變數載入設定。
//
// envPath 為 .env 檔路徑,傳入空字串時預設使用 ".env"。
// 回傳組裝完成的 Config;若必要欄位 (如 API 金鑰) 缺漏只會記錄警告,
// 由呼叫端決定是否中止,以利在「先啟動 API 提供舊資料、稍後再補金鑰」的情境運作。
func Load(envPath string) (*Config, error) {
	if envPath == "" {
		envPath = ".env"
	}
	loadDotEnv(envPath)

	cfg := &Config{
		MOENVAPIKey:  getEnv("MOENV_API_KEY", ""),
		MOENVDataset: getEnv("MOENV_DATASET", "aqx_p_432"),
		MOENVBaseURL: getEnv("MOENV_BASE_URL", "https://data.moenv.gov.tw/api/v2"),

		SiteIDs:  parseList(getEnv("SITE_ID", "67")),
		AliasMap: parseAliases(getEnv("SITE_ALIASES", "sanchong=三重")),

		EnableScheduler: getBool("ENABLE_SCHEDULER", true),
		ScheduleMinute:  getInt("SCHEDULE_MINUTE", 10),

		MaxRetries:  getInt("MAX_RETRIES", 3),
		RetryDelay:  time.Duration(getInt("RETRY_DELAY_SECONDS", 180)) * time.Second,
		HTTPTimeout: time.Duration(getInt("HTTP_TIMEOUT_SECONDS", 30)) * time.Second,

		StaleThreshold: time.Duration(getFloat("STALE_THRESHOLD_HOURS", 2.0) * float64(time.Hour)),

		DatabasePath: getEnv("DATABASE_PATH", "data/aqi.db"),

		LogLevel: getEnv("LOG_LEVEL", "INFO"),
		LogDir:   getEnv("LOG_DIR", "logs"),

		APIHost:      getEnv("API_HOST", "0.0.0.0"),
		APIPort:      getInt("API_PORT", 8000),
		RefreshToken: getEnv("REFRESH_TOKEN", ""),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// validate 檢查設定值的合法性。
func (c *Config) validate() error {
	if c.ScheduleMinute < 0 || c.ScheduleMinute > 59 {
		return fmt.Errorf("SCHEDULE_MINUTE 必須介於 0-59,實際為 %d", c.ScheduleMinute)
	}
	if len(c.SiteIDs) == 0 {
		return fmt.Errorf("SITE_ID 不可為空,請至少提供一個測站編號")
	}
	if c.MaxRetries < 0 {
		return fmt.Errorf("MAX_RETRIES 不可為負數")
	}
	return nil
}

// loadDotEnv 解析 .env 檔並將其中的鍵值設定到環境變數。
//
// 為維持輕量,此處自行實作極簡 .env 解析,不引入外部套件:
//   - 忽略空行與以 # 開頭的註解行。
//   - 以第一個 '=' 分隔鍵與值。
//   - 去除值前後空白與成對的引號。
//   - 僅在該環境變數尚未設定時才寫入 (真實環境變數優先)。
func loadDotEnv(path string) {
	file, err := os.Open(path)
	if err != nil {
		// .env 不存在屬正常情況 (例如正式環境直接用環境變數),不視為錯誤。
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`) // 去除成對引號
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, value)
		}
	}
}

// --- 取值輔助函式 ---

func getEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

func getFloat(key string, def float64) float64 {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f
		}
	}
	return def
}

func getBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			return b
		}
	}
	return def
}

// parseList 將逗號分隔字串解析為去除空白的字串清單。
func parseList(raw string) []string {
	var result []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			result = append(result, p)
		}
	}
	return result
}

// parseAliases 將 "slug=中文名,slug2=中文名2" 解析為 map (slug 一律轉小寫)。
func parseAliases(raw string) map[string]string {
	result := make(map[string]string)
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		slug, chinese, found := strings.Cut(pair, "=")
		if !found {
			continue
		}
		slug = strings.ToLower(strings.TrimSpace(slug))
		chinese = strings.TrimSpace(chinese)
		if slug != "" && chinese != "" {
			result[slug] = chinese
		}
	}
	return result
}
