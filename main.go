// main.go
//
// WASIO: WebAssembly System Interface Orchestrator
//
// A self‑contained HTTP server that dynamically loads and executes
// WebAssembly (WASM) modules (instruments) in response to HTTP requests.
// Features:
//   - Dynamic routing based on JSON config
//   - In‑memory compiled‐module LRU cache
//   - Optional per‑route response caching with TTL
//   - Controlled filesystem mounts for instruments
//   - Graceful shutdown on SIGINT/SIGTERM
//   - Apache‑style access logging middleware

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	htmlpkg "html"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// ServerStats tracks server metrics
type ServerStats struct {
	mu                sync.RWMutex
	StartTime         time.Time        `json:"start_time"`
	TotalRequests     int64            `json:"total_requests"`
	SuccessRequests   int64            `json:"success_requests"`
	ErrorRequests     int64            `json:"error_requests"`
	CacheHits         int64            `json:"cache_hits"`
	CacheMisses       int64            `json:"cache_misses"`
	ModuleCacheHits   int64            `json:"module_cache_hits"`
	ModuleCacheMiss   int64            `json:"module_cache_miss"`
	RouteStats        map[string]int64 `json:"route_stats"`
	AverageResponse   time.Duration    `json:"average_response_time"`
	totalResponseTime time.Duration
}

func NewServerStats() *ServerStats {
	return &ServerStats{
		StartTime:  time.Now(),
		RouteStats: make(map[string]int64),
	}
}

func (s *ServerStats) IncrementRequest(route string, success bool, responseTime time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.TotalRequests++
	if success {
		s.SuccessRequests++
	} else {
		s.ErrorRequests++
	}

	s.RouteStats[route]++
	s.totalResponseTime += responseTime
	s.AverageResponse = s.totalResponseTime / time.Duration(s.TotalRequests)
}

func (s *ServerStats) IncrementCacheHit() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CacheHits++
}

func (s *ServerStats) IncrementCacheMiss() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CacheMisses++
}

func (s *ServerStats) IncrementModuleCacheHit() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ModuleCacheHits++
}

func (s *ServerStats) IncrementModuleCacheMiss() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ModuleCacheMiss++
}

func (s *ServerStats) GetStats() ServerStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Create a copy to return
	routeStatsCopy := make(map[string]int64)
	for k, v := range s.RouteStats {
		routeStatsCopy[k] = v
	}

	return ServerStats{
		StartTime:       s.StartTime,
		TotalRequests:   s.TotalRequests,
		SuccessRequests: s.SuccessRequests,
		ErrorRequests:   s.ErrorRequests,
		CacheHits:       s.CacheHits,
		CacheMisses:     s.CacheMisses,
		ModuleCacheHits: s.ModuleCacheHits,
		ModuleCacheMiss: s.ModuleCacheMiss,
		RouteStats:      routeStatsCopy,
		AverageResponse: s.AverageResponse,
	}
}

// Route defines a single HTTP endpoint mapped to a WASM module.
type RouteExample struct {
	Label string `json:"label"`
	Query string `json:"query"`
}

type Route struct {
	// Path to the compiled WebAssembly module (WASI target).
	WASMFile string `json:"wasm_file"`

	// Enable in‑memory response caching for this route.
	Cache bool `json:"cache"`

	// TTL for response cache in seconds (overrides global TTL if > 0).
	TTL int `json:"ttl"`

	// Filesystem mount configuration exposed to the guest.
	Filesystem struct {
		Mount string `json:"mount"` // guest mount point, e.g. "/data"
		Path  string `json:"path"`  // host directory, e.g. "./data"
	} `json:"filesystem"`

	// Metadata for display and documentation
	Description string         `json:"description,omitempty"` // Human-readable description
	Category    string         `json:"category,omitempty"`    // Category for grouping (Basic, Math, etc.)
	Example     string         `json:"example,omitempty"`     // Example query parameters or usage
	Examples    []RouteExample `json:"examples,omitempty"`    // Additional example queries shown on the index page
	UseCase     string         `json:"use_case,omitempty"`    // Practical use case shown on the index page

	// Configuration data passed to the WASM module
	Config interface{} `json:"config,omitempty"` // Module-specific configuration

	// Methods restricts this route to the listed HTTP methods (e.g. ["GET","POST"]).
	// An empty slice allows all methods.
	Methods []string `json:"methods,omitempty"`

	// Env contains environment variables injected into the WASM module.
	Env map[string]string `json:"env,omitempty"`
}

// CORSConfig holds Cross-Origin Resource Sharing settings applied globally.
type CORSConfig struct {
	Enabled        bool     `json:"enabled"`
	AllowedOrigins []string `json:"allowed_origins"` // "*" to allow all
	AllowedMethods []string `json:"allowed_methods"`
	AllowedHeaders []string `json:"allowed_headers"`
	MaxAge         int      `json:"max_age"` // Preflight cache in seconds
}

// Config represents the server configuration loaded from JSON.
type Config struct {
	Port       string           `json:"port"`       // HTTP listen port, default "8080"
	CacheTTL   int              `json:"cache_ttl"`  // Global response cache TTL in seconds
	CacheSize  int              `json:"cache_size"` // Max entries for both module & response cache
	IndexPage  bool             `json:"index_page"` // Enable index page (default: true)
	Monitoring bool             `json:"monitoring"` // Enable monitoring endpoint (default: true)
	CORS       CORSConfig       `json:"cors"`       // Global CORS configuration
	Routes     map[string]Route `json:"routes"`     // Map URL paths to Route settings
}

// LoadConfig reads and validates configuration from the given file path.
func LoadConfig(path string) (*Config, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	var cfg Config
	if err = json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	// Apply defaults
	if cfg.Port == "" {
		cfg.Port = "8080"
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = 300
	}
	if cfg.CacheSize <= 0 {
		cfg.CacheSize = 1024
	}

	// Check if index_page and monitoring were explicitly set in JSON
	var rawConfig map[string]interface{}
	json.Unmarshal(data, &rawConfig)
	if _, exists := rawConfig["index_page"]; !exists {
		cfg.IndexPage = true // Default to true
	}
	if _, exists := rawConfig["monitoring"]; !exists {
		cfg.Monitoring = true // Default to true
	}
	return &cfg, nil
}

// cachedModule stores a compiled WASM module together with the file's
// modification time at the moment of compilation, enabling hot-reload.
type cachedModule struct {
	mod     wazero.CompiledModule
	modTime time.Time
}

// ModuleCache caches compiled WASM modules with mtime-based invalidation.
type ModuleCache struct {
	mu    sync.RWMutex
	rt    wazero.Runtime
	cache map[string]cachedModule
	size  int
}

// NewModuleCache constructs a ModuleCache with given max size.
func NewModuleCache(ctx context.Context, size int) *ModuleCache {
	rt := wazero.NewRuntime(ctx)
	wasi_snapshot_preview1.MustInstantiate(ctx, rt)
	return &ModuleCache{
		rt:    rt,
		cache: make(map[string]cachedModule, size),
		size:  size,
	}
}

// Evict removes a single compiled module from the cache.
func (m *ModuleCache) Evict(wasmPath string) {
	m.mu.Lock()
	delete(m.cache, wasmPath)
	m.mu.Unlock()
}

// FlushAll empties the entire compiled-module cache.
func (m *ModuleCache) FlushAll() {
	m.mu.Lock()
	m.cache = make(map[string]cachedModule, m.size)
	m.mu.Unlock()
}

// Get returns a compiled module, compiling and caching it if needed.
// If the .wasm file on disk is newer than the cached version, the cache entry
// is automatically invalidated and the module is recompiled (hot-reload).
func (m *ModuleCache) Get(ctx context.Context, wasmPath string, stats *ServerStats) (wazero.CompiledModule, error) {
	// Stat the file outside the lock to avoid holding it during I/O.
	fileInfo, statErr := os.Stat(wasmPath)

	m.mu.RLock()
	entry, ok := m.cache[wasmPath]
	m.mu.RUnlock()

	if ok {
		// Invalidate when the file has been modified since compilation.
		stale := statErr == nil && fileInfo.ModTime().After(entry.modTime)
		if !stale {
			if stats != nil {
				stats.IncrementModuleCacheHit()
			}
			return entry.mod, nil
		}
		log.Printf("hot-reload: %s changed, recompiling", wasmPath)
	}

	if stats != nil {
		stats.IncrementModuleCacheMiss()
	}

	var modTime time.Time
	if statErr == nil {
		modTime = fileInfo.ModTime()
	}

	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		return nil, fmt.Errorf("read wasm file: %w", err)
	}
	mod, err := m.rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		return nil, fmt.Errorf("compile wasm module: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.cache) >= m.size {
		// Evict one arbitrary item
		for k := range m.cache {
			delete(m.cache, k)
			break
		}
	}
	m.cache[wasmPath] = cachedModule{mod: mod, modTime: modTime}
	return mod, nil
}

// cachedResponse stores a response payload and its expiration time.
type cachedResponse struct {
	data      []byte
	expiresAt time.Time
}

// ResponseCache holds cached HTTP responses with TTL and eviction.
type ResponseCache struct {
	mu    sync.RWMutex
	cache map[string]cachedResponse
	size  int
}

// NewResponseCache constructs a ResponseCache with given max size.
func NewResponseCache(size int) *ResponseCache {
	return &ResponseCache{
		cache: make(map[string]cachedResponse, size),
		size:  size,
	}
}

// FlushAll empties the entire response cache.
func (r *ResponseCache) FlushAll() {
	r.mu.Lock()
	r.cache = make(map[string]cachedResponse, r.size)
	r.mu.Unlock()
}

// Get retrieves a cached response if present and not expired.
func (r *ResponseCache) Get(key string, stats *ServerStats) ([]byte, bool) {
	r.mu.RLock()
	cr, ok := r.cache[key]
	r.mu.RUnlock()
	if !ok || time.Now().After(cr.expiresAt) {
		if stats != nil {
			stats.IncrementCacheMiss()
		}
		return nil, false
	}
	if stats != nil {
		stats.IncrementCacheHit()
	}
	return cr.data, true
}

// Set caches a response under the given key for ttl duration.
// Evicts one arbitrary entry if cache is full.
func (r *ResponseCache) Set(key string, data []byte, ttl time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.cache) >= r.size {
		for k := range r.cache {
			delete(r.cache, k)
			break
		}
	}
	r.cache[key] = cachedResponse{
		data:      data,
		expiresAt: time.Now().Add(ttl),
	}
}

// Server is the main HTTP server with configuration, caches, and context.
type Server struct {
	cfg    *Config
	modC   *ModuleCache
	respC  *ResponseCache
	stats  *ServerStats
	ctx    context.Context
	cancel context.CancelFunc
}

// requestPayload is the JSON structure sent to the WASM module on stdin.
type requestPayload struct {
	Params map[string]string `json:"params"`
	Seed   int64             `json:"seed"`
}

// NewServer initializes a Server with caches and context for shutdown.
func NewServer(cfg *Config) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		cfg:    cfg,
		modC:   NewModuleCache(ctx, cfg.CacheSize),
		respC:  NewResponseCache(cfg.CacheSize),
		stats:  NewServerStats(),
		ctx:    ctx,
		cancel: cancel,
	}
}

// healthHandler responds with 200 OK for liveness probes.
func (s *Server) healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`OK`))
}

// reloadHandler flushes the compiled-module cache (and optionally the response
// cache) without restarting the server.  Any subsequent request will recompile
// WASM files from disk, picking up newly built .wasm binaries automatically.
//
//	GET /_reload          – flush module cache only
//	GET /_reload?all=1    – flush module cache + response cache
//	GET /_reload?route=/x – flush module cache entry for a single route
func (s *Server) reloadHandler(w http.ResponseWriter, r *http.Request) {
	type result struct {
		Flushed  []string `json:"flushed"`
		RespCache bool    `json:"response_cache_flushed"`
	}

	q := r.URL.Query()
	res := result{}

	if routePath := q.Get("route"); routePath != "" {
		// Targeted: evict one specific module
		route, ok := s.cfg.Routes[routePath]
		if !ok {
			http.Error(w, "route not found", http.StatusNotFound)
			return
		}
		s.modC.Evict(route.WASMFile)
		res.Flushed = []string{route.WASMFile}
		log.Printf("reload: evicted module cache for %s (%s)", routePath, route.WASMFile)
	} else {
		// Global: evict all compiled modules
		paths := make([]string, 0, len(s.cfg.Routes))
		for _, route := range s.cfg.Routes {
			paths = append(paths, route.WASMFile)
		}
		s.modC.FlushAll()
		res.Flushed = paths
		log.Printf("reload: flushed all %d compiled module(s)", len(paths))
	}

	if q.Get("all") == "1" {
		s.respC.FlushAll()
		res.RespCache = true
		log.Print("reload: flushed response cache")
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// ── LLM native handlers ───────────────────────────────────────────────────────
//
// WASI preview1 has no socket support, so all outbound HTTP calls to the LLM
// API (e.g. LM Studio / OpenAI-compatible endpoints) are handled here in the
// WASIO host rather than inside the llm.wasm module.
//
// Configuration is read from the /llm route's "env" block in config.json:
//   OPENAI_BASE_URL  – base URL of the OpenAI-compatible API
//   OPENAI_API_KEY   – API key (e.g. "lm-studio" for LM Studio)
//   OPENAI_MODEL     – default model name (empty = picked by server)
//   OPENAI_SYSTEM    – system prompt shown to the model

type llmSettings struct {
	BaseURL      string
	APIKey       string
	Model        string
	SystemPrompt string
}

func (s *Server) llmBaseConfig() llmSettings {
	c := llmSettings{
		BaseURL:      "http://localhost:1234/v1",
		APIKey:       "lm-studio",
		Model:        "",
		SystemPrompt: "You are a helpful assistant.",
	}
	if route, ok := s.cfg.Routes["/llm"]; ok {
		if v := route.Env["OPENAI_BASE_URL"]; v != "" {
			c.BaseURL = v
		}
		if v := route.Env["OPENAI_API_KEY"]; v != "" {
			c.APIKey = v
		}
		if v := route.Env["OPENAI_MODEL"]; v != "" {
			c.Model = v
		}
		if v := route.Env["OPENAI_SYSTEM"]; v != "" {
			c.SystemPrompt = v
		}
	}
	return c
}

// newLLMClient returns a pre-configured HTTP client for LLM API calls.
func newLLMClient() *http.Client {
	return &http.Client{Timeout: 120 * time.Second}
}

// llmModelsHandler handles GET /_llm/models and proxies to {baseURL}/models.
func (s *Server) llmModelsHandler(w http.ResponseWriter, r *http.Request) {
	cfg := s.llmBaseConfig()
	client := newLLMClient()

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, cfg.BaseURL+"/models", nil)
	if err != nil {
		http.Error(w, `{"error":"failed to build request"}`, http.StatusInternalServerError)
		return
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprintf(w, `{"error":"LLM API unreachable: %s"}`, strings.ReplaceAll(err.Error(), `"`, `'`))
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, `{"error":"failed to read response"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

// llmChatHandler handles GET /_llm/chat and forwards to {baseURL}/chat/completions.
//
// Query parameters:
//
//	message    – the user's current message (required)
//	system     – override system prompt (optional)
//	model      – override model name (optional)
//	temperature – float, default 0.7 (optional)
//	max_tokens  – integer, default 2048 (optional)
//	history    – JSON array of {"role":"user"|"assistant","content":"..."} (optional)
func (s *Server) llmChatHandler(w http.ResponseWriter, r *http.Request) {
	cfg := s.llmBaseConfig()
	q := r.URL.Query()

	message := q.Get("message")
	if message == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"missing message parameter"}`)
		return
	}

	systemPrompt := cfg.SystemPrompt
	if s := q.Get("system"); s != "" {
		systemPrompt = s
	}
	model := cfg.Model
	if m := q.Get("model"); m != "" {
		model = m
	}
	temperature := 0.7
	if t := q.Get("temperature"); t != "" {
		if parsed, err := fmt.Sscanf(t, "%f", &temperature); parsed != 1 || err != nil {
			temperature = 0.7
		}
	}
	maxTokens := 2048
	if mt := q.Get("max_tokens"); mt != "" {
		if parsed, err := fmt.Sscanf(mt, "%d", &maxTokens); parsed != 1 || err != nil {
			maxTokens = 2048
		}
	}

	type chatMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	messages := []chatMessage{{Role: "system", Content: systemPrompt}}

	// Inject history if provided
	if histJSON := q.Get("history"); histJSON != "" {
		var history []chatMessage
		if err := json.Unmarshal([]byte(histJSON), &history); err == nil {
			messages = append(messages, history...)
		}
	}
	messages = append(messages, chatMessage{Role: "user", Content: message})

	type chatRequest struct {
		Model       string        `json:"model,omitempty"`
		Messages    []chatMessage `json:"messages"`
		Temperature float64       `json:"temperature"`
		MaxTokens   int           `json:"max_tokens"`
	}

	payload := chatRequest{
		Model:       model,
		Messages:    messages,
		Temperature: temperature,
		MaxTokens:   maxTokens,
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, `{"error":"failed to marshal request"}`, http.StatusInternalServerError)
		return
	}

	client := newLLMClient()
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		cfg.BaseURL+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		http.Error(w, `{"error":"failed to build request"}`, http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprintf(w, `{"error":"LLM API unreachable: %s"}`, strings.ReplaceAll(err.Error(), `"`, `'`))
		return
	}
	defer resp.Body.Close()

	var apiResp struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, `{"error":"failed to parse LLM API response"}`)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	if apiResp.Error != nil {
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": apiResp.Error.Message})
		return
	}

	if len(apiResp.Choices) == 0 {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, `{"error":"no choices returned by LLM API"}`)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"content": apiResp.Choices[0].Message.Content,
		"role":    apiResp.Choices[0].Message.Role,
		"model":   apiResp.Model,
		"usage":   apiResp.Usage,
	})
}

// indexHandler serves the main index page with all active instruments.
func (s *Server) indexHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	html := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>WASIO - WebAssembly System Interface Orchestrator</title>
    <link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.2/dist/css/bootstrap.min.css" rel="stylesheet">
    <style>
        .instrument-card { transition: transform 0.2s; }
        .instrument-card:hover { transform: translateY(-2px); }
        .stats-card { background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); color: white; }
        .stat-number { font-size: 2rem; font-weight: bold; }
        .example-code { display: block; white-space: normal; word-break: break-all; }
    </style>
</head>
<body>
    <nav class="navbar navbar-expand-lg navbar-dark bg-primary">
        <div class="container">
            <a class="navbar-brand" href="/">
                <strong>WASIO</strong> <small>WebAssembly System Interface Orchestrator</small>
            </a>
            <div class="navbar-nav ms-auto">
                <a class="nav-link" href="/monitoring">📊 Monitoring</a>
                <a class="nav-link" href="/_reload">🔄 Reload</a>
                <a class="nav-link" href="/health">❤️ Health</a>
            </div>
        </div>
    </nav>

    <div class="container mt-4">
        <div class="row">
            <div class="col-12">
                <h1>Welcome to WASIO</h1>
                <p class="lead">Dynamically execute WebAssembly instruments through HTTP requests</p>
            </div>
        </div>`

	// Add quick stats if monitoring is enabled
	if s.cfg.Monitoring {
		stats := s.stats.GetStats()
		uptime := time.Since(stats.StartTime)

		html += fmt.Sprintf(`
        <div class="row mb-4">
            <div class="col-md-3">
                <div class="card stats-card">
                    <div class="card-body text-center">
                        <div class="stat-number">%d</div>
                        <div>Total Requests</div>
                    </div>
                </div>
            </div>
            <div class="col-md-3">
                <div class="card stats-card">
                    <div class="card-body text-center">
                        <div class="stat-number">%d</div>
                        <div>Active Routes</div>
                    </div>
                </div>
            </div>
            <div class="col-md-3">
                <div class="card stats-card">
                    <div class="card-body text-center">
                        <div class="stat-number">%.1f%%</div>
                        <div>Cache Hit Rate</div>
                    </div>
                </div>
            </div>
            <div class="col-md-3">
                <div class="card stats-card">
                    <div class="card-body text-center">
                        <div class="stat-number">%s</div>
                        <div>Uptime</div>
                    </div>
                </div>
            </div>
        </div>`,
			stats.TotalRequests,
			len(s.cfg.Routes),
			func() float64 {
				total := stats.CacheHits + stats.CacheMisses
				if total == 0 {
					return 0
				}
				return float64(stats.CacheHits) / float64(total) * 100
			}(),
			formatDuration(uptime),
		)
	}

	html += `
        <div class="row">
            <div class="col-12">
                <h2>Available Instruments</h2>
                <p>Click on any instrument to test it or view its documentation.</p>
            </div>
        </div>
        
        <div class="row">`

	// List all available routes in a stable order
	paths := make([]string, 0, len(s.cfg.Routes))
	for path := range s.cfg.Routes {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		route := s.cfg.Routes[path]
		// Try to determine instrument type and description
		instrumentName := strings.TrimPrefix(path, "/")
		description := getInstrumentDescription(instrumentName, route)
		category := getInstrumentCategory(instrumentName, route)
		useCase := getInstrumentUseCase(instrumentName, route)
		exampleActions := renderExampleActions(r.Host, path, getInstrumentExamples(path, route))

		html += fmt.Sprintf(`
            <div class="col-md-6 col-lg-4 mb-3">
                <div class="card instrument-card h-100">
                    <div class="card-body">
                        <h5 class="card-title">
                            %s <span class="badge bg-secondary">%s</span>
                        </h5>
                        <p class="card-text">%s</p>
                        <p class="small mb-3"><strong>Use case:</strong> %s</p>
                        <div class="mb-2">
                            <small class="text-muted">
                                📁 %s<br>
                                🎯 Cache: %t<br>
                                ⏱️ TTL: %ds
                            </small>
                        </div>
                        <div class="mb-3">
                            <small class="text-muted d-block mb-2">Example flows</small>
                            %s
                        </div>
                        <div class="d-flex flex-wrap gap-1">
                            <a href="%s" class="btn btn-outline-primary btn-sm" target="_blank">Base URL</a>
                        </div>
                    </div>
                </div>
            </div>`,
			htmlpkg.EscapeString(instrumentName),
			htmlpkg.EscapeString(category),
			htmlpkg.EscapeString(description),
			htmlpkg.EscapeString(useCase),
			htmlpkg.EscapeString(route.WASMFile),
			route.Cache,
			getTTL(route, s.cfg.CacheTTL),
			exampleActions,
			htmlpkg.EscapeString(buildExamplePath(path, "")),
		)
	}

	html += `
        </div>
    </div>

    <footer class="bg-light mt-5 py-4">
        <div class="container text-center">
            <p class="mb-0">
                <strong>WASIO</strong> - WebAssembly System Interface Orchestrator<br>
                <small class="text-muted">Powered by <a href="https://github.com/tetratelabs/wazero">Wazero</a> WebAssembly runtime</small>
            </p>
        </div>
    </footer>

    <script>
        function copyUrl(url, button) {
            navigator.clipboard.writeText(url).then(() => {
                const originalText = button.textContent;
                button.textContent = 'Copied!';
                button.classList.remove('btn-outline-secondary');
                button.classList.add('btn-success');
                setTimeout(() => {
                    button.textContent = originalText;
                    button.classList.remove('btn-success');
                    button.classList.add('btn-outline-secondary');
                }, 2000);
            });
        }
    </script>
</body>
</html>`

	w.Write([]byte(html))
}

// monitoringHandler serves detailed server statistics and monitoring information.
func (s *Server) monitoringHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("format") == "json" {
		w.Header().Set("Content-Type", "application/json")
		stats := s.stats.GetStats()
		// Create a copy without mutex for JSON serialization
		statsForJSON := struct {
			StartTime       time.Time        `json:"start_time"`
			TotalRequests   int64            `json:"total_requests"`
			SuccessRequests int64            `json:"success_requests"`
			ErrorRequests   int64            `json:"error_requests"`
			CacheHits       int64            `json:"cache_hits"`
			CacheMisses     int64            `json:"cache_misses"`
			ModuleCacheHits int64            `json:"module_cache_hits"`
			ModuleCacheMiss int64            `json:"module_cache_miss"`
			RouteStats      map[string]int64 `json:"route_stats"`
			AverageResponse string           `json:"average_response_time"`
			Uptime          string           `json:"uptime"`
		}{
			StartTime:       stats.StartTime,
			TotalRequests:   stats.TotalRequests,
			SuccessRequests: stats.SuccessRequests,
			ErrorRequests:   stats.ErrorRequests,
			CacheHits:       stats.CacheHits,
			CacheMisses:     stats.CacheMisses,
			ModuleCacheHits: stats.ModuleCacheHits,
			ModuleCacheMiss: stats.ModuleCacheMiss,
			RouteStats:      stats.RouteStats,
			AverageResponse: stats.AverageResponse.String(),
			Uptime:          formatDuration(time.Since(stats.StartTime)),
		}
		json.NewEncoder(w).Encode(statsForJSON)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	stats := s.stats.GetStats()
	uptime := time.Since(stats.StartTime)

	html := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>WASIO Monitoring Dashboard</title>
    <link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.2/dist/css/bootstrap.min.css" rel="stylesheet">
    <style>
        .metric-card { border-left: 4px solid #007bff; }
        .refresh-indicator { opacity: 0.7; }
    </style>
</head>
<body>
    <nav class="navbar navbar-expand-lg navbar-dark bg-primary">
        <div class="container">
            <a class="navbar-brand" href="/">
                <strong>WASIO</strong> Monitoring Dashboard
            </a>
            <div class="navbar-nav ms-auto">
                <a class="nav-link" href="/">🏠 Home</a>
                <a class="nav-link" href="/_reload">🔄 Reload</a>
                <a class="nav-link" href="/monitoring?format=json">📄 JSON</a>
            </div>
        </div>
    </nav>

    <div class="container mt-4">
        <div class="row">
            <div class="col-12">
                <div class="d-flex justify-content-between align-items-center mb-4">
                    <h1>Server Statistics</h1>
                    <div>
                        <button class="btn btn-primary" onclick="location.reload()">🔄 Refresh</button>
                        <small class="text-muted refresh-indicator">Auto-refresh in <span id="countdown">30</span>s</small>
                    </div>
                </div>
            </div>
        </div>

        <!-- Overview Stats -->
        <div class="row mb-4">
            <div class="col-md-3">
                <div class="card metric-card">
                    <div class="card-body">
                        <h5 class="card-title">Uptime</h5>
                        <h2 class="text-primary">%s</h2>
                        <small class="text-muted">Since %s</small>
                    </div>
                </div>
            </div>
            <div class="col-md-3">
                <div class="card metric-card">
                    <div class="card-body">
                        <h5 class="card-title">Total Requests</h5>
                        <h2 class="text-primary">%d</h2>
                        <small class="text-muted">Success: %d | Errors: %d</small>
                    </div>
                </div>
            </div>
            <div class="col-md-3">
                <div class="card metric-card">
                    <div class="card-body">
                        <h5 class="card-title">Average Response</h5>
                        <h2 class="text-primary">%s</h2>
                        <small class="text-muted">Response time</small>
                    </div>
                </div>
            </div>
            <div class="col-md-3">
                <div class="card metric-card">
                    <div class="card-body">
                        <h5 class="card-title">Success Rate</h5>
                        <h2 class="text-primary">%.1f%%</h2>
                        <small class="text-muted">Request success rate</small>
                    </div>
                </div>
            </div>
        </div>

        <!-- Cache Statistics -->
        <div class="row mb-4">
            <div class="col-md-6">
                <div class="card">
                    <div class="card-header">
                        <h5 class="mb-0">Response Cache</h5>
                    </div>
                    <div class="card-body">
                        <div class="row">
                            <div class="col-6">
                                <div class="text-center">
                                    <h3 class="text-success">%d</h3>
                                    <small>Cache Hits</small>
                                </div>
                            </div>
                            <div class="col-6">
                                <div class="text-center">
                                    <h3 class="text-warning">%d</h3>
                                    <small>Cache Misses</small>
                                </div>
                            </div>
                        </div>
                        <div class="mt-3">
                            <div class="progress">
                                <div class="progress-bar bg-success" style="width: %.1f%%"></div>
                            </div>
                            <small class="text-muted">Hit Rate: %.1f%%</small>
                        </div>
                    </div>
                </div>
            </div>
            <div class="col-md-6">
                <div class="card">
                    <div class="card-header">
                        <h5 class="mb-0">Module Cache</h5>
                    </div>
                    <div class="card-body">
                        <div class="row">
                            <div class="col-6">
                                <div class="text-center">
                                    <h3 class="text-success">%d</h3>
                                    <small>Module Hits</small>
                                </div>
                            </div>
                            <div class="col-6">
                                <div class="text-center">
                                    <h3 class="text-warning">%d</h3>
                                    <small>Module Misses</small>
                                </div>
                            </div>
                        </div>
                        <div class="mt-3">
                            <div class="progress">
                                <div class="progress-bar bg-info" style="width: %.1f%%"></div>
                            </div>
                            <small class="text-muted">Hit Rate: %.1f%%</small>
                        </div>
                    </div>
                </div>
            </div>
        </div>

        <!-- Route Statistics -->
        <div class="row">
            <div class="col-12">
                <div class="card">
                    <div class="card-header">
                        <h5 class="mb-0">Route Statistics</h5>
                    </div>
                    <div class="card-body">
                        <div class="table-responsive">
                            <table class="table table-striped">
                                <thead>
                                    <tr>
                                        <th>Route</th>
                                        <th>Requests</th>
                                        <th>WASM File</th>
                                        <th>Cache Enabled</th>
                                        <th>TTL</th>
                                    </tr>
                                </thead>
                                <tbody>`,
		formatDuration(uptime),
		stats.StartTime.Format("2006-01-02 15:04:05"),
		stats.TotalRequests,
		stats.SuccessRequests,
		stats.ErrorRequests,
		stats.AverageResponse.String(),
		func() float64 {
			if stats.TotalRequests == 0 {
				return 100.0
			}
			return float64(stats.SuccessRequests) / float64(stats.TotalRequests) * 100
		}(),
		stats.CacheHits,
		stats.CacheMisses,
		func() float64 {
			total := stats.CacheHits + stats.CacheMisses
			if total == 0 {
				return 0
			}
			return float64(stats.CacheHits) / float64(total) * 100
		}(),
		func() float64 {
			total := stats.CacheHits + stats.CacheMisses
			if total == 0 {
				return 0
			}
			return float64(stats.CacheHits) / float64(total) * 100
		}(),
		stats.ModuleCacheHits,
		stats.ModuleCacheMiss,
		func() float64 {
			total := stats.ModuleCacheHits + stats.ModuleCacheMiss
			if total == 0 {
				return 0
			}
			return float64(stats.ModuleCacheHits) / float64(total) * 100
		}(),
		func() float64 {
			total := stats.ModuleCacheHits + stats.ModuleCacheMiss
			if total == 0 {
				return 0
			}
			return float64(stats.ModuleCacheHits) / float64(total) * 100
		}(),
	)

	// Add route statistics
	for path, route := range s.cfg.Routes {
		requests := stats.RouteStats[path]
		html += fmt.Sprintf(`
                                    <tr>
                                        <td><a href="%s">%s</a></td>
                                        <td>%d</td>
                                        <td><code>%s</code></td>
                                        <td>%s</td>
                                        <td>%ds</td>
                                    </tr>`,
			path, path, requests, route.WASMFile,
			func() string {
				if route.Cache {
					return `<span class="badge bg-success">Yes</span>`
				}
				return `<span class="badge bg-secondary">No</span>`
			}(),
			getTTL(route, s.cfg.CacheTTL),
		)
	}

	html += `
                                </tbody>
                            </table>
                        </div>
                    </div>
                </div>
            </div>
        </div>
    </div>

    <script>
        let countdown = 30;
        setInterval(() => {
            countdown--;
            document.getElementById('countdown').textContent = countdown;
            if (countdown <= 0) {
                location.reload();
            }
        }, 1000);
    </script>
</body>
</html>`

	w.Write([]byte(html))
}

// ServeHTTP routes requests to the appropriate WASM module or built-in endpoints.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	path := r.URL.Path
	success := true

	defer func() {
		responseTime := time.Since(start)
		s.stats.IncrementRequest(path, success, responseTime)
	}()

	// Built-in endpoints
	switch path {
	case "/health":
		s.healthHandler(w, r)
		return
	case "/_reload":
		s.reloadHandler(w, r)
		return
	case "/_llm/models":
		s.llmModelsHandler(w, r)
		return
	case "/_llm/chat":
		s.llmChatHandler(w, r)
		return
	case "/":
		if s.cfg.IndexPage {
			s.indexHandler(w, r)
			return
		}
	case "/monitoring", "/stats":
		if s.cfg.Monitoring {
			s.monitoringHandler(w, r)
			return
		}
	}

	// Handle proxy paths specially
	if strings.HasPrefix(path, "/proxy/") {
		s.handleProxyRequest(w, r)
		return
	}

	route, ok := s.cfg.Routes[path]
	if !ok {
		success = false
		http.NotFound(w, r)
		return
	}

	// Enforce allowed HTTP methods when configured
	if len(route.Methods) > 0 {
		allowed := false
		for _, m := range route.Methods {
			if strings.EqualFold(m, r.Method) {
				allowed = true
				break
			}
		}
		if !allowed {
			success = false
			w.Header().Set("Allow", strings.Join(route.Methods, ", "))
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
	}

	key := path + "?" + r.URL.RawQuery
	if route.Cache {
		if data, found := s.respC.Get(key, s.stats); found {
			w.Write(data)
			return
		}
	}

	// Build payload from query parameters and random seed
	params := make(map[string]string, len(r.URL.Query()))
	for k, vs := range r.URL.Query() {
		if len(vs) > 0 {
			params[k] = vs[0]
		}
	}
	seed, _ := readRandomSeed()
	payload := requestPayload{Params: params, Seed: seed}
	stdin, _ := json.Marshal(payload)

	// Execute the WASM module
	var buf bytes.Buffer
	if err := s.runWASM(r.Context(), &route, stdin, &buf); err != nil {
		log.Printf("module error: %v", err)
		success = false
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	output := buf.Bytes()

	// Cache the response if enabled
	if route.Cache {
		ttl := time.Duration(s.cfg.CacheTTL) * time.Second
		if route.TTL > 0 {
			ttl = time.Duration(route.TTL) * time.Second
		}
		s.respC.Set(key, output, ttl)
	}

	w.Write(output)
}

// runWASM loads (or reuses) and instantiates the WASM module, piping stdin/stdout.
func (s *Server) runWASM(ctx context.Context, route *Route, stdin []byte, stdout io.Writer) error {
	mod, err := s.modC.Get(ctx, route.WASMFile, s.stats)
	if err != nil {
		return err
	}

	config := wazero.NewModuleConfig().
		WithStdin(bytes.NewReader(stdin)).
		WithStdout(stdout)

	// Inject per-route environment variables into the WASM module
	for k, v := range route.Env {
		config = config.WithEnv(k, v)
	}

	if route.Filesystem.Mount != "" && route.Filesystem.Path != "" {
		fsCfg := wazero.NewFSConfig().
			WithDirMount(route.Filesystem.Path, route.Filesystem.Mount)
		config = config.WithFSConfig(fsCfg)
	}

	instance, err := s.modC.rt.InstantiateModule(ctx, mod, config)
	if err != nil {
		return fmt.Errorf("instantiate module: %w", err)
	}
	defer instance.Close(ctx)

	_, err = instance.ExportedFunction("_start").Call(ctx)
	var exitErr interface{ ExitCode() uint32 }
	if err != nil && errors.As(err, &exitErr) && exitErr.ExitCode() == 0 {
		// Clean WASI exit(0) is not an error
		return nil
	}
	return err
}

// handleProxyRequest handles proxy requests by delegating to the proxy WASM module
func (s *Server) handleProxyRequest(w http.ResponseWriter, r *http.Request) {
	// Get proxy route configuration
	proxyRoute, ok := s.cfg.Routes["/proxy"]
	if !ok {
		http.Error(w, "Proxy not configured", http.StatusServiceUnavailable)
		return
	}

	// Extract the proxy path
	proxyPath := strings.TrimPrefix(r.URL.Path, "/proxy")
	if proxyPath == "" {
		proxyPath = "/"
	}

	// Build parameters for the proxy WASM module
	params := make(map[string]string)

	// Add proxy configuration if available
	if config, ok := proxyRoute.Config.(map[string]interface{}); ok {
		if configBytes, err := json.Marshal(config); err == nil {
			params["config"] = string(configBytes)
		}
	}

	// Add request details
	params["op"] = "proxy"
	params["path"] = proxyPath
	params["method"] = r.Method
	params["url"] = r.URL.String()
	params["remote_addr"] = r.RemoteAddr

	// Add headers
	for key, values := range r.Header {
		if len(values) > 0 {
			params["header_"+key] = values[0]
		}
	}

	// Add query parameters from original request
	for k, vs := range r.URL.Query() {
		if len(vs) > 0 {
			params[k] = vs[0]
		}
	}

	seed, _ := readRandomSeed()
	payload := requestPayload{Params: params, Seed: seed}
	stdin, _ := json.Marshal(payload)

	// Execute the proxy WASM module
	var buf bytes.Buffer
	if err := s.runWASM(r.Context(), &proxyRoute, stdin, &buf); err != nil {
		log.Printf("proxy module error: %v", err)
		http.Error(w, "proxy error", http.StatusBadGateway)
		return
	}

	// Parse the proxy response
	var proxyResponse map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &proxyResponse); err != nil {
		log.Printf("proxy response parse error: %v", err)
		http.Error(w, "proxy response error", http.StatusBadGateway)
		return
	}

	// Check if proxy was successful
	success, ok := proxyResponse["success"].(bool)
	if !ok || !success {
		errorMsg := "proxy failed"
		if errStr, ok := proxyResponse["error"].(string); ok {
			errorMsg = errStr
		}
		http.Error(w, errorMsg, http.StatusBadGateway)
		return
	}

	// Set response headers if available
	if headers, ok := proxyResponse["headers"].(map[string]interface{}); ok {
		for key, value := range headers {
			if valueStr, ok := value.(string); ok {
				w.Header().Set(key, valueStr)
			}
		}
	}

	// Set status code if available
	statusCode := http.StatusOK
	if code, ok := proxyResponse["status_code"].(float64); ok {
		statusCode = int(code)
	}
	w.WriteHeader(statusCode)

	// Write response body
	if body, ok := proxyResponse["body"].(string); ok {
		w.Write([]byte(body))
	} else {
		// If no body, write the JSON response
		w.Header().Set("Content-Type", "application/json")
		w.Write(buf.Bytes())
	}
}

// loggingResponseWriter wraps http.ResponseWriter to capture status and size.
type loggingResponseWriter struct {
	http.ResponseWriter
	status int
	size   int
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.status = code
	lrw.ResponseWriter.WriteHeader(code)
}

func (lrw *loggingResponseWriter) Write(b []byte) (int, error) {
	if lrw.status == 0 {
		lrw.status = http.StatusOK
	}
	n, err := lrw.ResponseWriter.Write(b)
	lrw.size += n
	return n, err
}

// corsMiddleware applies CORS headers based on the server configuration.
func corsMiddleware(cfg CORSConfig, next http.Handler) http.Handler {
	if !cfg.Enabled {
		return next
	}

	allowedOrigins := cfg.AllowedOrigins
	if len(allowedOrigins) == 0 {
		allowedOrigins = []string{"*"}
	}
	allowedMethods := cfg.AllowedMethods
	if len(allowedMethods) == 0 {
		allowedMethods = []string{"GET", "POST", "OPTIONS"}
	}
	allowedHeaders := cfg.AllowedHeaders
	if len(allowedHeaders) == 0 {
		allowedHeaders = []string{"Content-Type", "Authorization"}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		// Determine whether to reflect the origin or use a wildcard
		allowOrigin := ""
		for _, o := range allowedOrigins {
			if o == "*" {
				allowOrigin = "*"
				break
			}
			if o == origin {
				allowOrigin = origin
				break
			}
		}

		if allowOrigin != "" {
			w.Header().Set("Access-Control-Allow-Origin", allowOrigin)
			w.Header().Set("Access-Control-Allow-Methods", strings.Join(allowedMethods, ", "))
			w.Header().Set("Access-Control-Allow-Headers", strings.Join(allowedHeaders, ", "))
			if cfg.MaxAge > 0 {
				w.Header().Set("Access-Control-Max-Age", fmt.Sprintf("%d", cfg.MaxAge))
			}
		}

		// Short-circuit preflight requests
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// logMiddleware logs each HTTP request in Apache combined log format.
func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lrw := &loggingResponseWriter{ResponseWriter: w}
		next.ServeHTTP(lrw, r)

		// Determine client IP
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}

		// Apache Common Log Format
		log.Printf("%s - - [%s] \"%s %s %s\" %d %d \"%s\" \"%s\"",
			host,
			start.Format("02/Jan/2006:15:04:05 -0700"),
			r.Method, r.RequestURI, r.Proto,
			lrw.status, lrw.size,
			r.Referer(), r.UserAgent(),
		)
	})
}

// readRandomSeed returns a cryptographically random int64.
func readRandomSeed() (int64, error) {
	var seed int64
	if err := binary.Read(rand.Reader, binary.LittleEndian, &seed); err != nil {
		return 0, fmt.Errorf("read random seed: %w", err)
	}
	return seed, nil
}

// formatDuration formats a duration in a human-readable way
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	} else if d < time.Hour {
		return fmt.Sprintf("%.1fm", d.Minutes())
	} else if d < 24*time.Hour {
		return fmt.Sprintf("%.1fh", d.Hours())
	} else {
		return fmt.Sprintf("%.1fd", d.Hours()/24)
	}
}

// getTTL returns the effective TTL for a route
func getTTL(route Route, defaultTTL int) int {
	if route.TTL > 0 {
		return route.TTL
	}
	return defaultTTL
}

// getInstrumentDescription returns a description for the instrument from config or a default
func getInstrumentDescription(name string, route Route) string {
	if route.Description != "" {
		return route.Description
	}

	// Fallback descriptions for backward compatibility
	descriptions := map[string]string{}

	if desc, exists := descriptions[name]; exists {
		return desc
	}
	return "Custom WebAssembly instrument"
}

// getInstrumentCategory returns a category for the instrument from config or a default
func getInstrumentCategory(name string, route Route) string {
	if route.Category != "" {
		return route.Category
	}

	// Fallback categories for backward compatibility
	categories := map[string]string{}

	if cat, exists := categories[name]; exists {
		return cat
	}
	return "Custom"
}

// getInstrumentExample returns an example for the instrument from config or generates one
func getInstrumentExample(path string, route Route) string {
	if route.Example != "" {
		return route.Example
	}

	return ""
}

func getInstrumentExamples(path string, route Route) []RouteExample {
	examples := make([]RouteExample, 0, len(route.Examples)+1)
	seen := make(map[string]struct{}, len(route.Examples)+1)

	if route.Example != "" {
		examples = append(examples, RouteExample{
			Label: "Default example",
			Query: route.Example,
		})
		seen[buildExamplePath(path, route.Example)] = struct{}{}
	}

	for _, example := range route.Examples {
		if strings.TrimSpace(example.Query) == "" {
			continue
		}

		normalizedPath := buildExamplePath(path, example.Query)
		if _, exists := seen[normalizedPath]; exists {
			continue
		}

		label := strings.TrimSpace(example.Label)
		if label == "" {
			label = "Example"
		}
		examples = append(examples, RouteExample{
			Label: label,
			Query: example.Query,
		})
		seen[normalizedPath] = struct{}{}
	}

	if len(examples) == 0 {
		defaultQuery := getInstrumentExample(path, route)
		defaultLabel := "Example"
		if strings.TrimSpace(defaultQuery) == "" {
			defaultLabel = "Base example"
		}
		examples = append(examples, RouteExample{
			Label: defaultLabel,
			Query: defaultQuery,
		})
	}

	return examples
}

func getInstrumentUseCase(_ string, route Route) string {
	if route.UseCase != "" {
		return route.UseCase
	}
	return "General-purpose WebAssembly-backed endpoint"
}

func buildExamplePath(path, query string) string {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return path
	}
	if strings.HasPrefix(trimmed, "?") {
		return path + trimmed
	}
	return path + "?" + trimmed
}

func buildAbsoluteExampleURL(host, path, query string) string {
	relativePath := buildExamplePath(path, query)
	if strings.TrimSpace(host) == "" {
		return relativePath
	}
	return fmt.Sprintf("http://%s%s", host, relativePath)
}

func renderExampleActions(host, path string, examples []RouteExample) string {
	var builder strings.Builder

	for _, example := range examples {
		label := strings.TrimSpace(example.Label)
		if label == "" {
			label = "Example"
		}
		relativeURL := buildExamplePath(path, example.Query)
		absoluteURL := buildAbsoluteExampleURL(host, path, example.Query)

		builder.WriteString(fmt.Sprintf(`
                            <div class="border rounded p-2 mb-2">
                                <div class="d-flex justify-content-between align-items-start gap-2">
                                    <div class="flex-grow-1">
                                        <div class="fw-semibold">%s</div>
                                        <code class="example-code">%s</code>
                                    </div>
                                    <div class="btn-group btn-group-sm">
                                        <a href="%s" class="btn btn-primary" target="_blank">Open</a>
                                        <button type="button" class="btn btn-outline-secondary" data-url="%s" onclick="copyUrl(this.dataset.url, this)">Copy</button>
                                    </div>
                                </div>
                            </div>`,
			htmlpkg.EscapeString(label),
			htmlpkg.EscapeString(relativeURL),
			htmlpkg.EscapeString(relativeURL),
			htmlpkg.EscapeString(absoluteURL),
		))
	}

	return builder.String()
}

func main() {
	// Use standard logger with timestamp
	log.SetFlags(log.LstdFlags)

	// Load configuration
	cfg, err := LoadConfig("config.json")
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}

	// Initialize server
	server := NewServer(cfg)

	// Wrap with logging and CORS middleware
	handler := logMiddleware(corsMiddleware(cfg.CORS, server))

	// HTTP server with graceful shutdown
	httpSrv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: handler,
	}

	// Start listening
	go func() {
		log.Printf("WASIO listening on %s", httpSrv.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen error: %v", err)
		}
	}()

	// Wait for interrupt (SIGINT/SIGTERM)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Print("shutdown initiated")

	// Context with timeout for shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Printf("shutdown error: %v", err)
	}

	// Cancel any background context
	server.cancel()
	log.Print("shutdown complete")
}
