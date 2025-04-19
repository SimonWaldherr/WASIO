package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
)

// Payload is the JSON structure passed in on stdin.
type Payload struct {
	Params map[string]string `json:"params"`
}

func main() {
	// 1. Decode the JSON payload
	decoder := json.NewDecoder(os.Stdin)
	var pl Payload
	if err := decoder.Decode(&pl); err != nil {
		fmt.Println("Error: invalid payload")
		return
	}

	// 2. Extract parameters
	name := pl.Params["name"]
	if name == "" {
		name = "Guest"
	}
	age := pl.Params["age"]
	if age == "" {
		age = "unknown"
	}
	hobbiesParam := pl.Params["hobbies"] // expected "a,b,c"
	var hobbies []string
	if hobbiesParam != "" {
		hobbies = strings.Split(hobbiesParam, ",")
	}

	// 3. Read the template file (mounted at /templates)
	tmplBytes, err := ioutil.ReadFile("/templates/profile.html")
	if err != nil {
		fmt.Printf("Error loading template: %v\n", err)
		return
	}
	tmpl := string(tmplBytes)

	// 4. Replace the simple placeholders
	tmpl = strings.ReplaceAll(tmpl, "{{Name}}", name)
	tmpl = strings.ReplaceAll(tmpl, "{{Age}}", age)

	// 5. Build the hobbies list HTML
	var hobbiesHTML string
	if len(hobbies) > 0 {
		var b strings.Builder
		for _, h := range hobbies {
			b.WriteString("<li>" + h + "</li>")
		}
		hobbiesHTML = b.String()
	}

	// 6. Inject either the list or the “no hobbies” block
	if hobbiesHTML != "" {
		tmpl = strings.ReplaceAll(tmpl, "{{HobbiesList}}", hobbiesHTML)
		// remove the no‑hobbies placeholder
		tmpl = strings.ReplaceAll(tmpl, "{{NoHobbies}}", "")
	} else {
		// remove the list placeholder and inject the fallback
		tmpl = strings.ReplaceAll(tmpl, "{{HobbiesList}}", "")
		tmpl = strings.ReplaceAll(tmpl, "{{NoHobbies}}",
			"<p><em>No hobbies listed.</em></p>")
	}

	// 7. Write out the final HTML
	w := bufio.NewWriter(os.Stdout)
	w.WriteString(tmpl)
	w.Flush()
}
