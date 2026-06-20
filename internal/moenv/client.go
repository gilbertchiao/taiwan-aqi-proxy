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
	"time"
)

// Client 為環境部 API 用戶端。
type Client struct {
	apiKey     string
	dataset    string
	baseURL    string
	httpClient *http.Client
}

// apiResponse 對應 v2 API 的外層 JSON 結構。
// records 內每筆為 欄位名 -> 字串值 的對應。
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
		return nil, fmt.Errorf("呼叫環境部 API 失敗: %w", err)
	}
	defer resp.Body.Close()

	// 限制讀取大小 (10MB),避免異常回應耗盡記憶體。
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("讀取回應內容失敗: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 將上游錯誤視為失敗 (含 5xx),交由 Worker 進行重試。
		preview := string(body)
		if len(preview) > 300 {
			preview = preview[:300]
		}
		return nil, fmt.Errorf("環境部 API 回傳非 2xx 狀態 (%d): %s", resp.StatusCode, preview)
	}

	var parsed apiResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("解析 API JSON 失敗: %w", err)
	}

	return parsed.Records, nil
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
