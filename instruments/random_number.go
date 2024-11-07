package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
)

type Payload struct {
	Seed int64 `json:"seed"`
}

func main() {
	// Read JSON from stdin
	decoder := json.NewDecoder(os.Stdin)
	var payload Payload
	if err := decoder.Decode(&payload); err != nil {
		fmt.Println("Error decoding JSON:", err)
		return
	}

	// Set random seed and generate a random number
	rand.Seed(payload.Seed)
	randomNumber := rand.Intn(100) // Generates a number between 0 and 99

	fmt.Printf("Generated Random Number: %d\n", randomNumber)
}
