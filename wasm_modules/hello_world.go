package main

import (
    "math/rand"
    "os"
    "strconv"
)

func main() {
    args := os.Args

    // Use the first argument as the seed for random generation
    if len(args) > 0 {
        seed, err := strconv.ParseInt(args[0], 10, 64)
        if err == nil {
            rand.Seed(seed)
        }
    }

    // Use the second argument as the name, if provided
    if len(args) > 1 {
        name := args[1]
        println("Hello, " + name + "!")
    } else {
        println("Hello, World!")
    }
}
