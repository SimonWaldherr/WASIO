package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type Payload struct {
	Params map[string]string `json:"params"`
	Seed   int64             `json:"seed"`
}

func main() {
	// Read JSON from stdin
	decoder := json.NewDecoder(os.Stdin)
	var payload Payload
	if err := decoder.Decode(&payload); err != nil {
		fmt.Println("Error decoding JSON:", err)
		return
	}

	// Use the "name" parameter if provided
	name := payload.Params["name"]
	if name == "" {
		name = "World"
	}

	// Print a greeting
	fmt.Printf("Hello, %s! (seed: %d)\n", name, payload.Seed)
}
