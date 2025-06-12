// File: main.go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/oarkflow/llmagent"
	"github.com/oarkflow/llmagent/providers"
	"github.com/oarkflow/secretr"
)

func main() {
	os.Setenv("SECRETR_MASTERKEY", "admintest")

	openaiProvider := providers.NewOpenAI(secretr.MustGet("OPENAI_KEY"))
	deepseekProvider := providers.NewDeepSeek(secretr.MustGet("DEEPSEEK_KEY"))
	claudeProvider := providers.NewClaude(secretr.MustGet("ANTHROPIC_API_KEY"))

	agent := llmagent.NewAgent()
	agent.RegisterProvidersFromUser(openaiProvider)
	agent.RegisterProvidersFromUser(deepseekProvider)
	agent.RegisterProvidersFromUser(claudeProvider)

	if err := agent.SetDefault("claude"); err != nil {
		panic(err)
	}
	ctx := context.Background()
	streamReq := true
	req := llmagent.CompletionRequest{
		Stream: &streamReq,
		Messages: []llmagent.Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "What's the capital of France?"},
		},
	}
	stream, err := agent.Complete(ctx, "", req)
	if err != nil {
		panic(err)
	}
	for resp := range stream {
		if resp.Err != nil {
			fmt.Printf("[Error] %v\n", resp.Err)
			break
		}
		fmt.Print(resp.Content)
	}
	fmt.Println()
}
