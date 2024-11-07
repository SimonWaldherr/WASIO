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
	CacheTTL  int              `json:"cache_ttl"`
	CacheSize int              `json:"cache_size"`
}

// Route defines a server route mapped to a WASM instrument.
type Route struct {
	Path       string `json:"path"`
	WasmFile   string `json:"wasm_file"`
	Cache      bool   `json:"cache"`
	TTL        int    `json:"ttl"`
	Filesystem struct {
		Mount string `json:"mount"`
		Path  string `json:"path"`
	} `json:"filesystem"`
}

// Server represents the main server with configuration, caching, and Instruments.
type Server struct {
	config      *Config
	moduleCache *ModuleCache
	cache       *ResponseCache
}

// ModuleCache manages cached compiled modules.
type ModuleCache struct {
	cache map[string]wazero.CompiledModule
	mu    sync.RWMutex
	rt    wazero.Runtime
}

// ResponseCache manages cached responses with TTLs.
type ResponseCache struct {
	data map[string]CachedResponse
	mu   sync.RWMutex
}

// CachedResponse stores a cached response and expiration.
type CachedResponse struct {
	Value      []byte
	Expiration time.Time
}

// RequestPayload represents data sent to WASM.
type RequestPayload struct {
	Params map[string]string `json:"params"`
	Seed   int64             `json:"seed"`
}

// NewConfig loads configuration from a JSON file.
func NewConfig(filename string) (*Config, error) {
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %v", err)
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %v", err)
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

// GetCachedResponse retrieves a cached response if available and valid.
func (rc *ResponseCache) GetCachedResponse(key string) ([]byte, bool) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	if res, found := rc.data[key]; found && time.Now().Before(res.Expiration) {
		return res.Value, true
	}
	return nil, false
}

// SetCachedResponse saves a response in the cache with a specified TTL.
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
	route, exists := s.config.Routes[r.URL.Path]
	if !exists {
		http.Error(w, "404 - Not Found", http.StatusNotFound)
		return
	}

	cacheKey := r.URL.Path + r.URL.RawQuery
	if route.Cache {
		if cached, found := s.cache.GetCachedResponse(cacheKey); found {
			w.Write(cached)
			return
		}
	}

	payload := RequestPayload{
		Params: map[string]string{},
		Seed:   time.Now().UnixNano(),
	}
	for key, values := range r.URL.Query() {
		payload.Params[key] = values[0]
	}

	output := &bytes.Buffer{}
	err := s.moduleCache.RunInstrument(route, payload, output)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error running module: %v", err), http.StatusInternalServerError)
		return
	}

	response := output.Bytes()
	if route.Cache {
		ttl := s.config.CacheTTL
		if route.TTL > 0 {
			ttl = route.TTL
		}
		s.cache.SetCachedResponse(cacheKey, response, ttl)
	}
	w.Write(response)
}

// RunInstrument executes an instrument with enhanced memory management.
func (mc *ModuleCache) RunInstrument(route Route, payload RequestPayload, output io.Writer) error {
    compiledModule, err := mc.GetCompiledModule(route.WasmFile)
    if err != nil {
        return err
    }
    
    ctx := context.Background()
    moduleConfig := wazero.NewModuleConfig().
        WithStdin(bytes.NewReader(serializePayload(payload))).
        WithStdout(output)
    
    // If filesystem configuration is specified, mount the directory
    if route.Filesystem.Mount != "" && route.Filesystem.Path != "" {
        fsConfig := wazero.NewFSConfig().WithDirMount(route.Filesystem.Path, route.Filesystem.Mount)
        moduleConfig = moduleConfig.WithFSConfig(fsConfig)
    }
    
    mod, err := mc.rt.InstantiateModule(ctx, compiledModule, moduleConfig)
    if err != nil {
        return fmt.Errorf("failed to instantiate module: %v", err)
    }
    defer mod.Close(ctx)
    
    _, err = mod.ExportedFunction("_start").Call(ctx)
    return err
}


// GetCompiledModule returns a cached compiled module or loads it if not present.
func (mc *ModuleCache) GetCompiledModule(wasmFile string) (wazero.CompiledModule, error) {
	mc.mu.RLock()
	compiledModule, found := mc.cache[wasmFile]
	mc.mu.RUnlock()
	if found {
		return compiledModule, nil
	}

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

// serializePayload encodes payload as JSON for structured data transfer.
func serializePayload(payload RequestPayload) []byte {
	data, _ := json.Marshal(payload)
	return data
}

func main() {
	config, err := NewConfig("config.json")
	if err != nil {
		log.Fatalf("Error loading config: %v", err)
	}

	moduleCache := NewModuleCache()
	defer moduleCache.rt.Close(context.Background())
	responseCache := NewResponseCache(config.CacheSize)

	server := &Server{config: config, moduleCache: moduleCache, cache: responseCache}
	log.Printf("Starting WASIO on port %s...", config.Port)
	if err := http.ListenAndServe(":"+config.Port, server); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
