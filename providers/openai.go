// File: llm/providers/openai.go
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

type OpenAIProvider struct {
	apiKey     string
	cfg        *llmagent.ProviderConfig
	httpClient *http.Client
}

// NewOpenAI constructs a new OpenAIProvider with the given API key and options.
func NewOpenAI(apiKey string, opts ...llmagent.Option) *OpenAIProvider {
	p := &OpenAIProvider{apiKey: apiKey}
	cfg := &llmagent.ProviderConfig{
		BaseURL: "https://api.openai.com",
		Timeout: 30 * time.Second,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	p.cfg = cfg
	p.httpClient = &http.Client{Timeout: p.cfg.Timeout}
	return p
}

func (o *OpenAIProvider) Name() string {
	return "openai"
}

func (o *OpenAIProvider) Complete(ctx context.Context, req llmagent.CompletionRequest) (<-chan llmagent.CompletionResponse, error) {
	// Use defaults from config if not provided by request
	if req.Model == "" {
		req.Model = o.cfg.DefaultModel
	}
	if req.Stream == nil && o.cfg.DefaultStream != nil {
		req.Stream = o.cfg.DefaultStream
	}
	if req.Temperature == 0 {
		req.Temperature = o.cfg.DefaultTemperature
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = o.cfg.DefaultMaxTokens
		if req.MaxTokens == 0 {
			req.MaxTokens = 200
		}
	}
	if req.TopP == 0 {
		req.TopP = o.cfg.DefaultTopP
	}
	out := make(chan llmagent.CompletionResponse)
	go func() {
		defer close(out)
		body := map[string]any{
			"model":       req.Model,
			"messages":    req.Messages,
			"stream":      *req.Stream,
			"temperature": req.Temperature,
			"max_tokens":  req.MaxTokens,
			"top_p":       req.TopP,
		}
		data, _ := json.Marshal(body)
		httpReq, err := http.NewRequestWithContext(ctx, "POST", o.cfg.BaseURL+"/v1/chat/completions", bytes.NewReader(data))
		if err != nil {
			out <- llmagent.CompletionResponse{Err: err}
			return
		}
		httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
		httpReq.Header.Set("Content-Type", "application/json")
		resp, err := o.httpClient.Do(httpReq)
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
			var res struct {
				Choices []struct {
					Message llmagent.Message `json:"message"`
				} `json:"choices"`
			}
			bodyBytes, _ := io.ReadAll(resp.Body)
			if err := json.Unmarshal(bodyBytes, &res); err != nil {
				out <- llmagent.CompletionResponse{Err: err}
				return
			}
			if len(res.Choices) > 0 {
				out <- llmagent.CompletionResponse{Content: res.Choices[0].Message.Content}
			}
			return
		}
		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				if err != io.EOF {
					out <- llmagent.CompletionResponse{Err: err}
				}
				break
			}
			if bytes.HasPrefix(line, []byte("data: ")) {
				var chunk struct {
					Choices []struct {
						Delta struct {
							Content string `json:"content"`
						} `json:"delta"`
					} `json:"choices"`
				}
				if err := json.Unmarshal(line[6:], &chunk); err == nil {
					for _, c := range chunk.Choices {
						out <- llmagent.CompletionResponse{Content: c.Delta.Content}
					}
				}
			}
		}
	}()
	return out, nil
}
