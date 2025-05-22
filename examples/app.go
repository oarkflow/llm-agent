package main

import (
	"fmt"
	
	"github.com/oarkflow/llmagent/vault"
)

func main() {
	store := vault.NewVault()
	openAIKey, err := store.Get("OPENAI_KEY")
	if err != nil {
		panic(err)
	}
	fmt.Println("OPENAI_KEY  =", openAIKey)
}
