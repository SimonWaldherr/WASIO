package main

import (
	"encoding/json"
	"fmt"
	"os"
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

	text := payload.Params["text"]
	operation := strings.ToLower(payload.Params["op"])

	if text == "" {
		fmt.Println("Usage: /text_utils?op=upper&text=hello")
		fmt.Println("Operations: upper, lower, title, reverse, length, words, chars, trim, split")
		return
	}

	switch operation {
	case "upper":
		fmt.Printf("Uppercase: %s\n", strings.ToUpper(text))
	case "lower":
		fmt.Printf("Lowercase: %s\n", strings.ToLower(text))
	case "title":
		fmt.Printf("Title case: %s\n", strings.Title(text))
	case "reverse":
		runes := []rune(text)
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		fmt.Printf("Reversed: %s\n", string(runes))
	case "length", "len":
		fmt.Printf("Length: %d characters\n", len([]rune(text)))
	case "words":
		words := strings.Fields(text)
		fmt.Printf("Word count: %d\n", len(words))
		if len(words) <= 10 {
			fmt.Printf("Words: %v\n", words)
		}
	case "chars":
		chars := make(map[rune]int)
		for _, r := range text {
			chars[r]++
		}
		fmt.Printf("Character count: %d unique characters\n", len(chars))
		if len(chars) <= 20 {
			fmt.Printf("Character frequency: %v\n", chars)
		}
	case "trim":
		trimmed := strings.TrimSpace(text)
		fmt.Printf("Trimmed: '%s'\n", trimmed)
		fmt.Printf("Removed %d characters\n", len(text)-len(trimmed))
	case "split":
		delimiter := payload.Params["delimiter"]
		if delimiter == "" {
			delimiter = ","
		}
		parts := strings.Split(text, delimiter)
		fmt.Printf("Split by '%s': %d parts\n", delimiter, len(parts))
		for i, part := range parts {
			fmt.Printf("  [%d]: %s\n", i, strings.TrimSpace(part))
		}
	case "contains":
		search := payload.Params["search"]
		if search == "" {
			fmt.Println("Error: search parameter required for contains operation")
			return
		}
		count := strings.Count(text, search)
		fmt.Printf("'%s' appears %d times in text\n", search, count)
	case "replace":
		old := payload.Params["old"]
		new := payload.Params["new"]
		if old == "" {
			fmt.Println("Error: old parameter required for replace operation")
			return
		}
		if new == "" {
			new = ""
		}
		result := strings.ReplaceAll(text, old, new)
		fmt.Printf("Replaced '%s' with '%s': %s\n", old, new, result)
	case "palindrome":
		normalized := strings.ToLower(strings.ReplaceAll(text, " ", ""))
		runes := []rune(normalized)
		isPalindrome := true
		for i := 0; i < len(runes)/2; i++ {
			if runes[i] != runes[len(runes)-1-i] {
				isPalindrome = false
				break
			}
		}
		fmt.Printf("Is palindrome: %t\n", isPalindrome)
	default:
		fmt.Printf("Error: unsupported operation '%s'\n", operation)
		fmt.Println("Operations: upper, lower, title, reverse, length, words, chars, trim, split, contains, replace, palindrome")
		return
	}

	// Always show basic stats
	fmt.Printf("\nText statistics:\n")
	fmt.Printf("  Length: %d characters\n", len([]rune(text)))
	fmt.Printf("  Words: %d\n", len(strings.Fields(text)))
	fmt.Printf("  Lines: %d\n", strings.Count(text, "\n")+1)
}
