package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

type Payload struct {
	Params map[string]string `json:"params"`
}

func parseNumbers(s string) ([]float64, error) {
	parts := strings.Split(s, ",")
	nums := make([]float64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q: %v", p, err)
		}
		nums = append(nums, v)
	}
	if len(nums) == 0 {
		return nil, fmt.Errorf("no numbers provided")
	}
	return nums, nil
}

func mean(nums []float64) float64 {
	sum := 0.0
	for _, v := range nums {
		sum += v
	}
	return sum / float64(len(nums))
}

func median(sorted []float64) float64 {
	n := len(sorted)
	if n%2 == 0 {
		return (sorted[n/2-1] + sorted[n/2]) / 2
	}
	return sorted[n/2]
}

func mode(nums []float64) []float64 {
	freq := make(map[float64]int, len(nums))
	for _, v := range nums {
		freq[v]++
	}
	maxFreq := 0
	for _, f := range freq {
		if f > maxFreq {
			maxFreq = f
		}
	}
	var modes []float64
	for v, f := range freq {
		if f == maxFreq {
			modes = append(modes, v)
		}
	}
	sort.Float64s(modes)
	return modes
}

func stddev(nums []float64, m float64) float64 {
	sum := 0.0
	for _, v := range nums {
		d := v - m
		sum += d * d
	}
	return math.Sqrt(sum / float64(len(nums)))
}

func percentile(sorted []float64, p float64) float64 {
	if p <= 0 {
		return sorted[0]
	}
	if p >= 100 {
		return sorted[len(sorted)-1]
	}
	idx := p / 100 * float64(len(sorted)-1)
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo]*(float64(hi)-idx) + sorted[hi]*(idx-float64(lo))
}

func main() {
	var payload Payload
	if err := json.NewDecoder(os.Stdin).Decode(&payload); err != nil {
		fmt.Println("Error: invalid payload")
		return
	}

	operation := strings.ToLower(payload.Params["op"])
	input := payload.Params["data"]

	if input == "" {
		fmt.Println("Usage: /stats_utils?op=summary&data=1,2,3,4,5")
		fmt.Println("Operations: summary, mean, median, mode, stddev, variance, percentile, min, max, range, sum")
		return
	}

	nums, err := parseNumbers(input)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	sorted := make([]float64, len(nums))
	copy(sorted, nums)
	sort.Float64s(sorted)

	if operation == "" {
		operation = "summary"
	}

	switch operation {
	case "summary":
		m := mean(nums)
		fmt.Printf("Count:    %d\n", len(nums))
		fmt.Printf("Sum:      %g\n", func() float64 { s := 0.0; for _, v := range nums { s += v }; return s }())
		fmt.Printf("Min:      %g\n", sorted[0])
		fmt.Printf("Max:      %g\n", sorted[len(sorted)-1])
		fmt.Printf("Range:    %g\n", sorted[len(sorted)-1]-sorted[0])
		fmt.Printf("Mean:     %.4f\n", m)
		fmt.Printf("Median:   %g\n", median(sorted))
		fmt.Printf("Std Dev:  %.4f\n", stddev(nums, m))
		fmt.Printf("Variance: %.4f\n", math.Pow(stddev(nums, m), 2))
		modes := mode(nums)
		modeStrs := make([]string, len(modes))
		for i, v := range modes {
			modeStrs[i] = strconv.FormatFloat(v, 'g', -1, 64)
		}
		fmt.Printf("Mode:     %s\n", strings.Join(modeStrs, ", "))
		fmt.Printf("P25:      %g\n", percentile(sorted, 25))
		fmt.Printf("P75:      %g\n", percentile(sorted, 75))
		fmt.Printf("P90:      %g\n", percentile(sorted, 90))

	case "mean", "avg", "average":
		fmt.Printf("Mean: %.6f\n", mean(nums))

	case "median":
		fmt.Printf("Median: %g\n", median(sorted))

	case "mode":
		modes := mode(nums)
		strs := make([]string, len(modes))
		for i, v := range modes {
			strs[i] = strconv.FormatFloat(v, 'g', -1, 64)
		}
		fmt.Printf("Mode: %s\n", strings.Join(strs, ", "))

	case "stddev", "std":
		m := mean(nums)
		fmt.Printf("Standard deviation: %.6f\n", stddev(nums, m))

	case "variance":
		m := mean(nums)
		s := stddev(nums, m)
		fmt.Printf("Variance: %.6f\n", s*s)

	case "percentile", "pct":
		p := 50.0
		if v, err := strconv.ParseFloat(payload.Params["p"], 64); err == nil {
			p = v
		}
		fmt.Printf("P%.0f: %g\n", p, percentile(sorted, p))

	case "min":
		fmt.Printf("Min: %g\n", sorted[0])

	case "max":
		fmt.Printf("Max: %g\n", sorted[len(sorted)-1])

	case "range":
		fmt.Printf("Range: %g\n", sorted[len(sorted)-1]-sorted[0])

	case "sum":
		sum := 0.0
		for _, v := range nums {
			sum += v
		}
		fmt.Printf("Sum: %g\n", sum)

	default:
		fmt.Printf("Unknown operation: %s\n", operation)
		fmt.Println("Operations: summary, mean, median, mode, stddev, variance, percentile, min, max, range, sum")
	}
}
