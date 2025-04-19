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
	"sync"
	"syscall"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

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
}

// Config represents the server configuration loaded from JSON.
type Config struct {
	Port      string           `json:"port"`       // HTTP listen port, default "8080"
	CacheTTL  int              `json:"cache_ttl"`  // Global response cache TTL in seconds
	CacheSize int              `json:"cache_size"` // Max entries for both module & response cache
	Routes    map[string]Route `json:"routes"`     // Map URL paths to Route settings
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
func (m *ModuleCache) Get(ctx context.Context, wasmPath string) (wazero.CompiledModule, error) {
	m.mu.RLock()
	if mod, ok := m.cache[wasmPath]; ok {
		m.mu.RUnlock()
		return mod, nil
	}
	m.mu.RUnlock()

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
	data       []byte
	expiresAt  time.Time
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
func (r *ResponseCache) Get(key string) ([]byte, bool) {
	r.mu.RLock()
	cr, ok := r.cache[key]
	r.mu.RUnlock()
	if !ok || time.Now().After(cr.expiresAt) {
		return nil, false
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
		ctx:    ctx,
		cancel: cancel,
	}
}

// healthHandler responds with 200 OK for liveness probes.
func (s *Server) healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`OK`))
}

// ServeHTTP routes requests to the appropriate WASM module or health check.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	if path == "/health" {
		s.healthHandler(w, r)
		return
	}

	route, ok := s.cfg.Routes[path]
	if !ok {
		http.NotFound(w, r)
		return
	}

	key := path + "?" + r.URL.RawQuery
	if route.Cache {
		if data, found := s.respC.Get(key); found {
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
	mod, err := s.modC.Get(ctx, route.WASMFile)
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
