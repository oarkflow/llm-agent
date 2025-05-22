package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

type Client struct {
	APIKey          string
	BaseURL         string
	ChatEndpoint    string
	Timeout         time.Duration
	DefaultModel    string
	SupportedModels []string
	HttpClient      *http.Client
}

func NewClient(apiKey, baseURL, chatEndpoint string, timeout time.Duration, defaultModel string, supportedModels []string) *Client {
	return &Client{
		APIKey:          apiKey,
		BaseURL:         baseURL,
		ChatEndpoint:    chatEndpoint,
		Timeout:         timeout,
		DefaultModel:    defaultModel,
		SupportedModels: supportedModels,
		HttpClient:      &http.Client{Timeout: timeout},
	}
}

func (c *Client) ChatCompletion(ctx context.Context, payload map[string]any) (io.ReadCloser, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+c.ChatEndpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HttpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, errors.New("HTTP " + http.StatusText(resp.StatusCode) + ": " + string(body))
	}
	return resp.Body, nil
}
