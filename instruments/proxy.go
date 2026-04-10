package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ProxyConfig represents the complete proxy configuration
type ProxyConfig struct {
	// Global settings
	Timeout         int  `json:"timeout" yaml:"timeout"`                       // Request timeout in seconds
	MaxIdleConns    int  `json:"max_idle_conns" yaml:"max_idle_conns"`         // Max idle connections
	MaxConnsPerHost int  `json:"max_conns_per_host" yaml:"max_conns_per_host"` // Max connections per host
	KeepAlive       int  `json:"keep_alive" yaml:"keep_alive"`                 // Keep-alive timeout in seconds
	EnableHTTP2     bool `json:"enable_http2" yaml:"enable_http2"`             // Enable HTTP/2

	// TLS settings
	TLS TLSConfig `json:"tls" yaml:"tls"`

	// Route definitions
	Routes []Route `json:"routes" yaml:"routes"`

	// Default route when no matches found
	DefaultTarget string `json:"default_target" yaml:"default_target"`

	// Logging configuration
	Logging LoggingConfig `json:"logging" yaml:"logging"`

	// Rate limiting
	RateLimit RateLimitConfig `json:"rate_limit" yaml:"rate_limit"`

	// Health check configuration
	HealthCheck HealthCheckConfig `json:"health_check" yaml:"health_check"`
}

// TLSConfig represents TLS/SSL configuration
type TLSConfig struct {
	InsecureSkipVerify bool `json:"insecure_skip_verify" yaml:"insecure_skip_verify"`
	MinVersion         int  `json:"min_version" yaml:"min_version"` // 0=TLS1.0, 1=TLS1.1, 2=TLS1.2, 3=TLS1.3
	MaxVersion         int  `json:"max_version" yaml:"max_version"`
}

// Route represents a single routing rule
type Route struct {
	Name          string            `json:"name" yaml:"name"`
	Priority      int               `json:"priority" yaml:"priority"` // Higher priority routes are checked first
	Conditions    []Condition       `json:"conditions" yaml:"conditions"`
	Target        string            `json:"target" yaml:"target"`
	Transforms    []Transform       `json:"transforms" yaml:"transforms"`
	Headers       map[string]string `json:"headers" yaml:"headers"`               // Headers to add/modify
	RemoveHeaders []string          `json:"remove_headers" yaml:"remove_headers"` // Headers to remove
	Timeout       int               `json:"timeout" yaml:"timeout"`               // Override global timeout
	Enabled       bool              `json:"enabled" yaml:"enabled"`
}

// Condition represents a matching condition
type Condition struct {
	Type    string `json:"type" yaml:"type"`       // domain, port, path, header, query, method, ip
	Pattern string `json:"pattern" yaml:"pattern"` // RegExp pattern or exact match
	IsRegex bool   `json:"is_regex" yaml:"is_regex"`
	Negate  bool   `json:"negate" yaml:"negate"` // Invert the condition
	Key     string `json:"key" yaml:"key"`       // For header/query conditions
}

// Transform represents a URL/path transformation
type Transform struct {
	Type    string `json:"type" yaml:"type"`       // replace_path, replace_host, add_prefix, remove_prefix
	Pattern string `json:"pattern" yaml:"pattern"` // RegExp pattern for replacement
	Replace string `json:"replace" yaml:"replace"` // Replacement string
}

// LoggingConfig represents logging configuration
type LoggingConfig struct {
	Enabled    bool   `json:"enabled" yaml:"enabled"`
	Format     string `json:"format" yaml:"format"` // json, combined, common
	LogHeaders bool   `json:"log_headers" yaml:"log_headers"`
	LogBody    bool   `json:"log_body" yaml:"log_body"`
}

// RateLimitConfig represents rate limiting configuration
type RateLimitConfig struct {
	Enabled           bool `json:"enabled" yaml:"enabled"`
	RequestsPerMinute int  `json:"requests_per_minute" yaml:"requests_per_minute"`
	BurstSize         int  `json:"burst_size" yaml:"burst_size"`
}

// HealthCheckConfig represents health check configuration
type HealthCheckConfig struct {
	Enabled  bool   `json:"enabled" yaml:"enabled"`
	Path     string `json:"path" yaml:"path"`
	Interval int    `json:"interval" yaml:"interval"` // Check interval in seconds
	Timeout  int    `json:"timeout" yaml:"timeout"`   // Health check timeout
}

// RequestInfo contains information about the incoming request
type RequestInfo struct {
	Method     string
	URL        *url.URL
	Host       string
	Domain     string
	Port       string
	Path       string
	RawQuery   string
	Headers    http.Header
	RemoteAddr string
	RemoteIP   string
}

// ProxyResponse represents the response from proxying
type ProxyResponse struct {
	Success      bool              `json:"success"`
	StatusCode   int               `json:"status_code,omitempty"`
	Error        string            `json:"error,omitempty"`
	Target       string            `json:"target,omitempty"`
	MatchedRoute string            `json:"matched_route,omitempty"`
	ResponseTime int64             `json:"response_time_ms,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Body         string            `json:"body,omitempty"`
}

// Global variables for rate limiting (simple in-memory implementation)
var (
	rateLimitMap = make(map[string][]time.Time)
	httpClient   *http.Client
)

func main() {
	// Read input from stdin
	scanner := bufio.NewScanner(os.Stdin)
	var input strings.Builder
	for scanner.Scan() {
		input.WriteString(scanner.Text())
	}

	var request struct {
		Params map[string]string `json:"params"`
	}

	if err := json.Unmarshal([]byte(input.String()), &request); err != nil {
		printError(fmt.Sprintf("Failed to parse input: %v", err))
		return
	}

	// Handle different operations
	operation := request.Params["op"]
	if operation == "" {
		operation = "proxy"
	}

	switch operation {
	case "proxy":
		handleProxy(request.Params)
	case "config":
		handleConfigGeneration(request.Params)
	case "health":
		handleHealthCheck(request.Params)
	case "test":
		handleConfigTest(request.Params)
	default:
		printError(fmt.Sprintf("Unknown operation: %s", operation))
	}
}

func handleProxy(params map[string]string) {
	configData := params["config"]
	if configData == "" {
		printError("Config parameter is required for proxy operation")
		return
	}

	// Parse configuration
	config, err := parseConfig(configData)
	if err != nil {
		printError(fmt.Sprintf("Failed to parse config: %v", err))
		return
	}

	// Initialize HTTP client with configuration
	initHTTPClient(config)

	// Parse request information
	reqInfo, err := parseRequestInfo(params)
	if err != nil {
		printError(fmt.Sprintf("Failed to parse request info: %v", err))
		return
	}

	// Find matching route
	route, err := findMatchingRoute(config, reqInfo)
	if err != nil {
		printError(fmt.Sprintf("Route matching failed: %v", err))
		return
	}

	// Check rate limiting
	if config.RateLimit.Enabled {
		if !checkRateLimit(reqInfo.RemoteIP, config.RateLimit) {
			response := ProxyResponse{
				Success:    false,
				StatusCode: 429,
				Error:      "Rate limit exceeded",
			}
			printJSON(response)
			return
		}
	}

	// Perform the proxy request
	startTime := time.Now()
	response := performProxy(config, route, reqInfo)
	response.ResponseTime = time.Since(startTime).Milliseconds()

	// Log request if enabled
	if config.Logging.Enabled {
		logRequest(config.Logging, reqInfo, response)
	}

	printJSON(response)
}

func handleConfigGeneration(params map[string]string) {
	format := params["format"]
	if format == "" {
		format = "json"
	}

	// Generate example configuration
	config := generateExampleConfig()

	switch format {
	case "yaml":
		if data, err := yaml.Marshal(config); err != nil {
			printError(fmt.Sprintf("Failed to generate YAML: %v", err))
		} else {
			fmt.Print(string(data))
		}
	case "json":
		if data, err := json.MarshalIndent(config, "", "  "); err != nil {
			printError(fmt.Sprintf("Failed to generate JSON: %v", err))
		} else {
			fmt.Print(string(data))
		}
	default:
		printError(fmt.Sprintf("Unsupported format: %s", format))
	}
}

func handleHealthCheck(params map[string]string) {
	configData := params["config"]
	if configData == "" {
		printError("Config parameter is required for health check")
		return
	}

	config, err := parseConfig(configData)
	if err != nil {
		printError(fmt.Sprintf("Failed to parse config: %v", err))
		return
	}

	if !config.HealthCheck.Enabled {
		response := map[string]interface{}{
			"success": true,
			"message": "Health check disabled",
		}
		printJSON(response)
		return
	}

	// Perform health checks on all targets
	results := make(map[string]interface{})

	// Check all route targets
	targets := make(map[string]bool)
	for _, route := range config.Routes {
		if route.Enabled && route.Target != "" {
			targets[route.Target] = true
		}
	}
	if config.DefaultTarget != "" {
		targets[config.DefaultTarget] = true
	}

	for target := range targets {
		result := checkTargetHealth(target, config.HealthCheck)
		results[target] = result
	}

	response := map[string]interface{}{
		"success": true,
		"targets": results,
	}
	printJSON(response)
}

func handleConfigTest(params map[string]string) {
	configData := params["config"]
	if configData == "" {
		printError("Config parameter is required for config test")
		return
	}

	_, err := parseConfig(configData)
	if err != nil {
		response := map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		}
		printJSON(response)
		return
	}

	response := map[string]interface{}{
		"success": true,
		"message": "Configuration is valid",
	}
	printJSON(response)
}

func parseConfig(configData string) (*ProxyConfig, error) {
	var config ProxyConfig

	// Try JSON first
	if err := json.Unmarshal([]byte(configData), &config); err != nil {
		// Try YAML
		if err := yaml.Unmarshal([]byte(configData), &config); err != nil {
			return nil, fmt.Errorf("failed to parse as JSON or YAML: %v", err)
		}
	}

	// Apply defaults
	if config.Timeout == 0 {
		config.Timeout = 30
	}
	if config.MaxIdleConns == 0 {
		config.MaxIdleConns = 100
	}
	if config.MaxConnsPerHost == 0 {
		config.MaxConnsPerHost = 10
	}
	if config.KeepAlive == 0 {
		config.KeepAlive = 30
	}

	// Sort routes by priority (higher first)
	for i := 0; i < len(config.Routes); i++ {
		for j := i + 1; j < len(config.Routes); j++ {
			if config.Routes[i].Priority < config.Routes[j].Priority {
				config.Routes[i], config.Routes[j] = config.Routes[j], config.Routes[i]
			}
		}
	}

	return &config, nil
}

func initHTTPClient(config *ProxyConfig) {
	// TinyGo has limited HTTP client configuration support
	// Use basic HTTP client with timeout
	httpClient = &http.Client{
		Timeout: time.Duration(config.Timeout) * time.Second,
	}
}

func parseRequestInfo(params map[string]string) (*RequestInfo, error) {
	method := params["method"]
	if method == "" {
		method = "GET"
	}

	urlStr := params["url"]
	if urlStr == "" {
		return nil, fmt.Errorf("URL parameter is required")
	}

	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %v", err)
	}

	host := parsedURL.Host
	domain := parsedURL.Hostname()
	port := parsedURL.Port()
	if port == "" {
		if parsedURL.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}

	// Parse headers
	headers := make(http.Header)
	for key, value := range params {
		if strings.HasPrefix(key, "header_") {
			headerName := strings.TrimPrefix(key, "header_")
			headers.Set(headerName, value)
		}
	}

	remoteAddr := params["remote_addr"]
	if remoteAddr == "" {
		remoteAddr = "127.0.0.1:0"
	}

	remoteIP := remoteAddr
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		remoteIP = host
	}

	return &RequestInfo{
		Method:     method,
		URL:        parsedURL,
		Host:       host,
		Domain:     domain,
		Port:       port,
		Path:       parsedURL.Path,
		RawQuery:   parsedURL.RawQuery,
		Headers:    headers,
		RemoteAddr: remoteAddr,
		RemoteIP:   remoteIP,
	}, nil
}

func findMatchingRoute(config *ProxyConfig, reqInfo *RequestInfo) (*Route, error) {
	for _, route := range config.Routes {
		if !route.Enabled {
			continue
		}

		matches := true
		for _, condition := range route.Conditions {
			if !evaluateCondition(condition, reqInfo) {
				matches = false
				break
			}
		}

		if matches {
			return &route, nil
		}
	}

	// No route matched, use default if available
	if config.DefaultTarget != "" {
		return &Route{
			Name:    "default",
			Target:  config.DefaultTarget,
			Enabled: true,
		}, nil
	}

	return nil, fmt.Errorf("no matching route found")
}

func evaluateCondition(condition Condition, reqInfo *RequestInfo) bool {
	var value string

	switch condition.Type {
	case "domain":
		value = reqInfo.Domain
	case "host":
		value = reqInfo.Host
	case "port":
		value = reqInfo.Port
	case "path":
		value = reqInfo.Path
	case "method":
		value = reqInfo.Method
	case "ip":
		value = reqInfo.RemoteIP
	case "header":
		value = reqInfo.Headers.Get(condition.Key)
	case "query":
		values := reqInfo.URL.Query()
		value = values.Get(condition.Key)
	default:
		return false
	}

	var matches bool
	if condition.IsRegex {
		if regex, err := regexp.Compile(condition.Pattern); err == nil {
			matches = regex.MatchString(value)
		}
	} else {
		matches = value == condition.Pattern
	}

	if condition.Negate {
		matches = !matches
	}

	return matches
}

func checkRateLimit(clientIP string, config RateLimitConfig) bool {
	if !config.Enabled {
		return true
	}

	now := time.Now()
	cutoff := now.Add(-time.Minute)

	// Clean old entries
	if requests, exists := rateLimitMap[clientIP]; exists {
		filtered := make([]time.Time, 0)
		for _, reqTime := range requests {
			if reqTime.After(cutoff) {
				filtered = append(filtered, reqTime)
			}
		}
		rateLimitMap[clientIP] = filtered
	}

	// Check current request count
	requests := rateLimitMap[clientIP]
	if len(requests) >= config.RequestsPerMinute {
		return false
	}

	// Add current request
	rateLimitMap[clientIP] = append(requests, now)
	return true
}

func performProxy(config *ProxyConfig, route *Route, reqInfo *RequestInfo) ProxyResponse {
	// Apply URL transforms
	targetURL := route.Target
	for _, transform := range route.Transforms {
		targetURL = applyTransform(transform, targetURL, reqInfo)
	}

	// Parse target URL
	target, err := url.Parse(targetURL)
	if err != nil {
		return ProxyResponse{
			Success: false,
			Error:   fmt.Sprintf("Invalid target URL: %v", err),
		}
	}

	// Build final URL
	finalURL := *target
	if reqInfo.Path != "" && !strings.HasSuffix(targetURL, reqInfo.Path) {
		finalURL.Path = strings.TrimSuffix(finalURL.Path, "/") + reqInfo.Path
	}
	if reqInfo.RawQuery != "" {
		finalURL.RawQuery = reqInfo.RawQuery
	}

	// Create request
	req, err := http.NewRequest(reqInfo.Method, finalURL.String(), nil)
	if err != nil {
		return ProxyResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to create request: %v", err),
		}
	}

	// Copy headers
	if reqInfo.Headers != nil {
		for key, values := range reqInfo.Headers {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
	}

	// Add route-specific headers
	if route.Headers != nil {
		for key, value := range route.Headers {
			req.Header.Set(key, value)
		}
	}

	// Remove specified headers
	if route.RemoveHeaders != nil {
		for _, header := range route.RemoveHeaders {
			req.Header.Del(header)
		}
	}

	// Set Host header to target host
	req.Host = target.Host

	// Use route-specific timeout if set
	client := httpClient
	if httpClient == nil {
		client = &http.Client{
			Timeout: time.Duration(config.Timeout) * time.Second,
		}
	} else if route.Timeout > 0 {
		client = &http.Client{
			Timeout: time.Duration(route.Timeout) * time.Second,
		}
	}

	// Perform request
	resp, err := client.Do(req)
	if err != nil {
		return ProxyResponse{
			Success:      false,
			Error:        fmt.Sprintf("Request failed: %v", err),
			Target:       finalURL.String(),
			MatchedRoute: route.Name,
		}
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ProxyResponse{
			Success:      false,
			Error:        fmt.Sprintf("Failed to read response: %v", err),
			Target:       finalURL.String(),
			MatchedRoute: route.Name,
			StatusCode:   resp.StatusCode,
		}
	}

	// Convert headers to map
	headers := make(map[string]string)
	for key, values := range resp.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}

	return ProxyResponse{
		Success:      true,
		StatusCode:   resp.StatusCode,
		Target:       finalURL.String(),
		MatchedRoute: route.Name,
		Headers:      headers,
		Body:         string(body),
	}
}

func applyTransform(transform Transform, targetURL string, reqInfo *RequestInfo) string {
	switch transform.Type {
	case "replace_path":
		if transform.Pattern != "" {
			if regex, err := regexp.Compile(transform.Pattern); err == nil {
				targetURL = regex.ReplaceAllString(targetURL, transform.Replace)
			}
		}
	case "replace_host":
		if parsed, err := url.Parse(targetURL); err == nil {
			parsed.Host = transform.Replace
			targetURL = parsed.String()
		}
	case "add_prefix":
		if parsed, err := url.Parse(targetURL); err == nil {
			parsed.Path = transform.Replace + parsed.Path
			targetURL = parsed.String()
		}
	case "remove_prefix":
		if parsed, err := url.Parse(targetURL); err == nil {
			if strings.HasPrefix(parsed.Path, transform.Replace) {
				parsed.Path = strings.TrimPrefix(parsed.Path, transform.Replace)
				targetURL = parsed.String()
			}
		}
	}
	return targetURL
}

func checkTargetHealth(target string, config HealthCheckConfig) map[string]interface{} {
	result := map[string]interface{}{
		"target": target,
		"status": "unknown",
	}

	// Parse target URL
	targetURL, err := url.Parse(target)
	if err != nil {
		result["status"] = "error"
		result["error"] = fmt.Sprintf("Invalid URL: %v", err)
		return result
	}

	// Build health check URL
	healthURL := *targetURL
	if config.Path != "" {
		healthURL.Path = config.Path
	}

	// Create client with specific timeout
	client := &http.Client{
		Timeout: time.Duration(config.Timeout) * time.Second,
	}

	start := time.Now()
	resp, err := client.Get(healthURL.String())
	responseTime := time.Since(start).Milliseconds()

	result["response_time_ms"] = responseTime

	if err != nil {
		result["status"] = "error"
		result["error"] = err.Error()
		return result
	}
	defer resp.Body.Close()

	result["status_code"] = resp.StatusCode
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		result["status"] = "healthy"
	} else {
		result["status"] = "unhealthy"
	}

	return result
}

func logRequest(config LoggingConfig, reqInfo *RequestInfo, response ProxyResponse) {
	if !config.Enabled {
		return
	}

	logData := map[string]interface{}{
		"timestamp":     time.Now().Format(time.RFC3339),
		"method":        reqInfo.Method,
		"url":           reqInfo.URL.String(),
		"remote_ip":     reqInfo.RemoteIP,
		"status_code":   response.StatusCode,
		"response_time": response.ResponseTime,
		"target":        response.Target,
		"route":         response.MatchedRoute,
		"success":       response.Success,
	}

	if config.LogHeaders {
		headers := make(map[string]string)
		for key, values := range reqInfo.Headers {
			if len(values) > 0 {
				headers[key] = values[0]
			}
		}
		logData["headers"] = headers
	}

	if config.LogBody && response.Body != "" {
		logData["response_body"] = response.Body
	}

	switch config.Format {
	case "json":
		if jsonData, err := json.Marshal(logData); err == nil {
			fmt.Fprintf(os.Stderr, "%s\n", jsonData)
		}
	case "combined":
		fmt.Fprintf(os.Stderr, "%s - - [%s] \"%s %s\" %d %d \"%s\" \"%s\"\n",
			reqInfo.RemoteIP,
			time.Now().Format("02/Jan/2006:15:04:05 -0700"),
			reqInfo.Method,
			reqInfo.URL.String(),
			response.StatusCode,
			len(response.Body),
			reqInfo.Headers.Get("Referer"),
			reqInfo.Headers.Get("User-Agent"),
		)
	default:
		fmt.Fprintf(os.Stderr, "PROXY %s %s -> %s [%d] %dms\n",
			reqInfo.Method,
			reqInfo.URL.String(),
			response.Target,
			response.StatusCode,
			response.ResponseTime,
		)
	}
}

func generateExampleConfig() *ProxyConfig {
	return &ProxyConfig{
		Timeout:         30,
		MaxIdleConns:    100,
		MaxConnsPerHost: 10,
		KeepAlive:       30,
		EnableHTTP2:     true,
		TLS: TLSConfig{
			InsecureSkipVerify: false,
			MinVersion:         2, // TLS 1.2
			MaxVersion:         3, // TLS 1.3
		},
		DefaultTarget: "http://localhost:3000",
		Logging: LoggingConfig{
			Enabled:    true,
			Format:     "json",
			LogHeaders: false,
			LogBody:    false,
		},
		RateLimit: RateLimitConfig{
			Enabled:           true,
			RequestsPerMinute: 100,
			BurstSize:         20,
		},
		HealthCheck: HealthCheckConfig{
			Enabled:  true,
			Path:     "/health",
			Interval: 30,
			Timeout:  5,
		},
		Routes: []Route{
			{
				Name:     "api_v1",
				Priority: 100,
				Enabled:  true,
				Conditions: []Condition{
					{
						Type:    "path",
						Pattern: "^/api/v1/.*",
						IsRegex: true,
					},
				},
				Target: "http://api-server:8080",
				Headers: map[string]string{
					"X-Forwarded-Proto": "https",
					"X-API-Version":     "v1",
				},
				RemoveHeaders: []string{"Server"},
				Timeout:       15,
			},
			{
				Name:     "static_files",
				Priority: 50,
				Enabled:  true,
				Conditions: []Condition{
					{
						Type:    "path",
						Pattern: "^/static/.*",
						IsRegex: true,
					},
				},
				Target: "http://cdn-server:8080",
				Headers: map[string]string{
					"Cache-Control": "public, max-age=3600",
				},
			},
			{
				Name:     "domain_routing",
				Priority: 75,
				Enabled:  true,
				Conditions: []Condition{
					{
						Type:    "domain",
						Pattern: "admin.example.com",
						IsRegex: false,
					},
				},
				Target: "http://admin-server:8080",
				Transforms: []Transform{
					{
						Type:    "remove_prefix",
						Replace: "/admin",
					},
				},
			},
		},
	}
}

func printJSON(data interface{}) {
	if jsonData, err := json.Marshal(data); err == nil {
		fmt.Print(string(jsonData))
	}
}

func printError(message string) {
	response := ProxyResponse{
		Success: false,
		Error:   message,
	}
	printJSON(response)
}
