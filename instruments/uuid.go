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

// newV4 generates a single RFC 4122 v4 UUID using the given rand source.
func newV4(r *rand.Rand) string {
	var b [16]byte
	for i := range b {
		b[i] = byte(r.Intn(256))
	}
	// Set version 4 (bits 12-15 of byte 6)
	b[6] = (b[6] & 0x0f) | 0x40
	// Set variant 10xx (bits 6-7 of byte 8)
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func main() {
	var payload Payload
	if err := json.NewDecoder(os.Stdin).Decode(&payload); err != nil {
		fmt.Println("Error: invalid payload")
		return
	}

	r := rand.New(rand.NewSource(payload.Seed))

	operation := strings.ToLower(payload.Params["op"])
	if operation == "" {
		operation = "generate"
	}

	switch operation {
	case "generate", "gen", "new":
		count := 1
		if v, err := strconv.Atoi(payload.Params["n"]); err == nil && v >= 1 && v <= 100 {
			count = v
		}
		if count == 1 {
			fmt.Println(newV4(r))
			return
		}
		fmt.Printf("Generated %d UUIDs:\n", count)
		for i := 0; i < count; i++ {
			fmt.Printf("  %d: %s\n", i+1, newV4(r))
		}

	case "validate":
		input := strings.TrimSpace(payload.Params["input"])
		if input == "" {
			fmt.Println("Error: 'input' parameter required for validate operation")
			return
		}
		if isValidUUID(input) {
			fmt.Printf("Valid UUID: %s\n", strings.ToLower(input))
			version := (input[14] - '0')
			fmt.Printf("Version: %d\n", version)
		} else {
			fmt.Printf("Invalid UUID: %s\n", input)
		}

	case "bulk":
		count := 10
		if v, err := strconv.Atoi(payload.Params["n"]); err == nil && v >= 1 && v <= 100 {
			count = v
		}
		uuids := make([]string, count)
		for i := range uuids {
			uuids[i] = newV4(r)
		}
		// Output as JSON array for easy consumption
		out, _ := json.MarshalIndent(uuids, "", "  ")
		fmt.Println(string(out))

	default:
		fmt.Printf("Unknown operation: %s\n", operation)
		fmt.Println("Operations: generate (default), validate, bulk")
		fmt.Println("Parameters:")
		fmt.Println("  n=<count>  Number of UUIDs to generate (1-100, default 1)")
		fmt.Println("  input=<uuid>  UUID to validate (for validate operation)")
	}
}

// isValidUUID checks whether s matches the canonical UUID format
// xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx (any version/variant).
func isValidUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !isHex(byte(c)) {
				return false
			}
		}
	}
	return true
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') ||
		(c >= 'a' && c <= 'f') ||
		(c >= 'A' && c <= 'F')
}
