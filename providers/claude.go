// File: llm/providers/claude.go
package providers

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/oarkflow/llmagent"
	"github.com/oarkflow/llmagent/sdk/claude"
)

type ClaudeProvider struct {
	apiKey     string
	cfg        *llmagent.ProviderConfig
	httpClient *http.Client
}

func NewClaude(apiKey string, opts ...llmagent.Option) *ClaudeProvider {
	p := &ClaudeProvider{apiKey: apiKey}
	cfg := &llmagent.ProviderConfig{
		BaseURL: "https://api.anthropic.com",
		Timeout: 30 * time.Second,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.DefaultModel == "" {
		cfg.DefaultModel = "claude-3-opus-20240229" // Updated default model
	}
	cfg.SupportedModels = []string{"claude-3-opus-20240229", "claude-3-sonnet-20240229"} // Updated models
	p.cfg = cfg
	p.httpClient = &http.Client{Timeout: p.cfg.Timeout}
	return p
}

func (c *ClaudeProvider) Name() string {
	return "claude"
}

func (c *ClaudeProvider) GetConfig() *llmagent.ProviderConfig {
	return c.cfg
}

func (c *ClaudeProvider) Complete(ctx context.Context, req llmagent.CompletionRequest) (<-chan llmagent.CompletionResponse, error) {
	if c.apiKey == "" {
		return nil, errors.New("API key is required")
	}
	if req.Model == "" {
		req.Model = c.cfg.DefaultModel
	}
	if req.Stream == nil && c.cfg.DefaultStream != nil {
		req.Stream = c.cfg.DefaultStream
	}
	if req.Temperature == 0 {
		req.Temperature = c.cfg.DefaultTemperature
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = c.cfg.DefaultMaxTokens
		if req.MaxTokens == 0 {
			req.MaxTokens = 200
		}
	}
	if req.TopP == 0 {
		req.TopP = c.cfg.DefaultTopP
	}
	out := make(chan llmagent.CompletionResponse)
	go func() {
		defer close(out)
		payload := map[string]any{
			"model":       req.Model,
			"max_tokens":  req.MaxTokens,
			"temperature": req.Temperature,
			"stream":      req.StreamValue(),
		}
		var systemMsg string
		var msgs []map[string]any
		for _, msg := range req.Messages {
			if msg.Role == "system" {
				systemMsg = msg.Content
			} else {
				m := map[string]any{
					"role":    msg.Role,
					"content": msg.Content,
				}
				if msg.Name != "" {
					m["name"] = msg.Name
				}
				msgs = append(msgs, m)
			}
		}
		if systemMsg != "" {
			payload["system"] = systemMsg
		}
		payload["messages"] = msgs
		client := claude.NewClient(c.apiKey, c.cfg.BaseURL, "/v1/messages", c.cfg.Timeout, c.cfg.DefaultModel, c.cfg.SupportedModels)
		bodyRc, err := client.Complete(ctx, payload)
		if err != nil {
			out <- llmagent.CompletionResponse{Err: err}
			return
		}
		defer bodyRc.Close()

		if !req.StreamValue() {
			var r struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			}
			b, _ := io.ReadAll(bodyRc)
			if err := json.Unmarshal(b, &r); err != nil {
				out <- llmagent.CompletionResponse{Err: err}
			} else if len(r.Content) > 0 {
				var text string
				for _, content := range r.Content {
					if content.Type == "text" {
						text += content.Text
					}
				}
				out <- llmagent.CompletionResponse{Content: text}
			}
			return
		}
		reader := bufio.NewReader(bodyRc)
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				if err != io.EOF {
					out <- llmagent.CompletionResponse{Err: err}
				}
				break
			}
			out <- llmagent.CompletionResponse{Content: string(line)}
		}
	}()
	return out, nil
}
