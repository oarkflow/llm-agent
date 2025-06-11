// File: llm/providers/sonnet.go
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
	"github.com/oarkflow/llmagent/sdk/sonnet"
)

type SonnetProvider struct {
	apiKey     string
	cfg        *llmagent.ProviderConfig
	httpClient *http.Client
}

func NewSonnet(apiKey string, opts ...llmagent.Option) *SonnetProvider {
	p := &SonnetProvider{apiKey: apiKey}
	cfg := &llmagent.ProviderConfig{
		BaseURL: "https://api.cohere.ai",
		Timeout: 30 * time.Second,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	// Set supported models and default model if empty.
	if cfg.DefaultModel == "" {
		cfg.DefaultModel = "sonnet-basic"
	}
	cfg.SupportedModels = []string{"sonnet-basic", "sonnet-pro"}
	p.cfg = cfg
	p.httpClient = &http.Client{Timeout: p.cfg.Timeout}
	return p
}

func (s *SonnetProvider) Name() string {
	return "sonnet"
}

func (c *SonnetProvider) GetConfig() *llmagent.ProviderConfig {
	return c.cfg
}

func (s *SonnetProvider) Complete(ctx context.Context, req llmagent.CompletionRequest) (<-chan llmagent.CompletionResponse, error) {
	if s.apiKey == "" {
		return nil, errors.New("API key is required")
	}
	if req.Model == "" {
		req.Model = s.cfg.DefaultModel
	}
	if req.Stream == nil && s.cfg.DefaultStream != nil {
		req.Stream = s.cfg.DefaultStream
	}
	// Set extra options if not provided
	if req.Temperature == 0 {
		req.Temperature = s.cfg.DefaultTemperature
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = s.cfg.DefaultMaxTokens
		if req.MaxTokens == 0 {
			req.MaxTokens = 200
		}
	}
	if req.TopP == 0 {
		req.TopP = s.cfg.DefaultTopP
	}
	out := make(chan llmagent.CompletionResponse)
	go func() {
		defer close(out)
		payload := map[string]any{
			"model":       req.Model,
			"prompt":      concatenateMessages(req.Messages),
			"stream":      *req.Stream,
			"maxWords":    req.MaxTokens,
			"temperature": req.Temperature,
			"top_p":       req.TopP,
			// add stop if provided
			// (will be omitted from JSON if empty)
			"stop": req.Stop,
		}
		client := sonnet.NewClient(s.apiKey, s.cfg.BaseURL, "/v1/generate", s.cfg.Timeout, s.cfg.DefaultModel, s.cfg.SupportedModels)
		bodyRc, err := client.Generate(ctx, payload)
		if err != nil {
			out <- llmagent.CompletionResponse{Err: err}
			return
		}
		defer bodyRc.Close()
		if !req.StreamValue() {
			var r struct {
				Output string `json:"output"`
			}
			b, _ := io.ReadAll(bodyRc)
			if err := json.Unmarshal(b, &r); err != nil {
				out <- llmagent.CompletionResponse{Err: err}
			} else {
				out <- llmagent.CompletionResponse{Content: r.Output}
			}
			return
		}
		reader := bufio.NewReader(bodyRc)
		for {
			part, err := reader.ReadBytes('\n')
			if err != nil {
				if err != io.EOF {
					out <- llmagent.CompletionResponse{Err: err}
				}
				break
			}
			out <- llmagent.CompletionResponse{Content: string(part)}
		}
	}()
	return out, nil
}

func concatenateMessages(msgs []llmagent.Message) string {
	var buf bytes.Buffer
	for _, m := range msgs {
		buf.WriteString(m.Role + ": " + m.Content + "\n")
	}
	return buf.String()
}
