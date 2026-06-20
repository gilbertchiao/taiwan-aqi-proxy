// Package logging 提供應用程式日誌設定。
//
// 採用標準函式庫 log/slog,並依偏好實作:
//   - 每日輪替 (依日期切換檔案 app_YYYY-MM-DD.log)。
//   - 舊檔自動壓縮為 .gz。
//   - 逾保留天數的舊壓縮檔自動清除 (預設 30 天)。
//   - 同時輸出至主控台 (stderr) 與檔案。
package logging

import (
	"compress/gzip"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// retentionDays 為壓縮日誌的保留天數。
const retentionDays = 30

// Setup 初始化並回傳 slog.Logger,以及一個用於釋放資源的 closer。
func Setup(logDir, level string) (*slog.Logger, io.Closer, error) {
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, nil, err
	}

	rotator := newDailyRotator(logDir)
	writer := io.MultiWriter(os.Stderr, rotator)

	handler := slog.NewTextHandler(writer, &slog.HandlerOptions{
		Level: parseLevel(level),
	})
	logger := slog.New(handler)
	return logger, rotator, nil
}

// parseLevel 將字串日誌等級轉為 slog.Level。
func parseLevel(level string) slog.Level {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// dailyRotator 為每日輪替的檔案寫入器,實作 io.Writer 與 io.Closer。
type dailyRotator struct {
	mu      sync.Mutex
	dir     string
	curDate string   // 目前檔案對應的日期 (YYYY-MM-DD)
	file    *os.File // 目前開啟的檔案
}

// newDailyRotator 建立輪替器。
func newDailyRotator(dir string) *dailyRotator {
	return &dailyRotator{dir: dir}
}

// Write 實作 io.Writer;每次寫入前確認日期是否變更,必要時輪替。
func (d *dailyRotator) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	today := time.Now().Format("2006-01-02")
	if d.file == nil || d.curDate != today {
		if err := d.rotate(today); err != nil {
			return 0, err
		}
	}
	return d.file.Write(p)
}

// rotate 切換到指定日期的新檔案,並對前一天的檔案進行壓縮與清理。
func (d *dailyRotator) rotate(today string) error {
	prevDate := d.curDate
	if d.file != nil {
		_ = d.file.Close()
		d.file = nil
		// 壓縮前一天的檔案 (非阻塞,失敗僅忽略不影響主流程)。
		if prevDate != "" {
			go compressFile(filepath.Join(d.dir, "app_"+prevDate+".log"))
		}
	}

	path := filepath.Join(d.dir, "app_"+today+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	d.file = f
	d.curDate = today

	// 清除逾期的壓縮檔。
	go purgeOldLogs(d.dir, retentionDays)
	return nil
}

// Close 關閉目前開啟的檔案。
func (d *dailyRotator) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.file != nil {
		err := d.file.Close()
		d.file = nil
		return err
	}
	return nil
}

// compressFile 將指定檔案壓縮為 .gz 並刪除原檔。
func compressFile(path string) {
	if _, err := os.Stat(path); err != nil {
		return // 檔案不存在
	}
	src, err := os.Open(path)
	if err != nil {
		return
	}
	defer src.Close()

	dst, err := os.Create(path + ".gz")
	if err != nil {
		return
	}
	defer dst.Close()

	gz := gzip.NewWriter(dst)
	if _, err := io.Copy(gz, src); err != nil {
		_ = gz.Close()
		return
	}
	if err := gz.Close(); err != nil {
		return
	}
	// 壓縮成功後刪除原始檔。
	_ = os.Remove(path)
}

// purgeOldLogs 刪除超過保留天數的 .gz 壓縮日誌。
func purgeOldLogs(dir string, days int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log.gz") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}
