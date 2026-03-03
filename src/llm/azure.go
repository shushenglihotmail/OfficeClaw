package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/officeclaw/src/config"
	"github.com/officeclaw/src/telemetry"
)

// AzureProvider implements Provider for Azure OpenAI and Azure Foundry.
// Supports both OpenAI-compatible and Anthropic-compatible deployments
// routed through Azure, mirroring LLMCrawl gateway's multi-model resolution.
// Can use either API key or Entra ID bearer token for authentication.
type AzureProvider struct {
	endpoint      string
	apiKey        string
	apiVersion    string
	models        []config.ModelEntry
	temperature   float64
	httpClient    *http.Client
	tokenProvider TokenProvider
}

// NewAzureProvider creates an Azure provider.
func NewAzureProvider(cfg config.AzureConfig, defaultTemp float64, timeoutSec int) (*AzureProvider, error) {
	endpoint := strings.TrimRight(cfg.Endpoint, "/")
	if endpoint == "" {
		return nil, fmt.Errorf("azure endpoint is required")
	}

	return &AzureProvider{
		endpoint:    endpoint,
		apiKey:      cfg.APIKey,
		apiVersion:  cfg.APIVersion,
		models:      cfg.Models,
		temperature: defaultTemp,
		httpClient:  &http.Client{Timeout: time.Duration(timeoutSec) * time.Second},
	}, nil
}

func (p *AzureProvider) Name() string { return "azure" }

// SetTokenProvider sets the function used to obtain bearer tokens for Entra ID auth.
func (p *AzureProvider) SetTokenProvider(provider TokenProvider) {
	p.tokenProvider = provider
}

// resolveModel finds the deployment config for a model name.
func (p *AzureProvider) resolveModel(name string) (deploymentName, providerType string, maxTokens int) {
	for _, m := range p.models {
		if m.Name == name {
			return m.DeploymentName, m.ProviderType, m.MaxOutputTokens
		}
	}
	// Fallback: use name as deployment, assume openai
	return name, "openai", 8192
}

// ChatCompletion routes to the correct Azure deployment.
func (p *AzureProvider) ChatCompletion(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	// For Azure, we use the first model config if no specific model is requested
	modelName := ""
	if len(p.models) > 0 {
		modelName = p.models[0].Name
	}

	deploymentName, providerType, maxTokens := p.resolveModel(modelName)
	if req.MaxTokens > 0 {
		maxTokens = req.MaxTokens
	}

	log.Printf("[llm/azure] Resolved model '%s' -> deployment '%s' (provider: %s, max_tokens: %d)",
		modelName, deploymentName, providerType, maxTokens)

	if providerType == "anthropic" {
		return p.anthropicCompletion(ctx, deploymentName, req, maxTokens)
	}
	return p.openaiCompletion(ctx, deploymentName, req, maxTokens)
}

// openaiCompletion handles Azure OpenAI compatible completions.
func (p *AzureProvider) openaiCompletion(ctx context.Context, deployment string, req CompletionRequest, maxTokens int) (*CompletionResponse, error) {
	startTime := time.Now()

	url := fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=%s",
		p.endpoint, deployment, p.apiVersion)

	payload := map[string]interface{}{
		"messages":    convertMessagesToOpenAI(req.Messages),
		"max_tokens":  maxTokens,
		"temperature": req.Temperature,
	}

	if len(req.Tools) > 0 {
		payload["tools"] = req.Tools
		payload["tool_choice"] = req.ToolChoice
	}

	body, _ := json.Marshal(payload)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Set authentication: prefer bearer token from Entra ID, fallback to API key
	if p.tokenProvider != nil {
		token, err := p.tokenProvider(ctx)
		if err != nil {
			return nil, fmt.Errorf("getting Entra ID token for Azure OpenAI: %w", err)
		}
		httpReq.Header.Set("Authorization", "Bearer "+token)
		log.Printf("[llm/azure] Using Entra ID bearer token for authentication")
	} else if p.apiKey != "" {
		httpReq.Header.Set("api-key", p.apiKey)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("azure openai request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("azure openai error (%d): %s", resp.StatusCode, string(respBody))
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
		return nil, fmt.Errorf("parsing azure response: %w", err)
	}

	if len(data.Choices) == 0 {
		return nil, fmt.Errorf("azure returned no choices")
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
	telemetry.RecordLLMRequest(ctx, "azure", deployment, "success", duration,
		int64(data.Usage.PromptTokens), int64(data.Usage.CompletionTokens))

	return result, nil
}

// anthropicCompletion handles Anthropic deployments routed through Azure Foundry.
// NOTE: This is not currently supported. Use the "anthropic" provider with Claude CLI instead.
func (p *AzureProvider) anthropicCompletion(ctx context.Context, deployment string, req CompletionRequest, maxTokens int) (*CompletionResponse, error) {
	return nil, fmt.Errorf("Anthropic models through Azure Foundry are not supported. Use provider: 'anthropic' with Claude CLI instead")
}

// convertMessagesToOpenAI converts internal messages to OpenAI API format.
func convertMessagesToOpenAI(messages []Message) []map[string]interface{} {
	var result []map[string]interface{}
	for _, msg := range messages {
		m := map[string]interface{}{
			"role":    msg.Role,
			"content": msg.Content,
		}
		if len(msg.ToolCalls) > 0 {
			var toolCalls []map[string]interface{}
			for _, tc := range msg.ToolCalls {
				toolCalls = append(toolCalls, map[string]interface{}{
					"id":   tc.ID,
					"type": tc.Type,
					"function": map[string]string{
						"name":      tc.Function.Name,
						"arguments": tc.Function.Arguments,
					},
				})
			}
			m["tool_calls"] = toolCalls
		}
		if msg.ToolCallID != "" {
			m["tool_call_id"] = msg.ToolCallID
		}
		if msg.Name != "" {
			m["name"] = msg.Name
		}
		result = append(result, m)
	}
	return result
}
