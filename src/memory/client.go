// Package memory provides HTTP client for LLMCrawl's memory service.
package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Client is an HTTP client for the memory service API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new memory service client.
func NewClient(serviceURL string) *Client {
	return &Client{
		baseURL: serviceURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SearchResult represents a single search result from the memory service.
type SearchResult struct {
	Content string  `json:"content"`
	Source  string  `json:"source"`
	Heading string  `json:"heading,omitempty"`
	Score   float64 `json:"score"`
}

// HealthResponse represents the health check response.
type HealthResponse struct {
	Status           string `json:"status"`
	Service          string `json:"service"`
	Version          string `json:"version"`
	MemoryFileExists bool   `json:"memory_file_exists"`
	DailyLogCount    int    `json:"daily_log_count"`
	WatcherRunning   bool   `json:"watcher_running"`
}

// ContextResponse represents the context response.
type ContextResponse struct {
	Context    string   `json:"context"`
	Sources    []string `json:"sources"`
	TokenCount int      `json:"token_count"`
}

// WriteResponse represents a write operation response.
type WriteResponse struct {
	Success  bool   `json:"success"`
	FilePath string `json:"file_path"`
	Message  string `json:"message"`
}

// SearchResponse represents the search response.
type SearchResponse struct {
	Results []SearchResult `json:"results"`
	Query   string         `json:"query"`
}

// ReindexResponse represents the reindex response.
type ReindexResponse struct {
	Success       bool   `json:"success"`
	ChunksIndexed int    `json:"chunks_indexed"`
	Message       string `json:"message"`
}

// HealthCheck checks if the memory service is reachable and healthy.
func (c *Client) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("health check returned status %d: %s", resp.StatusCode, string(body))
	}

	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return fmt.Errorf("decoding health response: %w", err)
	}

	if health.Status != "healthy" && health.Status != "healthy (fallback mode)" {
		return fmt.Errorf("service unhealthy: %s", health.Status)
	}

	return nil
}

// WriteDaily logs a conversation message to the daily log.
// POST /write_daily
func (c *Client) WriteDaily(ctx context.Context, role, content, sessionID string) error {
	body := map[string]string{
		"role":    role,
		"content": content,
	}
	if sessionID != "" {
		body["session_id"] = sessionID
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/write_daily", bytes.NewBuffer(jsonBody))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("write_daily failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("write_daily returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// WriteMemory saves durable facts to MEMORY.md.
// POST /write_memory
func (c *Client) WriteMemory(ctx context.Context, content, section string) error {
	body := map[string]interface{}{
		"content": content,
		"replace": false,
	}
	if section != "" {
		body["section"] = section
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/write_memory", bytes.NewBuffer(jsonBody))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("write_memory failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("write_memory returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// Search performs semantic search across memories.
// POST /search
func (c *Client) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 5
	}

	body := map[string]interface{}{
		"query": query,
		"limit": limit,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/search", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("search returned status %d: %s", resp.StatusCode, string(body))
	}

	var searchResp SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, fmt.Errorf("decoding search response: %w", err)
	}

	return searchResp.Results, nil
}

// GetContext retrieves memory context for conversation start.
// GET /context
func (c *Client) GetContext(ctx context.Context, query string, maxTokens int) (string, error) {
	u, err := url.Parse(c.baseURL + "/context")
	if err != nil {
		return "", fmt.Errorf("parsing URL: %w", err)
	}

	q := u.Query()
	if maxTokens > 0 {
		q.Set("max_tokens", strconv.Itoa(maxTokens))
	}
	if query != "" {
		q.Set("query", query)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("get context failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("get context returned status %d: %s", resp.StatusCode, string(body))
	}

	var contextResp ContextResponse
	if err := json.NewDecoder(resp.Body).Decode(&contextResp); err != nil {
		return "", fmt.Errorf("decoding context response: %w", err)
	}

	return contextResp.Context, nil
}

// Reindex rebuilds the vector index from markdown files.
// POST /reindex
func (c *Client) Reindex(ctx context.Context, force bool) error {
	u, err := url.Parse(c.baseURL + "/reindex")
	if err != nil {
		return fmt.Errorf("parsing URL: %w", err)
	}

	if force {
		q := u.Query()
		q.Set("force", "true")
		u.RawQuery = q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("reindex failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("reindex returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}
