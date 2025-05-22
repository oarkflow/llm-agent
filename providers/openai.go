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
	"github.com/oarkflow/llmagent/sdk/openai"
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
	// Set supported models and default model if empty.
	if cfg.DefaultModel == "" {
		cfg.DefaultModel = "gpt-3.5-turbo"
	}
	cfg.SupportedModels = []string{"gpt-3.5-turbo", "gpt-4"}
	p.cfg = cfg
	p.httpClient = &http.Client{Timeout: p.cfg.Timeout}
	return p
}

func (o *OpenAIProvider) Name() string {
	return "openai"
}

func (c *OpenAIProvider) GetConfig() *llmagent.ProviderConfig {
	return c.cfg
}

func (o *OpenAIProvider) Complete(ctx context.Context, req llmagent.CompletionRequest) (<-chan llmagent.CompletionResponse, error) {
	if o.apiKey == "" {
		return nil, errors.New("API key is required")
	}
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
		payload := map[string]any{
			"model":       req.Model,
			"messages":    req.Messages,
			"stream":      *req.Stream,
			"temperature": req.Temperature,
			"max_tokens":  req.MaxTokens,
			"top_p":       req.TopP,
		}
		client := openai.NewClient(o.apiKey, o.cfg.BaseURL, "/v1/chat/completions", o.cfg.Timeout, o.cfg.DefaultModel, o.cfg.SupportedModels)
		bodyRc, err := client.ChatCompletion(ctx, payload)
		if err != nil {
			out <- llmagent.CompletionResponse{Err: err}
			return
		}
		defer bodyRc.Close()
		if !req.StreamValue() {
			var res struct {
				Choices []struct {
					Message llmagent.Message `json:"message"`
				} `json:"choices"`
			}
			b, _ := io.ReadAll(bodyRc)
			if err := json.Unmarshal(b, &res); err != nil {
				out <- llmagent.CompletionResponse{Err: err}
				return
			}
			if len(res.Choices) > 0 {
				out <- llmagent.CompletionResponse{Content: res.Choices[0].Message.Content}
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
