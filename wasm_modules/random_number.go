package main

import (
    "math/rand"
    "os"
    "strconv"
)

func main() {
    // Access all arguments, starting from os.Args[0]
    args := os.Args
    println("os.Args length:", len(os.Args))
    for i, arg := range os.Args {
        println("os.Args[", i, "]:", arg)
    }

    if len(args) > 0 {
        println("Received seed argument:", args[0])

        // Attempt to parse the seed from os.Args[0]
        seed, err := strconv.ParseInt(args[0], 10, 64)
        if err == nil {
            rand.Seed(seed) // Set the seed
        } else {
            println("Invalid seed argument, using default seed")
        }
    } else {
        println("No seed argument provided, using default seed")
    }

    // Generate a random number
    num := rand.Intn(100) // Random number between 0 and 99

    // Process additional arguments if any
    if len(args) > 1 {
        for _, arg := range args[1:] {
            println("Received additional argument:", arg)
        }
    }

    // Output the random number
    println("Generated Random Number:", num)
}
