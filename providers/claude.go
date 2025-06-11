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
	// Set supported models and default model if empty.
	if cfg.DefaultModel == "" {
		cfg.DefaultModel = "claude-v1"
	}
	cfg.SupportedModels = []string{"claude-v1", "claude-instant-v1"}
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
			"messages":    req.Messages,
			"stream":      *req.Stream,
			"temperature": req.Temperature,
			"max_tokens":  req.MaxTokens,
			"top_p":       req.TopP,
			// add stop if provided
			"stop": req.Stop,
		}
		client := claude.NewClient(c.apiKey, c.cfg.BaseURL, "/v1/complete", c.cfg.Timeout, c.cfg.DefaultModel, c.cfg.SupportedModels)
		bodyRc, err := client.Complete(ctx, payload)
		if err != nil {
			out <- llmagent.CompletionResponse{Err: err}
			return
		}
		defer bodyRc.Close()
		if !req.StreamValue() {
			var r struct {
				Completion string `json:"completion"`
			}
			b, _ := io.ReadAll(bodyRc)
			if err := json.Unmarshal(b, &r); err != nil {
				out <- llmagent.CompletionResponse{Err: err}
			} else {
				out <- llmagent.CompletionResponse{Content: r.Completion}
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
