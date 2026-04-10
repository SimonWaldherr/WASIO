package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
)

type Payload struct {
	Params map[string]string `json:"params"`
	Seed   int64             `json:"seed"`
}

func main() {
	var payload Payload
	if err := json.NewDecoder(os.Stdin).Decode(&payload); err != nil {
		fmt.Println("Error decoding JSON:", err)
		return
	}

	rand.Seed(payload.Seed)

	// Parse optional range parameters
	min := 0
	max := 100
	count := 1

	if v, err := strconv.Atoi(payload.Params["min"]); err == nil {
		min = v
	}
	if v, err := strconv.Atoi(payload.Params["max"]); err == nil && v > min {
		max = v
	}
	if v, err := strconv.Atoi(payload.Params["n"]); err == nil && v >= 1 && v <= 100 {
		count = v
	}

	rangeSize := max - min
	if count == 1 {
		fmt.Printf("Random number (%d–%d): %d\n", min, max, min+rand.Intn(rangeSize))
		return
	}

	nums := make([]string, count)
	for i := range nums {
		nums[i] = strconv.Itoa(min + rand.Intn(rangeSize))
	}
	fmt.Printf("Random numbers (%d–%d, n=%d): %s\n", min, max, count, strings.Join(nums, ", "))
}
