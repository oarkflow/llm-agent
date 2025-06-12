// File: main.go
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/oarkflow/llmagent"
	"github.com/oarkflow/llmagent/providers"
	"github.com/oarkflow/secretr"
)

func main() {
	os.Setenv("SECRETR_MASTERKEY", "admintest")
	// 1. Build agent and register providers (user-specific):
	agent := llmagent.NewAgent()
	deepseekApiKey, err := secretr.Get("DEEPSEEK_KEY")
	if err != nil {
		panic(err)
	}

	openAIKey, err := secretr.Get("OPENAI_KEY")
	if err != nil {
		panic(err)
	}
	// Construct providers with their options and register.
	openaiProvider := providers.NewOpenAI(openAIKey,
		llmagent.WithTimeout(30*time.Second),
		llmagent.WithBaseURL("https://api.openai.com"))
	agent.RegisterProvidersFromUser(openaiProvider)
	deepseekProvider := providers.NewDeepSeek(deepseekApiKey,
		llmagent.WithTimeout(30*time.Second),
		llmagent.WithBaseURL("https://api.deepseek.com"))
	agent.RegisterProvidersFromUser(deepseekProvider)

	claudeProvider := providers.NewClaude("anthropicApiKey",
		llmagent.WithTimeout(30*time.Second),
		llmagent.WithBaseURL("https://api.anthropic.com"))
	agent.RegisterProvidersFromUser(claudeProvider)
	// 2. Set default and issue a completion:
	if err := agent.SetDefault("deepseek"); err != nil {
		panic(err)
	}

	ctx := context.Background()
	streamReq := true
	req := llmagent.CompletionRequest{
		Stream: &streamReq, // toggle streaming
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
