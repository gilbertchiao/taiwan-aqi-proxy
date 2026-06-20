package moenv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestRedactRemovesAPIKey 驗證錯誤訊息不會夾帶 API 金鑰。
func TestRedactRemovesAPIKey(t *testing.T) {
	c := New("super-secret-key", "data.json", "http://127.0.0.1:1/api", time.Second)
	s := c.redact("dial tcp 127.0.0.1:1: ...?api_key=super-secret-key&format=JSON")
	if strings.Contains(s, "super-secret-key") {
		t.Fatalf("redact 後仍含金鑰: %s", s)
	}
	if !strings.Contains(s, "REDACTED") {
		t.Fatalf("redact 後應有遮蔽標記: %s", s)
	}
}

// TestFetchStations_ErrorDoesNotLeakKey 驗證連線失敗時回傳的錯誤不含金鑰。
func TestFetchStations_ErrorDoesNotLeakKey(t *testing.T) {
	// 指向一個必定無法連線的位址,觸發 *url.Error。
	c := New("leak-me-please", "data.json", "http://127.0.0.1:0", time.Second)
	_, err := c.FetchStations(context.Background())
	if err == nil {
		t.Fatal("應回傳連線錯誤")
	}
	if strings.Contains(err.Error(), "leak-me-please") {
		t.Fatalf("錯誤訊息洩漏了 API 金鑰: %s", err.Error())
	}
}

// TestFetchStations_ParsesRecords 驗證正常解析流程。
func TestFetchStations_ParsesRecords(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total":"1","records":[{"siteid":"67","sitename":"三重","aqi":"45"}]}`))
	}))
	defer srv.Close()

	c := New("k", "dataset", srv.URL, 5*time.Second)
	records, err := c.FetchStations(context.Background())
	if err != nil {
		t.Fatalf("非預期錯誤: %v", err)
	}
	if len(records) != 1 || records[0]["sitename"] != "三重" {
		t.Fatalf("解析結果不符: %+v", records)
	}
}
