package main

import (
	"fmt"
)

func main() {
	// Library-style usage:
	vault := NewVault()

	// First call: prompts for master key
	openAIKey, err := vault.Get("OPENAI_KEY")
	if err != nil {
		panic(err)
	}

	// Second call (within 1 min): uses cached key, no prompt
	deepseekKey, err := vault.Get("DEEPSEEK_KEY")
	if err != nil {
		panic(err)
	}

	fmt.Println("OPENAI_KEY  =", openAIKey)
	fmt.Println("DEEPSEEK_KEY=", deepseekKey)
}
