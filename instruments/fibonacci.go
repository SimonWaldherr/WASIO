package main

import (
    "os"
    "strconv"
)

func fibonacci(n int) int {
    if n <= 1 {
        return n
    }
    return fibonacci(n-1) + fibonacci(n-2)
}

func main() {
    args := os.Args
    if len(args) > 0 {
        n, err := strconv.Atoi(args[1])
        if err == nil && n >= 0 {
            println("Fibonacci number:", fibonacci(n))
            return
        }
    }
    println("Please provide a valid non-negative integer as parameter.")
}
