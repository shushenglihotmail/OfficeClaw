package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/officeclaw/src/config"
	"github.com/officeclaw/src/telemetry"
)

// OpenAIProvider implements Provider for direct OpenAI API access.
type OpenAIProvider struct {
	apiKey      string
	model       string
	maxTokens   int
	temperature float64
	httpClient  *http.Client
}

// NewOpenAIProvider creates an OpenAI provider.
func NewOpenAIProvider(cfg config.OpenAIConfig, defaultTemp float64, timeoutSec int) (*OpenAIProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("openai API key is required")
	}

	model := cfg.Model
	if model == "" {
		model = "gpt-4"
	}
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}

	return &OpenAIProvider{
		apiKey:      cfg.APIKey,
		model:       model,
		maxTokens:   maxTokens,
		temperature: defaultTemp,
		httpClient:  &http.Client{Timeout: time.Duration(timeoutSec) * time.Second},
	}, nil
}

func (p *OpenAIProvider) Name() string { return "openai" }

// ChatCompletion sends a request to the OpenAI Chat Completions API.
func (p *OpenAIProvider) ChatCompletion(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	startTime := time.Now()

	maxTokens := p.maxTokens
	if req.MaxTokens > 0 {
		maxTokens = req.MaxTokens
	}

	payload := map[string]interface{}{
		"model":       p.model,
		"messages":    convertMessagesToOpenAI(req.Messages),
		"max_tokens":  maxTokens,
		"temperature": req.Temperature,
	}

	if len(req.Tools) > 0 {
		payload["tools"] = req.Tools
		payload["tool_choice"] = req.ToolChoice
	}

	body, _ := json.Marshal(payload)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai error (%d): %s", resp.StatusCode, string(respBody))
	}

	var data struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				Role      string `json:"role"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(respBody, &data); err != nil {
		return nil, fmt.Errorf("parsing openai response: %w", err)
	}

	if len(data.Choices) == 0 {
		return nil, fmt.Errorf("openai returned no choices")
	}

	choice := data.Choices[0]
	result := &CompletionResponse{
		Content:      choice.Message.Content,
		Role:         "assistant",
		FinishReason: choice.FinishReason,
		Usage: Usage{
			PromptTokens:     data.Usage.PromptTokens,
			CompletionTokens: data.Usage.CompletionTokens,
			TotalTokens:      data.Usage.TotalTokens,
		},
	}

	for _, tc := range choice.Message.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, ToolCall{
			ID:   tc.ID,
			Type: tc.Type,
			Function: FunctionCall{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
	}

	duration := time.Since(startTime).Seconds()
	telemetry.RecordLLMRequest(ctx, "openai", p.model, "success", duration,
		int64(data.Usage.PromptTokens), int64(data.Usage.CompletionTokens))

	return result, nil
}
