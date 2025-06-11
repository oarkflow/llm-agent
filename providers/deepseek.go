// File: llm/providers/deepseek.go
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
	"github.com/oarkflow/llmagent/sdk/deepseek"
)

type DeepSeekProvider struct {
	apiKey     string
	cfg        *llmagent.ProviderConfig
	httpClient *http.Client
}

func NewDeepSeek(apiKey string, opts ...llmagent.Option) *DeepSeekProvider {
	p := &DeepSeekProvider{apiKey: apiKey}
	cfg := &llmagent.ProviderConfig{
		BaseURL: "https://api.deepseek.com",
		Timeout: 30 * time.Second,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	// Set supported models and default model if empty.
	if cfg.DefaultModel == "" {
		cfg.DefaultModel = "deepseek-chat"
	}
	cfg.SupportedModels = []string{"deepseek-chat", "deepseek-text"}
	p.cfg = cfg
	p.httpClient = &http.Client{Timeout: p.cfg.Timeout}
	return p
}

func (d *DeepSeekProvider) Name() string {
	return "deepseek"
}

func (c *DeepSeekProvider) GetConfig() *llmagent.ProviderConfig {
	return c.cfg
}

func (d *DeepSeekProvider) Complete(ctx context.Context, req llmagent.CompletionRequest) (<-chan llmagent.CompletionResponse, error) {
	if d.apiKey == "" {
		return nil, errors.New("API key is required")
	}
	if req.Model == "" {
		req.Model = d.cfg.DefaultModel
	}
	if req.Stream == nil && d.cfg.DefaultStream != nil {
		req.Stream = d.cfg.DefaultStream
	}
	if req.Temperature == 0 {
		req.Temperature = d.cfg.DefaultTemperature
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = d.cfg.DefaultMaxTokens
		if req.MaxTokens == 0 {
			req.MaxTokens = 200
		}
	}
	if req.TopP == 0 {
		req.TopP = d.cfg.DefaultTopP
	}
	if req.TopP == 0 {
		req.TopP = 1.0
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
		client := deepseek.NewClient(d.apiKey, d.cfg.BaseURL, "/chat/completions", d.cfg.Timeout, d.cfg.DefaultModel, d.cfg.SupportedModels)
		bodyRc, err := client.ChatCompletion(ctx, payload)
		if err != nil {
			out <- llmagent.CompletionResponse{Err: err}
			return
		}
		defer bodyRc.Close()
		if !req.StreamValue() {
			var r struct {
				Text string `json:"text"`
			}
			b, _ := io.ReadAll(bodyRc)
			if err := json.Unmarshal(b, &r); err != nil {
				out <- llmagent.CompletionResponse{Err: err}
			} else {
				out <- llmagent.CompletionResponse{Content: r.Text}
			}
			return
		}
		reader := bufio.NewReader(bodyRc)
		for {
			chunk, err := reader.ReadBytes('\n')
			if err != nil {
				if err != io.EOF {
					out <- llmagent.CompletionResponse{Err: err}
				}
				break
			}
			out <- llmagent.CompletionResponse{Content: string(chunk)}
		}
	}()
	return out, nil
}
