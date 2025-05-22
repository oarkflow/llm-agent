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
	p.cfg = cfg
	p.httpClient = &http.Client{Timeout: p.cfg.Timeout}
	return p
}

func (s *SonnetProvider) Name() string {
	return "sonnet"
}

func (s *SonnetProvider) Complete(ctx context.Context, req llmagent.CompletionRequest) (<-chan llmagent.CompletionResponse, error) {
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
		body := map[string]any{
			"model":       req.Model,
			"prompt":      concatenateMessages(req.Messages),
			"stream":      *req.Stream,
			"maxWords":    req.MaxTokens, // using max_tokens from request
			"temperature": req.Temperature,
			"top_p":       req.TopP,
		}
		data, _ := json.Marshal(body)
		httpReq, _ := http.NewRequestWithContext(ctx, "POST", s.cfg.BaseURL+"/v1/generate", bytes.NewReader(data))
		httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)
		httpReq.Header.Set("Content-Type", "application/json")
		resp, err := s.httpClient.Do(httpReq)
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
				Output string `json:"output"`
			}
			b, _ := io.ReadAll(resp.Body)
			if err := json.Unmarshal(b, &r); err != nil {
				out <- llmagent.CompletionResponse{Err: err}
			} else {
				out <- llmagent.CompletionResponse{Content: r.Output}
			}
			return
		}
		reader := bufio.NewReader(resp.Body)
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
