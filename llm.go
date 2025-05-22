// File: llm/provider.go
package llmagent

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type ProviderConfig struct {
	BaseURL            string
	Timeout            time.Duration
	DefaultModel       string  // default model if request.Model is empty
	DefaultStream      *bool   // default stream value if request.Stream is nil
	DefaultTemperature float64 // default temperature (e.g. 0.7)
	DefaultMaxTokens   int     // default max tokens (e.g. 100)
	DefaultTopP        float64 // default top_p (e.g. 1.0)
}

type Option func(*ProviderConfig)

func WithTimeout(timeout time.Duration) Option {
	return func(p *ProviderConfig) {
		p.Timeout = timeout
	}
}

func WithBaseURL(url string) Option {
	return func(p *ProviderConfig) {
		p.BaseURL = url
	}
}

func WithDefaultModel(model string) Option {
	return func(p *ProviderConfig) {
		p.DefaultModel = model
	}
}

func WithDefaultStream(stream bool) Option {
	return func(p *ProviderConfig) {
		p.DefaultStream = &stream
	}
}

func WithDefaultTemperature(temp float64) Option {
	return func(p *ProviderConfig) {
		p.DefaultTemperature = temp
	}
}

func WithDefaultMaxTokens(max int) Option {
	return func(p *ProviderConfig) {
		p.DefaultMaxTokens = max
	}
}

func WithDefaultTopP(topP float64) Option {
	return func(p *ProviderConfig) {
		p.DefaultTopP = topP
	}
}

// Message represents a single turn in the conversation.
type Message struct {
	Role    string `json:"role"`    // "user" or "assistant"
	Content string `json:"content"` //
}

// CompletionRequest holds settings for a completion call.
type CompletionRequest struct {
	Messages    []Message `json:"messages"`
	Model       string    `json:"model,omitempty"`       // if empty, use ProviderConfig.DefaultModel
	Stream      *bool     `json:"stream,omitempty"`      // if nil, use ProviderConfig.DefaultStream
	Temperature float64   `json:"temperature,omitempty"` // if zero, use ProviderConfig.DefaultTemperature
	MaxTokens   int       `json:"max_tokens,omitempty"`  // if zero, use ProviderConfig.DefaultMaxTokens
	TopP        float64   `json:"top_p,omitempty"`       // if zero, use ProviderConfig.DefaultTopP
}

func (c CompletionRequest) StreamValue() bool {
	if c.Stream != nil {
		return *c.Stream
	}
	if c.Stream == nil && c.MaxTokens > 0 {
		return true // stream if max tokens is set
	}
	return false
}

// CompletionResponse is streamed back to the caller.
type CompletionResponse struct {
	Content string `json:"content"` // the completion text
	Err     error  `json:"error"`   // any error that occurred
}

// Provider now assumes provider configuration is internal.
type Provider interface {
	Name() string
	Complete(ctx context.Context, req CompletionRequest) (<-chan CompletionResponse, error)
}

// Agent holds user-registered providers and system default providers.
type Agent struct {
	DefaultProvider string
	userProviders   map[string]Provider
	systemProviders map[string]Provider
}

// NewAgent creates an empty Agent.
func NewAgent() *Agent {
	return &Agent{
		userProviders:   make(map[string]Provider),
		systemProviders: make(map[string]Provider),
	}
}

// RegisterProvidersFromUser registers a provider constructed by the user.
func (a *Agent) RegisterProvidersFromUser(p Provider) {
	a.userProviders[p.Name()] = p
}

// RegisterProvidersFromSystem registers a system default provider.
func (a *Agent) RegisterProvidersFromSystem(p Provider) {
	a.systemProviders[p.Name()] = p
}

// SetDefault selects which provider to use if none is specified per-call.
func (a *Agent) SetDefault(name string) error {
	if _, ok := a.userProviders[name]; !ok {
		if _, ok = a.systemProviders[name]; !ok {
			return errors.New("default provider not registered")
		}
	}
	a.DefaultProvider = name
	return nil
}

// Complete does a completion using either the named provider or the default.
func (a *Agent) Complete(ctx context.Context, providerName string, req CompletionRequest) (<-chan CompletionResponse, error) {
	name := providerName
	if name == "" {
		name = a.DefaultProvider
	}
	var p Provider
	var ok bool
	if p, ok = a.userProviders[name]; !ok {
		if p, ok = a.systemProviders[name]; !ok {
			return nil, fmt.Errorf("provider %q not registered", name)
		}
	}
	return p.Complete(ctx, req)
}
