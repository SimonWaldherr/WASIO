package main

import (
    "bytes"
    "fmt"
    "log"
    "net/http"
    "strconv"
    "time"
)

type Server struct {
    config      *Config
    moduleCache *ModuleCache
}

func NewServer(config *Config, moduleCache *ModuleCache) *Server {
    return &Server{
        config:      config,
        moduleCache: moduleCache,
    }
}

// ServeHTTP is the main entry point for handling HTTP requests.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    path := r.URL.Path

    // Check if the request path corresponds to a configured route for a WASM module
    if route, exists := s.config.GetRoutes()[path]; exists {
        s.handleRoute(w, r, route)
        return
    }

    // If the path does not match a configured route, return a 404 error
    http.Error(w, "404 - Not Found", http.StatusNotFound)
}

// handleRoute handles HTTP requests that are mapped to WASM modules.
func (s *Server) handleRoute(w http.ResponseWriter, r *http.Request, route Route) {
    startTime := time.Now()
    output := &bytes.Buffer{}
    
    // Generate a seed and other parameters
    randomSeed := time.Now().UnixNano()
    query := r.URL.Query()
    
    // Set up arguments with the seed as the first argument
    args := []string{strconv.FormatInt(randomSeed, 10)} // First argument is the seed
    
    // Add GET/POST parameters as additional arguments
    if name := query.Get("name"); name != "" {
        args = append(args, name)
    }
    
    // Instantiate and run the module with parameters
    err := s.moduleCache.InstantiateAndRunModuleWithArgs(route.WasmFile, output, args)
    if err != nil {
        http.Error(w, fmt.Sprintf("Error running module: %v", err), http.StatusInternalServerError)
        return
    }
    
    // Log the request duration
    duration := time.Since(startTime)
    log.Printf("Route: %s | Module: %s | Duration: %v", route.Path, route.WasmFile, duration)
    
    // Send the captured output as the HTTP response
    w.Write(output.Bytes())
}
