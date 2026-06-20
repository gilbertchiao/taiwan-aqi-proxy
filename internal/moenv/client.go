// Package moenv 封裝對「環境部開放資料平台」的 API 存取。
//
// 目標資料集:空氣品質指標 (AQI),資料集代碼預設 aqx_p_432。
// 使用 v2 介面: GET {base}/{dataset}?api_key=...&language=zh&limit=...&format=JSON
package moenv

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client 為環境部 API 用戶端。
type Client struct {
	apiKey     string
	dataset    string
	baseURL    string
	httpClient *http.Client
}

// apiResponse 對應 v2 API 的「物件包裹」JSON 結構。
// records 內每筆為 欄位名 -> 字串值 的對應。
//
// 注意:環境部 v2 API (帶 api_key 並指定 format=JSON) 實測會直接回傳頂層為
// 測站陣列的 JSON,而非本結構。為相容兩種格式,解析時優先嘗試裸陣列,
// 失敗再退回此包裹結構,詳見 parseRecords。
type apiResponse struct {
	Total   string              `json:"total"`
	Records []map[string]string `json:"records"`
}

// New 建立一個環境部 API 用戶端。
func New(apiKey, dataset, baseURL string, timeout time.Duration) *Client {
	return &Client{
		apiKey:  apiKey,
		dataset: dataset,
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// FetchStations 向環境部拉取所有測站的最新 AQI 原始資料。
//
// 回傳每筆測站的「欄位名 -> 值」對應;呼叫端 (Worker) 再依測站編號篩選與轉換。
// 任何網路錯誤、非 2xx 狀態碼或 JSON 解析失敗都會回傳 error。
func (c *Client) FetchStations(ctx context.Context) ([]map[string]string, error) {
	endpoint, err := c.buildURL()
	if err != nil {
		return nil, fmt.Errorf("組裝請求網址失敗: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("建立 HTTP 請求失敗: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// 注意:net/http 的 *url.Error 會在錯誤訊息中夾帶完整請求網址,
		// 而 api_key 位於查詢字串,故務必先 redact 再回傳/記錄,避免金鑰外洩到日誌。
		return nil, fmt.Errorf("呼叫環境部 API 失敗: %s", c.redact(err.Error()))
	}
	defer resp.Body.Close()

	// 限制讀取大小 (10MB),避免異常回應耗盡記憶體。
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("讀取回應內容失敗: %s", c.redact(err.Error()))
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 將上游錯誤視為失敗 (含 5xx),交由 Worker 進行重試。
		preview := string(body)
		if len(preview) > 300 {
			preview = preview[:300]
		}
		return nil, fmt.Errorf("環境部 API 回傳非 2xx 狀態 (%d): %s", resp.StatusCode, c.redact(preview))
	}

	records, err := parseRecords(body)
	if err != nil {
		return nil, fmt.Errorf("解析 API JSON 失敗: %w", err)
	}

	return records, nil
}

// parseRecords 將環境部 API 的回應內容解析為測站記錄陣列。
//
// 相容兩種格式:
//  1. 裸陣列 (實測格式):[ {欄位...}, {欄位...} ]
//  2. 物件包裹 (舊版/部分介面):{ "total": "...", "records": [ {欄位...} ] }
//
// 解析策略:先嘗試裸陣列;若內容並非 JSON 陣列 (例如為物件),再退回包裹結構。
// 兩種皆失敗時回傳最後一個解析錯誤,方便排查實際回應格式。
func parseRecords(body []byte) ([]map[string]string, error) {
	// 格式一:頂層即為測站陣列。
	var bare []map[string]string
	if err := json.Unmarshal(body, &bare); err == nil {
		return bare, nil
	}

	// 格式二:{ total, records } 包裹物件。
	var wrapped apiResponse
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, err
	}

	return wrapped.Records, nil
}

// buildURL 組裝帶有 API 金鑰與查詢參數的完整請求網址。
func (c *Client) buildURL() (string, error) {
	base, err := url.Parse(fmt.Sprintf("%s/%s", c.baseURL, c.dataset))
	if err != nil {
		return "", err
	}
	q := base.Query()
	q.Set("api_key", c.apiKey)
	q.Set("language", "zh")
	q.Set("format", "JSON")
	q.Set("offset", "0")
	q.Set("limit", strconv.Itoa(1000)) // 全台測站約數十站,1000 足以一次取回
	base.RawQuery = q.Encode()
	return base.String(), nil
}

// redact 將字串中的 API 金鑰遮蔽,避免金鑰透過錯誤訊息或日誌外洩。
// 同時處理 api_key 出現在 URL 查詢字串中的情況 (例如 *url.Error)。
func (c *Client) redact(s string) string {
	if c.apiKey != "" {
		s = strings.ReplaceAll(s, c.apiKey, "***REDACTED***")
		// 連同 URL 編碼後的形式一併遮蔽 (查詢字串通常為編碼後)。
		if encoded := url.QueryEscape(c.apiKey); encoded != c.apiKey {
			s = strings.ReplaceAll(s, encoded, "***REDACTED***")
		}
	}
	return s
}
