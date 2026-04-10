package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

type Payload struct {
	Params map[string]string `json:"params"`
}

func main() {
	var payload Payload
	if err := json.NewDecoder(os.Stdin).Decode(&payload); err != nil {
		fmt.Println("Error: invalid payload")
		return
	}

	operation := strings.ToLower(payload.Params["op"])
	input := payload.Params["input"]

	if input == "" && operation != "" {
		fmt.Println("Error: 'input' parameter required")
		fmt.Println("Usage: /json_utils?op=format&input={...}")
		fmt.Println("Operations: format, minify, validate, keys, get, stats")
		return
	}

	if operation == "" {
		fmt.Println("Usage: /json_utils?op=format&input={...}")
		fmt.Println("Operations: format, minify, validate, keys, get, stats")
		return
	}

	switch operation {
	case "format", "pretty":
		var v interface{}
		if err := json.Unmarshal([]byte(input), &v); err != nil {
			fmt.Printf("Invalid JSON: %v\n", err)
			return
		}
		out, _ := json.MarshalIndent(v, "", "  ")
		fmt.Println(string(out))

	case "minify", "compact":
		var v interface{}
		if err := json.Unmarshal([]byte(input), &v); err != nil {
			fmt.Printf("Invalid JSON: %v\n", err)
			return
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.Encode(v)
		fmt.Print(strings.TrimRight(buf.String(), "\n"))

	case "validate":
		if json.Valid([]byte(input)) {
			// Count tokens for a rough complexity measure
			dec := json.NewDecoder(strings.NewReader(input))
			tokens := 0
			for {
				_, err := dec.Token()
				if err != nil {
					break
				}
				tokens++
			}
			fmt.Println("Valid JSON")
			fmt.Printf("Tokens: %d\n", tokens)
		} else {
			fmt.Println("Invalid JSON")
		}

	case "keys":
		var obj map[string]json.RawMessage
		if err := json.Unmarshal([]byte(input), &obj); err != nil {
			fmt.Printf("Error: input must be a JSON object: %v\n", err)
			return
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Printf("Keys (%d):\n", len(keys))
		for _, k := range keys {
			fmt.Printf("  %s\n", k)
		}

	case "get":
		key := payload.Params["key"]
		if key == "" {
			fmt.Println("Error: 'key' parameter required for get operation")
			return
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal([]byte(input), &obj); err != nil {
			fmt.Printf("Error: input must be a JSON object: %v\n", err)
			return
		}
		val, ok := obj[key]
		if !ok {
			fmt.Printf("Key %q not found\n", key)
			return
		}
		// Pretty-print the extracted value
		var v interface{}
		json.Unmarshal(val, &v)
		out, _ := json.MarshalIndent(v, "", "  ")
		fmt.Printf("%s: %s\n", key, string(out))

	case "stats":
		var v interface{}
		if err := json.Unmarshal([]byte(input), &v); err != nil {
			fmt.Printf("Invalid JSON: %v\n", err)
			return
		}
		keys, arrays, depth := 0, 0, 0
		collectStats(v, 0, &keys, &arrays, &depth)
		fmt.Printf("Size (bytes): %d\n", len(input))
		fmt.Printf("Object keys: %d\n", keys)
		fmt.Printf("Array elements: %d\n", arrays)
		fmt.Printf("Max nesting depth: %d\n", depth)
		fmt.Printf("Minified size: %d bytes\n", minifiedSize(v))

	default:
		fmt.Printf("Unknown operation: %s\n", operation)
		fmt.Println("Operations: format, minify, validate, keys, get, stats")
	}
}

func collectStats(v interface{}, currentDepth int, keys, arrays, maxDepth *int) {
	if currentDepth > *maxDepth {
		*maxDepth = currentDepth
	}
	switch val := v.(type) {
	case map[string]interface{}:
		*keys += len(val)
		for _, child := range val {
			collectStats(child, currentDepth+1, keys, arrays, maxDepth)
		}
	case []interface{}:
		*arrays += len(val)
		for _, child := range val {
			collectStats(child, currentDepth+1, keys, arrays, maxDepth)
		}
	}
}

func minifiedSize(v interface{}) int {
	b, _ := json.Marshal(v)
	return len(strconv.Quote(string(b))) - 2 // unquote overhead
}
