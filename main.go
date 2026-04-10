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
	"flag"
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

	// TimeoutMs is the per-route WASM execution deadline in milliseconds.
	// Zero falls back to Config.DefaultTimeout; still zero means no deadline.
	TimeoutMs int `json:"timeout_ms,omitempty"`

	// NativeRoutes declares host-side companion routes such as HTTP proxies,
	// SSE streams, or future WebSocket endpoints that belong to this instrument.
	NativeRoutes []NativeRouteSpec `json:"native_routes,omitempty"`

	// MaxMemoryPages overrides the global WASM memory limit for this route
	// (unit: 64 KiB pages). Zero means use the global Config.MaxMemoryPages.
	// Note: per-route limits require separate runtimes; use sparingly.
	MaxMemoryPages uint32 `json:"max_memory_pages,omitempty"`
}

// CORSConfig holds Cross-Origin Resource Sharing settings applied globally.
type CORSConfig struct {
	Enabled        bool     `json:"enabled"`
	AllowedOrigins []string `json:"allowed_origins"` // "*" to allow all
	AllowedMethods []string `json:"allowed_methods"`
	AllowedHeaders []string `json:"allowed_headers"`
	MaxAge         int      `json:"max_age"` // Preflight cache in seconds
}

// LoggingConfig controls the access-log format and request tracing.
type LoggingConfig struct {
	// Format is "json" (default, structured) or "combined" (Apache Combined Log Format).
	Format string `json:"format"`
	// RequestID enables automatic X-Request-ID header generation when true.
	RequestID bool `json:"request_id"`
}

// Config represents the server configuration loaded from JSON.
type Config struct {
	Port           string           `json:"port"`               // HTTP listen port, default "8080"
	CacheTTL       int              `json:"cache_ttl"`          // Global response cache TTL in seconds
	CacheSize      int              `json:"cache_size"`         // Max entries for both module & response cache
	IndexPage      bool             `json:"index_page"`         // Enable index page (default: true)
	Monitoring     bool             `json:"monitoring"`         // Enable monitoring endpoint (default: true)
	DefaultTimeout int              `json:"default_timeout_ms"` // Global WASM execution timeout in ms (0 = none)
	MaxMemoryPages uint32           `json:"max_memory_pages"`   // Global WASM memory limit in 64 KiB pages (0 = unlimited)
	Logging        LoggingConfig    `json:"logging"`            // Access-log format configuration
	CORS           CORSConfig       `json:"cors"`               // Global CORS configuration
	Routes         map[string]Route `json:"routes"`             // Map URL paths to Route settings
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
	if cfg.Logging.Format == "" {
		cfg.Logging.Format = "json" // Default to structured JSON logging
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

// NewModuleCache constructs a ModuleCache with given max size and optional memory limit.
// maxMemPages is the WASM linear-memory ceiling in 64 KiB pages (0 = unlimited).
func NewModuleCache(ctx context.Context, size int, maxMemPages uint32) *ModuleCache {
	rtCfg := wazero.NewRuntimeConfig()
	if maxMemPages > 0 {
		rtCfg = rtCfg.WithMemoryLimitPages(maxMemPages)
	}
	rt := wazero.NewRuntimeWithConfig(ctx, rtCfg)
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
// Server is the main HTTP server with configuration, caches, and context.
type Server struct {
	cfg            *Config
	modC           *ModuleCache
	respC          *ResponseCache
	stats          *ServerStats
	ctx            context.Context
	cancel         context.CancelFunc
	nativeHandlers map[string]http.HandlerFunc
}

// RegisterNativeHandler registers a native Go handler at the given path.
// Native handlers take precedence over WASM routes for the same path.
func (s *Server) RegisterNativeHandler(path string, h http.HandlerFunc) {
	s.nativeHandlers[path] = h
}

// requestPayload is the JSON structure sent to the WASM module on stdin.
type requestPayload struct {
	Params    map[string]string `json:"params"`
	Headers   map[string]string `json:"headers"`    // Sanitised request headers
	RequestID string            `json:"request_id"` // X-Request-ID for correlation
	Seed      int64             `json:"seed"`
}

// NewServer initializes a Server with caches and context for shutdown.
// It also runs all registered native-handler registrations.
func NewServer(cfg *Config) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		cfg:            cfg,
		modC:           NewModuleCache(ctx, cfg.CacheSize, cfg.MaxMemoryPages),
		respC:          NewResponseCache(cfg.CacheSize),
		stats:          NewServerStats(),
		ctx:            ctx,
		cancel:         cancel,
		nativeHandlers: make(map[string]http.HandlerFunc),
	}
	if err := s.registerConfiguredNativeRoutes(); err != nil {
		log.Printf(`{"level":"error","msg":"native route registration failed","error":%q}`, err)
	}
	return s
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
		Flushed   []string `json:"flushed"`
		RespCache bool     `json:"response_cache_flushed"`
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

// categoryBadgeColor maps a category name to a Bootstrap color token.
func categoryBadgeColor(cat string) string {
	switch strings.ToLower(cat) {
	case "ai":
		return "success"
	case "math":
		return "info"
	case "security":
		return "danger"
	case "network":
		return "warning"
	case "utils":
		return "primary"
	case "graphics":
		return "dark"
	case "basic":
		return "secondary"
	default:
		return "secondary"
	}
}

// pathToID converts a URL path to a safe HTML id substring.
func pathToID(path string) string {
	r := strings.NewReplacer("/", "_", "-", "_", ".", "_")
	return r.Replace(strings.TrimPrefix(path, "/"))
}

// indexHandler serves the main index page with all active instruments.
func (s *Server) indexHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// ── group routes by category ──────────────────────────────────────────────
	paths := make([]string, 0, len(s.cfg.Routes))
	for path := range s.cfg.Routes {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	categoryRoutes := map[string][]string{}
	for _, path := range paths {
		cat := getInstrumentCategory(strings.TrimPrefix(path, "/"), s.cfg.Routes[path])
		categoryRoutes[cat] = append(categoryRoutes[cat], path)
	}
	categories := make([]string, 0, len(categoryRoutes))
	for cat := range categoryRoutes {
		categories = append(categories, cat)
	}
	sort.Strings(categories)

	// ── stats ─────────────────────────────────────────────────────────────────
	var statsBar string
	if s.cfg.Monitoring {
		stats := s.stats.GetStats()
		cacheRate := 0.0
		if total := stats.CacheHits + stats.CacheMisses; total > 0 {
			cacheRate = float64(stats.CacheHits) / float64(total) * 100
		}
		statsBar = fmt.Sprintf(`
<div class="bg-dark text-white py-2 mb-0">
  <div class="container-fluid px-4">
    <div class="d-flex flex-wrap gap-4 align-items-center small">
      <span>⏱ Uptime <strong>%s</strong></span>
      <span>📥 Requests <strong>%d</strong></span>
      <span>🗂 Routes <strong>%d</strong></span>
      <span>🎯 Cache hit rate <strong>%.0f%%</strong></span>
      <span class="ms-auto text-muted">avg %s / req</span>
    </div>
  </div>
</div>`,
			formatDuration(time.Since(stats.StartTime)),
			stats.TotalRequests,
			len(s.cfg.Routes),
			cacheRate,
			stats.AverageResponse.Round(time.Millisecond),
		)
	}

	// ── category filter pills ─────────────────────────────────────────────────
	var filterPills strings.Builder
	filterPills.WriteString(`<button class="btn btn-sm btn-dark active" onclick="setCategory('',this)">All</button>`)
	for _, cat := range categories {
		filterPills.WriteString(fmt.Sprintf(
			`<button class="btn btn-sm btn-outline-secondary" onclick="setCategory('%s',this)">%s</button>`,
			htmlpkg.EscapeString(cat), htmlpkg.EscapeString(cat),
		))
	}

	// ── instrument cards ──────────────────────────────────────────────────────
	var cardsHTML strings.Builder
	for _, cat := range categories {
		color := categoryBadgeColor(cat)
		cardsHTML.WriteString(fmt.Sprintf(`
<div class="category-section mb-5" data-cat="%s">
  <h5 class="text-uppercase text-muted fw-semibold mb-3 border-bottom pb-1 d-flex align-items-center gap-2">
    <span class="badge bg-%s">%s</span>
    <span>%s</span>
  </h5>
  <div class="row g-3">`,
			htmlpkg.EscapeString(cat), color, htmlpkg.EscapeString(cat), htmlpkg.EscapeString(cat),
		))

		for _, path := range categoryRoutes[cat] {
			route := s.cfg.Routes[path]
			name := strings.TrimPrefix(path, "/")
			desc := getInstrumentDescription(name, route)
			useCase := getInstrumentUseCase(name, route)
			examples := getInstrumentExamples(path, route)
			id := pathToID(path)
			ttl := getTTL(route, s.cfg.CacheTTL)

			// cache badge
			cacheBadge := ""
			if route.Cache {
				cacheBadge = fmt.Sprintf(
					`<span class="badge bg-success-subtle text-success border border-success-subtle ms-1" title="Cached %ds">⚡ %ds</span>`,
					ttl, ttl,
				)
			}

			// first example → primary action button
			firstAction := ""
			if len(examples) > 0 {
				ex := examples[0]
				rel := buildExamplePath(path, ex.Query)
				abs := buildAbsoluteExampleURL(r.Host, path, ex.Query)
				firstAction = fmt.Sprintf(`
      <div class="d-flex gap-1 mb-1">
        <a href="%s" class="btn btn-primary btn-sm flex-grow-1 text-truncate" target="_blank" title="%s">▶ %s</a>
        <button class="btn btn-outline-secondary btn-sm px-2" onclick="copyUrl(%q,this)" title="Copy URL">⎘</button>
      </div>`,
					htmlpkg.EscapeString(rel),
					htmlpkg.EscapeString(rel),
					htmlpkg.EscapeString(ex.Label),
					abs,
				)
			}

			// extra examples → collapsible
			extraExamples := ""
			if len(examples) > 1 {
				var extra strings.Builder
				extra.WriteString(fmt.Sprintf(
					`<div class="collapse mt-1" id="more-%s">`, id,
				))
				for _, ex := range examples[1:] {
					rel := buildExamplePath(path, ex.Query)
					abs := buildAbsoluteExampleURL(r.Host, path, ex.Query)
					extra.WriteString(fmt.Sprintf(`
        <div class="d-flex gap-1 mb-1">
          <a href="%s" class="btn btn-outline-primary btn-sm flex-grow-1 text-truncate" target="_blank" title="%s">%s</a>
          <button class="btn btn-outline-secondary btn-sm px-2" onclick="copyUrl(%q,this)" title="Copy URL">⎘</button>
        </div>`,
						htmlpkg.EscapeString(rel),
						htmlpkg.EscapeString(rel),
						htmlpkg.EscapeString(ex.Label),
						abs,
					))
				}
				extra.WriteString(`</div>`)
				extra.WriteString(fmt.Sprintf(
					`<button class="btn btn-link btn-sm p-0 mt-1 text-muted" data-bs-toggle="collapse" data-bs-target="#more-%s">+%d more</button>`,
					id, len(examples)-1,
				))
				extraExamples = extra.String()
			}

			cardsHTML.WriteString(fmt.Sprintf(`
    <div class="col-sm-6 col-xl-4 instrument-card" data-cat="%s" data-search="%s">
      <div class="card h-100 shadow-sm">
        <div class="card-body d-flex flex-column">
          <div class="d-flex align-items-center flex-wrap gap-1 mb-2">
            <code class="fs-6 fw-bold text-primary">/%s</code>
            <span class="badge bg-%s rounded-pill">%s</span>
            %s
          </div>
          <p class="card-text text-muted small mb-1 flex-grow-1" title="%s">%s</p>
          <p class="card-text text-muted" style="font-size:.75rem" title="%s"><em>%s</em></p>
          <div class="mt-auto pt-2">
            %s%s
          </div>
        </div>
      </div>
    </div>`,
				htmlpkg.EscapeString(cat),
				htmlpkg.EscapeString(strings.ToLower(name+" "+desc+" "+cat)),
				htmlpkg.EscapeString(name),
				color,
				htmlpkg.EscapeString(cat),
				cacheBadge,
				htmlpkg.EscapeString(desc),
				htmlpkg.EscapeString(desc),
				htmlpkg.EscapeString(useCase),
				htmlpkg.EscapeString(useCase),
				firstAction,
				extraExamples,
			))
		}

		cardsHTML.WriteString(`
  </div>
</div>`)
	}

	// ── full page ─────────────────────────────────────────────────────────────
	page := `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>WASIO – Instrument Overview</title>
  <link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.2/dist/css/bootstrap.min.css" rel="stylesheet">
  <style>
    body { background: #f8f9fa; }
    .instrument-card { transition: transform .15s, box-shadow .15s; }
    .instrument-card .card:hover { transform: translateY(-2px); box-shadow: 0 4px 16px rgba(0,0,0,.12) !important; }
    .instrument-card .card-text { display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
    #search { max-width: 340px; }
    .category-section.d-none-cat { display: none !important; }
  </style>
</head>
<body>
<nav class="navbar navbar-expand-lg navbar-dark bg-primary">
  <div class="container-fluid px-4">
    <a class="navbar-brand fw-bold" href="/"><strong>WASIO</strong></a>
    <div class="navbar-nav ms-auto flex-row gap-2">
      <a class="nav-link" href="/monitoring">📊 Monitoring</a>
      <a class="nav-link" href="/_reload">🔄 Reload</a>
      <a class="nav-link" href="/health">❤️ Health</a>
    </div>
  </div>
</nav>
` + statsBar + `
<div class="container-fluid px-4 py-4">
  <div class="d-flex flex-wrap align-items-center gap-3 mb-4">
    <input id="search" type="search" class="form-control form-control-sm" placeholder="Search instruments…" oninput="filterInstruments()">
    <div id="cat-filters" class="d-flex flex-wrap gap-2">` + filterPills.String() + `</div>
    <span id="count-badge" class="badge bg-secondary ms-auto"></span>
  </div>
  <div id="instrument-grid">` + cardsHTML.String() + `</div>
  <p id="no-results" class="text-center text-muted py-5 d-none">No instruments match your filter.</p>
</div>

<footer class="bg-light border-top py-3 mt-4">
  <div class="container-fluid px-4 text-center text-muted small">
    <strong>WASIO</strong> · WebAssembly System Interface Orchestrator ·
    Powered by <a href="https://github.com/tetratelabs/wazero">Wazero</a>
  </div>
</footer>

<script src="https://cdn.jsdelivr.net/npm/bootstrap@5.3.2/dist/js/bootstrap.bundle.min.js"></script>
<script>
  let activeCategory = '';

  function setCategory(cat, btn) {
    activeCategory = cat;
    document.querySelectorAll('#cat-filters button').forEach(b => {
      b.classList.toggle('active', b === btn);
      b.classList.toggle('btn-dark', b === btn);
      b.classList.toggle('btn-outline-secondary', b !== btn);
    });
    filterInstruments();
  }

  function filterInstruments() {
    const q = document.getElementById('search').value.toLowerCase().trim();
    let visible = 0;

    document.querySelectorAll('.category-section').forEach(section => {
      const cat = section.dataset.cat;
      const catMatch = !activeCategory || cat === activeCategory;

      let sectionVisible = false;
      section.querySelectorAll('.instrument-card').forEach(card => {
        const searchMatch = !q || card.dataset.search.includes(q);
        const show = catMatch && searchMatch;
        card.style.display = show ? '' : 'none';
        if (show) { sectionVisible = true; visible++; }
      });
      section.style.display = sectionVisible ? '' : 'none';
    });

    const badge = document.getElementById('count-badge');
    badge.textContent = visible + ' instrument' + (visible !== 1 ? 's' : '');
    document.getElementById('no-results').classList.toggle('d-none', visible > 0);
  }

  function copyUrl(url, btn) {
    navigator.clipboard.writeText(url).then(() => {
      const t = btn.textContent;
      btn.textContent = '✓';
      btn.classList.add('btn-success');
      btn.classList.remove('btn-outline-secondary');
      setTimeout(() => { btn.textContent = t; btn.classList.remove('btn-success'); btn.classList.add('btn-outline-secondary'); }, 1800);
    });
  }

  // initialise count badge
  filterInstruments();
</script>
</body>
</html>`

	w.Write([]byte(page))
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

	// Dispatch to registered native handlers (non-WASM, instrument-specific)
	if h, ok := s.nativeHandlers[path]; ok {
		h(w, r)
		return
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

	// Build payload from query parameters, sanitised request headers, and a random seed.
	params := make(map[string]string, len(r.URL.Query()))
	for k, vs := range r.URL.Query() {
		if len(vs) > 0 {
			params[k] = vs[0]
		}
	}
	headers := make(map[string]string)
	skipHeaders := map[string]bool{"Cookie": true, "Set-Cookie": true}
	for k, vs := range r.Header {
		if !skipHeaders[k] && len(vs) > 0 {
			headers[k] = vs[0]
		}
	}
	seed, _ := readRandomSeed()
	payload := requestPayload{
		Params:    params,
		Headers:   headers,
		RequestID: r.Header.Get("X-Request-ID"),
		Seed:      seed,
	}
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
// A per-route or global execution deadline is applied when configured.
// Note: wazero's default ModuleConfig already runs the WASI command entrypoint
// "_start" during InstantiateModule, so we must not call it manually again.
func (s *Server) runWASM(ctx context.Context, route *Route, stdin []byte, stdout io.Writer) error {
	// Apply per-route timeout, falling back to the global default.
	timeoutMs := route.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = s.cfg.DefaultTimeout
	}
	if timeoutMs > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
		defer cancel()
	}

	mod, err := s.modC.Get(ctx, route.WASMFile, s.stats)
	if err != nil {
		return err
	}

	stderrBuf := &bytes.Buffer{}
	config := wazero.NewModuleConfig().
		WithStdin(bytes.NewReader(stdin)).
		WithStdout(stdout).
		WithStderr(stderrBuf)

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
		if stderrBuf.Len() > 0 {
			return fmt.Errorf("instantiate module: %w: %s", err, strings.TrimSpace(stderrBuf.String()))
		}
		return fmt.Errorf("instantiate module: %w", err)
	}
	defer instance.Close(ctx)
	return nil
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

func (lrw *loggingResponseWriter) Flush() {
	if flusher, ok := lrw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
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

// logMiddleware logs one line per request in the configured format.
// cfg.Format "json" (default) emits a structured JSON object.
// cfg.Format "combined" emits the Apache Combined Log Format.
func logMiddleware(cfg LoggingConfig, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lrw := &loggingResponseWriter{ResponseWriter: w}
		next.ServeHTTP(lrw, r)

		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		dur := time.Since(start)

		if cfg.Format == "combined" {
			log.Printf("%s - - [%s] \"%s %s %s\" %d %d \"%s\" \"%s\"",
				host,
				start.Format("02/Jan/2006:15:04:05 -0700"),
				r.Method, r.RequestURI, r.Proto,
				lrw.status, lrw.size,
				r.Referer(), r.UserAgent(),
			)
			return
		}

		// Structured JSON log entry (default)
		entry := map[string]interface{}{
			"time":        start.UTC().Format(time.RFC3339Nano),
			"method":      r.Method,
			"path":        r.URL.Path,
			"status":      lrw.status,
			"bytes":       lrw.size,
			"duration_ms": dur.Milliseconds(),
			"remote":      host,
			"user_agent":  r.UserAgent(),
		}
		if q := r.URL.RawQuery; q != "" {
			entry["query"] = q
		}
		if rid := r.Header.Get("X-Request-ID"); rid != "" {
			entry["request_id"] = rid
		}
		if ref := r.Referer(); ref != "" {
			entry["referer"] = ref
		}
		b, _ := json.Marshal(entry)
		log.Print(string(b))
	})
}

// requestIDMiddleware attaches a unique X-Request-ID to every request and response.
// If the client already supplies the header, the existing value is preserved.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			var b [8]byte
			rand.Read(b[:]) //nolint:errcheck // Read never fails on crypto/rand
			id = fmt.Sprintf("%x", b)
			r.Header.Set("X-Request-ID", id)
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r)
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
	// Disable stdlib log prefix – structured logger embeds its own timestamp.
	log.SetFlags(0)

	args := os.Args[1:]
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		cmdServe(args)
		return
	}
	switch args[0] {
	case "serve", "run", "start":
		cmdServe(args[1:])
	case "list":
		cmdList(args[1:])
	case "info":
		cmdInfo(args[1:])
	case "reload":
		cmdReload(args[1:])
	case "init":
		cmdInit(args[1:])
	case "pull":
		cmdPull(args[1:])
	case "add", "install":
		cmdAdd(args[1:])
	case "keygen":
		cmdKeygen(args[1:])
	case "sign":
		cmdSign(args[1:])
	case "validate":
		cmdValidate(args[1:])
	case "version", "--version", "-v":
		fmt.Printf("wasio %s\n", Version)
	case "help", "--help", "-h":
		cmdHelp()
	default:
		// Unknown first arg – treat whole args slice as server flags.
		cmdServe(args)
	}
}

// cmdServe starts the WASIO HTTP server.
func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", "config.json", "path to config file")
	portFlag := fs.String("port", "", "override listen port from config")
	fs.Parse(args) //nolint:errcheck // ExitOnError handles the error

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf(`{"level":"fatal","msg":"configuration error","error":%q}`, err)
	}
	if *portFlag != "" {
		cfg.Port = *portFlag
	}

	server := NewServer(cfg)

	// Middleware chain (outermost → innermost):
	//   structured logger → request-ID → CORS → WASM router
	var handler http.Handler = server
	handler = corsMiddleware(cfg.CORS, handler)
	if cfg.Logging.RequestID {
		handler = requestIDMiddleware(handler)
	}
	handler = logMiddleware(cfg.Logging, handler)

	httpSrv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: handler,
	}

	go func() {
		log.Printf(`{"level":"info","msg":"WASIO listening","addr":%q,"version":%q}`, httpSrv.Addr, Version)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf(`{"level":"fatal","msg":"listen error","error":%q}`, err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Print(`{"level":"info","msg":"shutdown initiated"}`)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Printf(`{"level":"error","msg":"shutdown error","error":%q}`, err)
	}
	server.cancel()
	log.Print(`{"level":"info","msg":"shutdown complete"}`)
}
