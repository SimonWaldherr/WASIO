package main

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "io/ioutil"
    "log"
    "net/http"
    "os"
    "sync"
    "time"

    "github.com/tetratelabs/wazero"
    "github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// Config represents the server configuration, including routes and caching settings.
type Config struct {
    Port      string           `json:"port"`
    Routes    map[string]Route `json:"routes"`
    CacheTTL  int              `json:"cache_ttl"` // Default cache TTL in seconds
    CacheSize int              `json:"cache_size"`
}

// Route defines a server route mapped to a WASM instrument with specific options.
type Route struct {
    Path     string `json:"path"`
    WasmFile string `json:"wasm_file"`
    Cache    bool   `json:"cache"`
    TTL      int    `json:"ttl"`
}

// RequestPayload represents the data sent to the WASM instrument.
type RequestPayload struct {
    Params map[string]string `json:"params"`
    Seed   int64             `json:"seed"`
}

// Server represents the main server with configuration and module caching.
type Server struct {
    config      *Config
    moduleCache *ModuleCache
    cache       *ResponseCache
}

// ModuleCache manages the caching of compiled WASM modules.
type ModuleCache struct {
    cache map[string]wazero.CompiledModule
    mu    sync.RWMutex
    rt    wazero.Runtime
}

// ResponseCache manages cached responses for the server.
type ResponseCache struct {
    data map[string]CachedResponse
    mu   sync.RWMutex
}

// CachedResponse stores a cached response with an expiration timestamp.
type CachedResponse struct {
    Value      []byte
    Expiration time.Time
}

// NewConfig parses the configuration from a JSON file.
func NewConfig(filename string) (*Config, error) {
    data, err := ioutil.ReadFile(filename)
    if err != nil {
        return nil, fmt.Errorf("failed to read config: %v", err)
    }
    var config Config
    if err := json.Unmarshal(data, &config); err != nil {
        return nil, fmt.Errorf("failed to parse config: %v", err)
    }
    return &config, nil
}

// NewModuleCache initializes the module cache.
func NewModuleCache() *ModuleCache {
    ctx := context.Background()
    rt := wazero.NewRuntime(ctx)
    wasi_snapshot_preview1.MustInstantiate(ctx, rt)
    return &ModuleCache{
        cache: make(map[string]wazero.CompiledModule),
        rt:    rt,
    }
}

// NewResponseCache initializes the response cache.
func NewResponseCache(size int) *ResponseCache {
    return &ResponseCache{data: make(map[string]CachedResponse, size)}
}

// GetCachedResponse checks and returns a cached response if it's valid.
func (rc *ResponseCache) GetCachedResponse(key string) ([]byte, bool) {
    rc.mu.RLock()
    defer rc.mu.RUnlock()
    if res, found := rc.data[key]; found && time.Now().Before(res.Expiration) {
        return res.Value, true
    }
    return nil, false
}

// SetCachedResponse stores a response in the cache with TTL.
func (rc *ResponseCache) SetCachedResponse(key string, value []byte, ttl int) {
    rc.mu.Lock()
    defer rc.mu.Unlock()
    rc.data[key] = CachedResponse{
        Value:      value,
        Expiration: time.Now().Add(time.Duration(ttl) * time.Second),
    }
}

// ServeHTTP routes requests to the appropriate WASM instrument and handles caching.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    path := r.URL.Path
    route, exists := s.config.Routes[path]
    if !exists {
        http.Error(w, "404 - Not Found", http.StatusNotFound)
        return
    }
    
    // Generate cache key and attempt to retrieve from cache
    cacheKey := path + r.URL.RawQuery
    if route.Cache {
        if cached, found := s.cache.GetCachedResponse(cacheKey); found {
            w.Write(cached)
            return
        }
    }

    // Prepare the request payload
    payload := RequestPayload{
        Params: map[string]string{},
        Seed:   time.Now().UnixNano(),
    }
    for key, values := range r.URL.Query() {
        payload.Params[key] = values[0]
    }
    
    // Run the instrument and capture output
    output := &bytes.Buffer{}
    err := s.moduleCache.RunInstrument(route.WasmFile, payload, output)
    if err != nil {
        http.Error(w, fmt.Sprintf("Error running module: %v", err), http.StatusInternalServerError)
        return
    }

    // Cache the response if caching is enabled
    response := output.Bytes()
    if route.Cache {
        ttl := s.config.CacheTTL
        if route.TTL > 0 {
            ttl = route.TTL
        }
        s.cache.SetCachedResponse(cacheKey, response, ttl)
    }
    
    // Return response to client
    w.Write(response)
}

// RunInstrument executes a WASM instrument with structured payload data.
func (mc *ModuleCache) RunInstrument(wasmFile string, payload RequestPayload, output io.Writer) error {
    compiledModule, err := mc.GetCompiledModule(wasmFile)
    if err != nil {
        return err
    }
    
    // Convert payload to JSON
    payloadBytes, err := json.Marshal(payload)
    if err != nil {
        return fmt.Errorf("failed to marshal payload: %v", err)
    }
    
    // Create a new module config with payload as stdin
    moduleConfig := wazero.NewModuleConfig().WithStdin(bytes.NewReader(payloadBytes)).WithStdout(output)
    
    // Instantiate and run the module
    module, err := mc.rt.InstantiateModule(context.Background(), compiledModule, moduleConfig)
    if err != nil {
        return fmt.Errorf("failed to instantiate module: %v", err)
    }
    defer module.Close(context.Background())
    
    if mainFunc := module.ExportedFunction("_start"); mainFunc != nil {
        _, err := mainFunc.Call(context.Background())
        return err
    }
    return fmt.Errorf("no _start function found in module")
}

// GetCompiledModule returns a cached compiled module or loads it if not present.
func (mc *ModuleCache) GetCompiledModule(wasmFile string) (wazero.CompiledModule, error) {
    mc.mu.RLock()
    compiledModule, found := mc.cache[wasmFile]
    mc.mu.RUnlock()
    if found {
        return compiledModule, nil
    }

    // Load and compile the module
    wasmBytes, err := os.ReadFile(wasmFile)
    if err != nil {
        return nil, fmt.Errorf("failed to read WASM file: %v", err)
    }
    compiledModule, err = mc.rt.CompileModule(context.Background(), wasmBytes)
    if err != nil {
        return nil, fmt.Errorf("failed to compile module: %v", err)
    }

    mc.mu.Lock()
    mc.cache[wasmFile] = compiledModule
    mc.mu.Unlock()
    return compiledModule, nil
}

func main() {
    // Load config
    config, err := NewConfig("config.json")
    if err != nil {
        log.Fatalf("Error loading config: %v", err)
    }
    
    // Initialize caches
    moduleCache := NewModuleCache()
    defer moduleCache.rt.Close(context.Background())
    responseCache := NewResponseCache(config.CacheSize)
    
    // Start the server
    server := &Server{
        config:      config,
        moduleCache: moduleCache,
        cache:       responseCache,
    }
    log.Printf("Starting server on port %s...", config.Port)
    if err := http.ListenAndServe(":"+config.Port, server); err != nil {
        log.Fatalf("Server failed: %v", err)
    }
}
