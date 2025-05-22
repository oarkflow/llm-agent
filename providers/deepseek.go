// File: llm/providers/deepseek.go
package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/oarkflow/llmagent"
)

type DeepSeekProvider struct {
	apiKey     string
	cfg        *llmagent.ProviderConfig
	httpClient *http.Client
}

func NewDeepSeek(apiKey string, opts ...llmagent.Option) *DeepSeekProvider {
	p := &DeepSeekProvider{apiKey: apiKey}
	cfg := &llmagent.ProviderConfig{
		BaseURL: "https://api.deepseek.ai",
		Timeout: 30 * time.Second,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	p.cfg = cfg
	p.httpClient = &http.Client{Timeout: p.cfg.Timeout}
	return p
}

func (d *DeepSeekProvider) Name() string {
	return "deepseek"
}

func (d *DeepSeekProvider) Complete(ctx context.Context, req llmagent.CompletionRequest) (<-chan llmagent.CompletionResponse, error) {
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
	out := make(chan llmagent.CompletionResponse)
	go func() {
		defer close(out)
		body := map[string]any{
			"model":       req.Model,
			"prompts":     req.Messages,
			"stream":      *req.Stream,
			"temperature": req.Temperature,
			"max_tokens":  req.MaxTokens,
			"top_p":       req.TopP,
		}
		data, _ := json.Marshal(body)
		httpReq, _ := http.NewRequestWithContext(ctx, "POST", d.cfg.BaseURL+"/api/v1/completions", bytes.NewReader(data))
		httpReq.Header.Set("X-API-Key", d.apiKey)
		httpReq.Header.Set("Content-Type", "application/json")
		resp, err := d.httpClient.Do(httpReq)
		if err != nil {
			out <- llmagent.CompletionResponse{Err: err}
			return
		}
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			out <- llmagent.CompletionResponse{Err: errors.New("HTTP " + http.StatusText(resp.StatusCode) + ": " + string(b))}
			resp.Body.Close()
			return
		}
		defer resp.Body.Close()
		if !req.StreamValue() {
			var r struct {
				Text string `json:"text"`
			}
			b, _ := io.ReadAll(resp.Body)
			if err := json.Unmarshal(b, &r); err != nil {
				out <- llmagent.CompletionResponse{Err: err}
			} else {
				out <- llmagent.CompletionResponse{Content: r.Text}
			}
			return
		}
		reader := bufio.NewReader(resp.Body)
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
