package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

type Payload struct {
	Params map[string]string `json:"params"`
}

func fibonacci(n int) int {
	if n <= 1 {
		return n
	}
	return fibonacci(n-1) + fibonacci(n-2)
}

func main() {
	decoder := json.NewDecoder(os.Stdin)
	var payload Payload
	if err := decoder.Decode(&payload); err != nil {
		fmt.Println("Error decoding JSON:", err)
		return
	}

	// Parse the "n" parameter from the payload
	n, err := strconv.Atoi(payload.Params["n"])
	if err != nil || n < 0 {
		fmt.Println("Please provide a valid non-negative integer for 'n'.")
		return
	}

	// Compute and print the Fibonacci number
	result := fibonacci(n)
	fmt.Printf("Fibonacci number for n=%d is %d\n", n, result)
}
