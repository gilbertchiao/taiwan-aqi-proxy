package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadDotEnv_MissingFileIsOK 驗證:.env 不存在屬正常情況,回傳 nil。
func TestLoadDotEnv_MissingFileIsOK(t *testing.T) {
	if err := loadDotEnv(filepath.Join(t.TempDir(), "nope.env")); err != nil {
		t.Fatalf("不存在的 .env 應回傳 nil,實際: %v", err)
	}
}

// TestLoad_ReadsEnvFile 驗證:.env 內容會被載入。
func TestLoad_ReadsEnvFile(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte("SITE_ID=99\nLOG_LEVEL=DEBUG\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	clearEnv(t, "SITE_ID", "LOG_LEVEL")

	cfg, err := Load(envPath)
	if err != nil {
		t.Fatalf("Load 失敗: %v", err)
	}
	if len(cfg.SiteIDs) != 1 || cfg.SiteIDs[0] != "99" {
		t.Errorf("SITE_ID 應為 [99],實際 %v", cfg.SiteIDs)
	}
	if cfg.LogLevel != "DEBUG" {
		t.Errorf("LOG_LEVEL 應為 DEBUG,實際 %s", cfg.LogLevel)
	}
}

// TestLoad_RealEnvOverridesDotEnv 驗證:真實環境變數優先於 .env。
func TestLoad_RealEnvOverridesDotEnv(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte("SITE_ID=99\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	clearEnv(t, "SITE_ID")
	t.Setenv("SITE_ID", "5") // 真實環境變數

	cfg, err := Load(envPath)
	if err != nil {
		t.Fatalf("Load 失敗: %v", err)
	}
	if len(cfg.SiteIDs) != 1 || cfg.SiteIDs[0] != "5" {
		t.Errorf("真實環境變數應優先 (SITE_ID=5),實際 %v", cfg.SiteIDs)
	}
}

// clearEnv 清除指定環境變數,並在測試結束後一併清除 (避免污染其他測試)。
func clearEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		_ = os.Unsetenv(k)
	}
	t.Cleanup(func() {
		for _, k := range keys {
			_ = os.Unsetenv(k)
		}
	})
}
