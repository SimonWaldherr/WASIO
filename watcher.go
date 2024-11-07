package main

import (
    "log"

    "github.com/fsnotify/fsnotify"
)

func WatchConfigFile(configFile string, config *Config) {
    watcher, _ := fsnotify.NewWatcher()
    defer watcher.Close()

    watcher.Add(configFile)
    for {
        select {
        case event := <-watcher.Events:
            if event.Op&fsnotify.Write == fsnotify.Write {
                log.Println("Config file changed. Reloading...")
                newConfig := NewConfig()
                if err := newConfig.ParseConfig(configFile); err == nil {
                    config.Update(newConfig)
                } else {
                    log.Printf("Failed to reload config: %v", err)
                }
            }
        case err := <-watcher.Errors:
            log.Printf("Config watcher error: %v", err)
        }
    }
}

// Optional: Implement `WatchWasmFiles` if you want to dynamically reload modules
// on change, but for now, you can comment out or ignore the calls to `InvalidateModule`.
