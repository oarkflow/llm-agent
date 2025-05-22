// File: main.go
package main

import (
	"context"
	"fmt"
	"time"
	
	"github.com/oarkflow/llmagent"
	"github.com/oarkflow/llmagent/providers"
)

func main() {
	// 1. Build agent and register providers (user-specific):
	agent := llmagent.NewAgent()
	
	// Construct providers with their options and register.
	openaiProvider := providers.NewOpenAI("OPENAI_API_KEY",
		llmagent.WithTimeout(30*time.Second),
		llmagent.WithBaseURL("https://api.openai.com"))
	agent.RegisterProvidersFromUser(openaiProvider)
	
	deepseekProvider := providers.NewDeepSeek("DEEPSEEK_API_KEY",
		llmagent.WithTimeout(30*time.Second),
		llmagent.WithBaseURL("https://api.deepseek.ai"))
	agent.RegisterProvidersFromUser(deepseekProvider)
	
	claudeProvider := providers.NewClaude("ANTHROPIC_API_KEY",
		llmagent.WithTimeout(30*time.Second),
		llmagent.WithBaseURL("https://api.anthropic.com"))
	agent.RegisterProvidersFromUser(claudeProvider)
	
	sonnetProvider := providers.NewSonnet("COHERE_API_KEY",
		llmagent.WithTimeout(30*time.Second),
		llmagent.WithBaseURL("https://api.cohere.ai"))
	agent.RegisterProvidersFromUser(sonnetProvider)
	
	// 2. Set default and issue a completion:
	if err := agent.SetDefault("openai"); err != nil {
		panic(err)
	}
	
	ctx := context.Background()
	streamReq := true
	req := llmagent.CompletionRequest{
		Model:  "gpt-4o-mini", // or other model names per provider
		Stream: &streamReq,    // toggle streaming
		Messages: []llmagent.Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "What's the capital of France?"},
		},
	}
	stream, err := agent.Complete(ctx, "", req) // empty string = use default
	if err != nil {
		panic(err)
	}
	
	for resp := range stream {
		if resp.Err != nil {
			fmt.Printf("[Error] %v\n", resp.Err)
			break
		}
		fmt.Print(resp.Content) // streaming chunks
	}
	fmt.Println()
}
