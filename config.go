package main

import (
    "encoding/json"
    "fmt"
    "io/ioutil"
    //"os"
    "strings"
    "sync"
)

type Route struct {
    Path     string `json:"path"`
    WasmFile string `json:"wasm_file"`
}

type Config struct {
    Port   string           `json:"port"`
    Routes map[string]Route `json:"routes"`
    mu     sync.RWMutex     // Mutex for thread-safe operations
}

// NewConfig creates a new Config instance
func NewConfig() *Config {
    return &Config{
        Routes: make(map[string]Route),
    }
}

// ParseConfig reads the configuration file and populates the Config struct
func (c *Config) ParseConfig(filename string) error {
    c.mu.Lock()
    defer c.mu.Unlock()

    data, err := ioutil.ReadFile(filename)
    if err != nil {
        return fmt.Errorf("failed to read config: %v", err)
    }

    lines := strings.Split(string(data), "\n")
    for _, line := range lines {
        line = strings.TrimSpace(line)
        if strings.HasPrefix(line, "Listen") {
            c.Port = strings.TrimSpace(strings.Split(line, " ")[1])
        } else if strings.HasPrefix(line, "Route") {
            parts := strings.Fields(line)
            if len(parts) < 3 {
                continue
            }
            routePath := parts[1]
            wasmFile := parts[2]
            c.Routes[routePath] = Route{Path: routePath, WasmFile: wasmFile}
        }
    }

    return nil
}

// Update replaces the current config with a new one
func (c *Config) Update(newConfig *Config) {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.Port = newConfig.Port
    c.Routes = newConfig.Routes
}

// GetRoutes returns a copy of the routes
func (c *Config) GetRoutes() map[string]Route {
    c.mu.RLock()
    defer c.mu.RUnlock()
    routesCopy := make(map[string]Route)
    for k, v := range c.Routes {
        routesCopy[k] = v
    }
    return routesCopy
}

// SaveConfig saves the current configuration to a file
func (c *Config) SaveConfig(filename string) error {
    c.mu.RLock()
    defer c.mu.RUnlock()

    var sb strings.Builder
    sb.WriteString(fmt.Sprintf("Listen %s\n\n", c.Port))
    sb.WriteString("<VirtualHost localhost:" + c.Port + ">\n")
    for _, route := range c.Routes {
        sb.WriteString(fmt.Sprintf("    Route %s %s\n", route.Path, route.WasmFile))
    }
    sb.WriteString("</VirtualHost>\n")

    return ioutil.WriteFile(filename, []byte(sb.String()), 0644)
}

// ToJSON returns the JSON representation of the config
func (c *Config) ToJSON() ([]byte, error) {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return json.MarshalIndent(c, "", "  ")
}

// FromJSON updates the config from JSON data
func (c *Config) FromJSON(data []byte) error {
    c.mu.Lock()
    defer c.mu.Unlock()
    return json.Unmarshal(data, c)
}
