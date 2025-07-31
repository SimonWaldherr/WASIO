package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
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

	timezone := payload.Params["tz"]
	format := payload.Params["format"]
	operation := strings.ToLower(payload.Params["op"])

	now := time.Now()

	// Handle timezone
	if timezone != "" {
		if loc, err := time.LoadLocation(timezone); err == nil {
			now = now.In(loc)
		} else {
			fmt.Printf("Warning: invalid timezone '%s', using local time\n", timezone)
		}
	}

	// Handle different operations
	switch operation {
	case "unix":
		fmt.Printf("Unix timestamp: %d\n", now.Unix())
		return
	case "iso":
		fmt.Printf("ISO 8601: %s\n", now.Format(time.RFC3339))
		return
	case "rfc822":
		fmt.Printf("RFC 822: %s\n", now.Format(time.RFC822))
		return
	case "add":
		duration := payload.Params["duration"]
		if duration == "" {
			fmt.Println("Error: duration parameter required for add operation")
			return
		}
		if d, err := time.ParseDuration(duration); err == nil {
			result := now.Add(d)
			fmt.Printf("Time + %s = %s\n", duration, result.Format(time.RFC3339))
		} else {
			fmt.Printf("Error: invalid duration '%s'\n", duration)
		}
		return
	case "diff":
		targetTime := payload.Params["target"]
		if targetTime == "" {
			fmt.Println("Error: target parameter required for diff operation")
			return
		}
		if target, err := time.Parse(time.RFC3339, targetTime); err == nil {
			diff := target.Sub(now)
			fmt.Printf("Difference: %s\n", diff.String())
		} else {
			fmt.Printf("Error: invalid target time '%s' (use RFC3339 format)\n", targetTime)
		}
		return
	case "weekday":
		fmt.Printf("Day of week: %s\n", now.Weekday().String())
		return
	case "year":
		fmt.Printf("Year: %d\n", now.Year())
		return
	case "month":
		fmt.Printf("Month: %s (%d)\n", now.Month().String(), int(now.Month()))
		return
	case "day":
		fmt.Printf("Day: %d\n", now.Day())
		return
	}

	// Default format handling
	if format == "" {
		format = time.RFC3339
	} else {
		// Convert common format shortcuts
		switch strings.ToLower(format) {
		case "kitchen":
			format = time.Kitchen
		case "stamp":
			format = time.Stamp
		case "rfc822":
			format = time.RFC822
		case "rfc3339":
			format = time.RFC3339
		case "iso":
			format = time.RFC3339
		}
	}

	formatted := now.Format(format)
	fmt.Printf("Current time: %s\n", formatted)

	// Additional info
	if timezone != "" {
		fmt.Printf("Timezone: %s\n", timezone)
	}
	fmt.Printf("Unix timestamp: %d\n", now.Unix())
}
