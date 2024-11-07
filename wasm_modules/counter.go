package main

import (
    "fmt"
    "os"
    "strconv"
)

const counterFile = "/tmp/counter.txt"

func main() {
    count := loadCounter()
    fmt.Printf("Current Counter: %d\n", count)
    saveCounter(count + 1)
}

// Load the counter from a file
func loadCounter() int {
    data, err := os.ReadFile(counterFile)
    if err != nil {
        return 0 // default to 0 if file doesn't exist
    }
    count, _ := strconv.Atoi(string(data))
    return count
}

// Save the counter to a file
func saveCounter(count int) {
    os.WriteFile(counterFile, []byte(strconv.Itoa(count)), 0644)
}
