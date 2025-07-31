package main

import (
	"encoding/json"
	"fmt"
	"os"
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
	aStr := payload.Params["a"]
	bStr := payload.Params["b"]

	if operation == "" || aStr == "" || bStr == "" {
		fmt.Println("Usage: /calculator?op=add&a=5&b=3")
		fmt.Println("Supported operations: add, sub, mul, div, pow, mod")
		return
	}

	a, err := strconv.ParseFloat(aStr, 64)
	if err != nil {
		fmt.Printf("Error: invalid number 'a': %s\n", aStr)
		return
	}

	b, err := strconv.ParseFloat(bStr, 64)
	if err != nil {
		fmt.Printf("Error: invalid number 'b': %s\n", bStr)
		return
	}

	var result float64
	var resultStr string

	switch operation {
	case "add", "+":
		result = a + b
		resultStr = fmt.Sprintf("%.2f + %.2f = %.2f", a, b, result)
	case "sub", "-":
		result = a - b
		resultStr = fmt.Sprintf("%.2f - %.2f = %.2f", a, b, result)
	case "mul", "*":
		result = a * b
		resultStr = fmt.Sprintf("%.2f * %.2f = %.2f", a, b, result)
	case "div", "/":
		if b == 0 {
			fmt.Println("Error: division by zero")
			return
		}
		result = a / b
		resultStr = fmt.Sprintf("%.2f / %.2f = %.2f", a, b, result)
	case "pow", "**":
		result = 1
		if b >= 0 {
			for i := 0; i < int(b); i++ {
				result *= a
			}
		} else {
			fmt.Println("Error: negative exponents not supported")
			return
		}
		resultStr = fmt.Sprintf("%.2f ^ %.0f = %.2f", a, b, result)
	case "mod", "%":
		if b == 0 {
			fmt.Println("Error: modulo by zero")
			return
		}
		result = float64(int(a) % int(b))
		resultStr = fmt.Sprintf("%.0f %% %.0f = %.0f", a, b, result)
	default:
		fmt.Printf("Error: unsupported operation '%s'\n", operation)
		fmt.Println("Supported operations: add, sub, mul, div, pow, mod")
		return
	}

	fmt.Println(resultStr)
}
