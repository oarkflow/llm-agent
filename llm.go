// File: llm/provider.go
package llmagent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

// new: ProviderMetrics tracks per‑provider statistics.
type ProviderMetrics struct {
	SuccessCount int
	FailureCount int
	TotalLatency time.Duration
}

type ProviderConfig struct {
	BaseURL            string
	Timeout            time.Duration
	DefaultModel       string      // default model if request.Model is empty
	DefaultStream      *bool       // default stream value if request.Stream is nil
	DefaultTemperature float64     // default temperature (e.g. 0.7)
	DefaultMaxTokens   int         // default max tokens (e.g. 100)
	DefaultTopP        float64     // default top_p (e.g. 1.0)
	SupportedModels    []string    // list of supported models
	Logger             *log.Logger // optional logger for debugging
	RetryCount         int         // number of retry attempts for a failing request
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

func WithLogger(logger *log.Logger) Option {
	return func(p *ProviderConfig) {
		p.Logger = logger
	}
}

func WithRetryCount(count int) Option {
	return func(p *ProviderConfig) {
		p.RetryCount = count
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
	Stop        []string  `json:"stop,omitempty"`        // new optional stop sequence(s)
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
	GetConfig() *ProviderConfig
}

// Agent holds user-registered providers and system default providers.
type Agent struct {
	DefaultProvider   string
	FallbackProviders []string // new: fallback provider names
	userProviders     map[string]Provider
	systemProviders   map[string]Provider

	// updated: cache now stores cacheEntry with expiration.
	cache     map[string]cacheEntry
	cacheLock sync.RWMutex

	// new: CacheTTL defines the lifetime of a cached entry.
	CacheTTL time.Duration

	// new: metrics tracking per provider
	metrics     map[string]*ProviderMetrics
	metricsLock sync.Mutex
}

// new: cacheEntry holds cached response and its expiration.
type cacheEntry struct {
	content   string
	expiresAt time.Time
}

// NewAgent creates an empty Agent.
func NewAgent() *Agent {
	agent := &Agent{
		userProviders:   make(map[string]Provider),
		systemProviders: make(map[string]Provider),
		cache:           make(map[string]cacheEntry),
		metrics:         make(map[string]*ProviderMetrics),
		CacheTTL:        5 * time.Minute, // default TTL
	}
	// new: background goroutine to purge expired cache entries.
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			agent.cacheLock.Lock()
			for k, entry := range agent.cache {
				if entry.expiresAt.Before(now) {
					delete(agent.cache, k)
				}
			}
			agent.cacheLock.Unlock()
		}
	}()
	return agent
}

type CachedRequest struct {
	Messages    []Message
	Model       string
	Temperature float64
	MaxTokens   int
	TopP        float64
	Stop        []string
}

// new helper: getCacheKey computes a hash key from a non-streaming request.
func getCacheKey(req CompletionRequest) (string, error) {
	data, err := json.Marshal(CachedRequest{
		Messages:    req.Messages,
		Model:       req.Model,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		TopP:        req.TopP,
		Stop:        req.Stop,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum), nil
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

// ListProviders returns a list of all provider names.
func (a *Agent) ListProviders() []string {
	var list []string
	for name := range a.userProviders {
		list = append(list, name)
	}
	for name := range a.systemProviders {
		if _, ok := a.userProviders[name]; !ok {
			list = append(list, name)
		}
	}
	return list
}

// RegisterFallbackProviders sets the fallback provider names (in order).
func (a *Agent) RegisterFallbackProviders(names []string) {
	a.FallbackProviders = names
}

// Complete does a completion using either the named provider or the default.
// If the request is non-streaming, it checks an internal cache.
func (a *Agent) Complete(ctx context.Context, providerName string, req CompletionRequest) (<-chan CompletionResponse, error) {
	// If non-streaming, try cache first.
	if !req.StreamValue() {
		key, err := getCacheKey(req)
		if err == nil {
			a.cacheLock.RLock()
			if entry, ok := a.cache[key]; ok {
				// Check if the cached entry is still valid.
				if entry.expiresAt.After(time.Now()) {
					a.cacheLock.RUnlock()
					out := make(chan CompletionResponse, 1)
					out <- CompletionResponse{Content: entry.content}
					close(out)
					return out, nil
				}
			}
			a.cacheLock.RUnlock()
		}
	}

	// ...existing provider selection and request defaults code...
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
	cfg := p.GetConfig()
	if cfg.DefaultModel == "" && req.Model == "" {
		return nil, errors.New("no model specified")
	}
	if cfg.DefaultMaxTokens == 0 {
		if req.MaxTokens == 0 {
			req.MaxTokens = 200
		}
	}

	tryProvider := func(current Provider) (<-chan CompletionResponse, error) {
		// Ensure metrics for current provider exists.
		a.metricsLock.Lock()
		if _, ok := a.metrics[current.Name()]; !ok {
			a.metrics[current.Name()] = &ProviderMetrics{}
		}
		a.metricsLock.Unlock()

		attempts := 1
		if current.GetConfig().RetryCount > 0 {
			attempts = current.GetConfig().RetryCount + 1
		}
		var respChan <-chan CompletionResponse
		var err error
		for i := 0; i < attempts; i++ {
			start := time.Now()
			respChan, err = current.Complete(ctx, req)
			latency := time.Since(start)

			a.metricsLock.Lock()
			m := a.metrics[current.Name()]
			m.TotalLatency += latency
			if err == nil {
				m.SuccessCount++
				a.metricsLock.Unlock()
				if current.GetConfig().Logger != nil {
					current.GetConfig().Logger.Printf("Provider %q succeeded on attempt %d", current.Name(), i+1)
				}
				return respChan, nil
			}
			m.FailureCount++
			a.metricsLock.Unlock()

			if current.GetConfig().Logger != nil {
				current.GetConfig().Logger.Printf("Provider %q attempt %d failed: %v", current.Name(), i+1, err)
			}
			time.Sleep(100 * time.Millisecond)
		}
		return nil, err
	}

	respChan, err := tryProvider(p)
	// If chosen provider fails, try fallback providers.
	if err != nil && len(a.FallbackProviders) > 0 {
		errMsg := fmt.Sprintf("Primary provider %q failed: %v", name, err)
		if cfg.Logger != nil {
			cfg.Logger.Println(errMsg)
		}
		for _, fbName := range a.FallbackProviders {
			if fbName == name {
				continue
			}
			var fb Provider
			if fb, ok = a.userProviders[fbName]; !ok {
				if fb, ok = a.systemProviders[fbName]; !ok {
					continue
				}
			}
			fbCfg := fb.GetConfig()
			if fbCfg.DefaultModel == "" && req.Model == "" {
				continue
			}
			if fbCfg.DefaultMaxTokens == 0 && req.MaxTokens == 0 {
				req.MaxTokens = 200
			}
			if respChan, err = tryProvider(fb); err == nil {
				goto CACHE_STORE
			}
			errMsg = fmt.Sprintf("Fallback provider %q failed: %v", fb.Name(), err)
			if fbCfg.Logger != nil {
				fbCfg.Logger.Println(errMsg)
			}
		}
		return nil, fmt.Errorf("all providers failed; last error: %v", err)
	}

CACHE_STORE:
	// If the request is non-streaming, capture and cache the response.
	if !req.StreamValue() {
		// Read single response from respChan (non-streaming returns one response).
		resp, ok := <-respChan
		if ok && resp.Err == nil {
			if key, err := getCacheKey(req); err == nil {
				a.cacheLock.Lock()
				a.cache[key] = cacheEntry{
					content:   resp.Content,
					expiresAt: time.Now().Add(a.CacheTTL),
				}
				a.cacheLock.Unlock()
			}
			// Return a channel with the captured response.
			out := make(chan CompletionResponse, 1)
			out <- resp
			close(out)
			return out, nil
		}
		// If error, return as is.
		out := make(chan CompletionResponse, 1)
		out <- resp
		close(out)
		return out, nil
	}

	return respChan, nil
}
