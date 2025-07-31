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
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
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
	Description string `json:"description,omitempty"` // Human-readable description
	Category    string `json:"category,omitempty"`    // Category for grouping (Basic, Math, etc.)
	Example     string `json:"example,omitempty"`     // Example query parameters or usage
}

// Config represents the server configuration loaded from JSON.
type Config struct {
	Port       string           `json:"port"`       // HTTP listen port, default "8080"
	CacheTTL   int              `json:"cache_ttl"`  // Global response cache TTL in seconds
	CacheSize  int              `json:"cache_size"` // Max entries for both module & response cache
	IndexPage  bool             `json:"index_page"` // Enable index page (default: true)
	Monitoring bool             `json:"monitoring"` // Enable monitoring endpoint (default: true)
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

// ModuleCache caches compiled WASM modules with simple LRU eviction.
type ModuleCache struct {
	mu    sync.RWMutex
	rt    wazero.Runtime
	cache map[string]wazero.CompiledModule
	size  int
}

// NewModuleCache constructs a ModuleCache with given max size.
func NewModuleCache(ctx context.Context, size int) *ModuleCache {
	rt := wazero.NewRuntime(ctx)
	wasi_snapshot_preview1.MustInstantiate(ctx, rt)
	return &ModuleCache{
		rt:    rt,
		cache: make(map[string]wazero.CompiledModule, size),
		size:  size,
	}
}

// Get returns a compiled module, compiling and caching it if needed.
// Evicts one arbitrary entry when cache is full.
func (m *ModuleCache) Get(ctx context.Context, wasmPath string, stats *ServerStats) (wazero.CompiledModule, error) {
	m.mu.RLock()
	if mod, ok := m.cache[wasmPath]; ok {
		m.mu.RUnlock()
		if stats != nil {
			stats.IncrementModuleCacheHit()
		}
		return mod, nil
	}
	m.mu.RUnlock()

	if stats != nil {
		stats.IncrementModuleCacheMiss()
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
	m.cache[wasmPath] = mod
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

	// List all available routes
	for path, route := range s.cfg.Routes {
		// Try to determine instrument type and description
		instrumentName := strings.TrimPrefix(path, "/")
		description := getInstrumentDescription(instrumentName, route)
		category := getInstrumentCategory(instrumentName, route)
		example := getInstrumentExample(path, route)

		html += fmt.Sprintf(`
            <div class="col-md-6 col-lg-4 mb-3">
                <div class="card instrument-card h-100">
                    <div class="card-body">
                        <h5 class="card-title">
                            %s <span class="badge bg-secondary">%s</span>
                        </h5>
                        <p class="card-text">%s</p>
                        <div class="mb-2">
                            <small class="text-muted">
                                📁 %s<br>
                                🎯 Cache: %t<br>
                                ⏱️ TTL: %ds
                            </small>
                        </div>
                        <div class="d-flex flex-wrap gap-1">
                            <a href="%s%s" class="btn btn-primary btn-sm" target="_blank">Try Example</a>
                            <a href="%s" class="btn btn-outline-primary btn-sm" target="_blank">Base URL</a>
                            <button class="btn btn-outline-secondary btn-sm" onclick="copyUrl('%s%s')">Copy Example</button>
                        </div>
                    </div>
                </div>
            </div>`,
			instrumentName,
			category,
			description,
			route.WASMFile,
			route.Cache,
			getTTL(route, s.cfg.CacheTTL),
			path,
			example,
			path,
			fmt.Sprintf("http://%s%s%s", r.Host, path, example),
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
        function copyUrl(url) {
            navigator.clipboard.writeText(url).then(() => {
                // Show feedback
                const button = event.target;
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

	route, ok := s.cfg.Routes[path]
	if !ok {
		success = false
		http.NotFound(w, r)
		return
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

	// Generate basic example
	return "?param=value"
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

	// Wrap with logging middleware
	handler := logMiddleware(server)

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
