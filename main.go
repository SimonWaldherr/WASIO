package main

import (
    "log"
    "net/http"
)

func main() {
    // Initialize config
    config := NewConfig()
    if err := config.ParseConfig("config.conf"); err != nil {
        log.Fatalf("Error parsing config: %v", err)
    }

    // Initialize module cache
    moduleCache := NewModuleCache()
    defer moduleCache.Close()

    // Create server
    server := NewServer(config, moduleCache)

    // Start config watcher
    go WatchConfigFile("config.conf", config)

    // Optional: Uncomment if `WatchWasmFiles` is implemented
    // go WatchWasmFiles(config, moduleCache)

    // Start HTTP server
    log.Printf("Starting server on port %s...", config.Port)
    if err := http.ListenAndServe(":"+config.Port, server); err != nil {
        log.Fatalf("Server failed: %v", err)
    }
}
