package main

import (
	"encoding/json"
	"fmt"
	"net/url"
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

	input := payload.Params["input"]
	operation := strings.ToLower(payload.Params["op"])

	if input == "" {
		fmt.Println("Usage: /url_utils?op=encode&input=hello world")
		fmt.Println("Operations: encode, decode, parse, validate")
		return
	}

	switch operation {
	case "encode":
		encoded := url.QueryEscape(input)
		fmt.Printf("URL encoded: %s\n", encoded)

	case "decode":
		decoded, err := url.QueryUnescape(input)
		if err != nil {
			fmt.Printf("Error decoding URL: %v\n", err)
			return
		}
		fmt.Printf("URL decoded: %s\n", decoded)

	case "parse":
		u, err := url.Parse(input)
		if err != nil {
			fmt.Printf("Error parsing URL: %v\n", err)
			return
		}
		
		fmt.Printf("URL components:\n")
		fmt.Printf("  Scheme: %s\n", u.Scheme)
		fmt.Printf("  Host: %s\n", u.Host)
		fmt.Printf("  Path: %s\n", u.Path)
		fmt.Printf("  Query: %s\n", u.RawQuery)
		fmt.Printf("  Fragment: %s\n", u.Fragment)
		
		if u.User != nil {
			fmt.Printf("  User: %s\n", u.User.Username())
		}
		
		if u.RawQuery != "" {
			values, err := url.ParseQuery(u.RawQuery)
			if err == nil {
				fmt.Printf("  Query parameters:\n")
				for key, vals := range values {
					for _, val := range vals {
						fmt.Printf("    %s = %s\n", key, val)
					}
				}
			}
		}

	case "validate":
		u, err := url.Parse(input)
		if err != nil {
			fmt.Printf("Invalid URL: %v\n", err)
			return
		}
		
		isValid := true
		issues := []string{}
		
		if u.Scheme == "" {
			issues = append(issues, "missing scheme")
			isValid = false
		}
		
		if u.Host == "" && (u.Scheme == "http" || u.Scheme == "https") {
			issues = append(issues, "missing host for http/https URL")
			isValid = false
		}
		
		if isValid {
			fmt.Printf("Valid URL: %s\n", input)
		} else {
			fmt.Printf("Invalid URL: %s\n", strings.Join(issues, ", "))
		}

	case "join":
		base := payload.Params["base"]
		if base == "" {
			fmt.Println("Error: base parameter required for join operation")
			return
		}
		
		baseURL, err := url.Parse(base)
		if err != nil {
			fmt.Printf("Error parsing base URL: %v\n", err)
			return
		}
		
		relativeURL, err := url.Parse(input)
		if err != nil {
			fmt.Printf("Error parsing relative URL: %v\n", err)
			return
		}
		
		joined := baseURL.ResolveReference(relativeURL)
		fmt.Printf("Joined URL: %s\n", joined.String())

	default:
		fmt.Printf("Error: unsupported operation '%s'\n", operation)
		fmt.Println("Operations: encode, decode, parse, validate, join")
		return
	}
}
