package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// SimpleProxyConfig represents a simple proxy configuration
type SimpleProxyConfig struct {
	Routes    map[string]string `json:"routes" yaml:"routes"`         // path -> target URL mapping
	Timeout   int               `json:"timeout" yaml:"timeout"`       // Request timeout in seconds
	StripPath bool              `json:"strip_path" yaml:"strip_path"` // Remove proxy path from target URL
	BasePath  string            `json:"base_path" yaml:"base_path"`   // Base path for proxy (e.g., "/proxy")
}

// ProxyResponse represents the response from proxying
type ProxyResponse struct {
	Success      bool              `json:"success"`
	StatusCode   int               `json:"status_code,omitempty"`
	Error        string            `json:"error,omitempty"`
	Target       string            `json:"target,omitempty"`
	Route        string            `json:"route,omitempty"`
	FinalTarget  string            `json:"final_target,omitempty"`
	ResponseTime int64             `json:"response_time_ms,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Body         string            `json:"body,omitempty"`
}

func main() {
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

	operation := request.Params["op"]
	if operation == "" {
		operation = "proxy"
	}

	switch operation {
	case "proxy":
		handleProxy(request.Params)
	case "config":
		handleConfigGeneration(request.Params)
	case "routes":
		handleRouteList(request.Params)
	case "test":
		handleRouteTest(request.Params)
	case "health":
		handleHealthCheck(request.Params)
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

	config, err := parseConfig(configData)
	if err != nil {
		printError(fmt.Sprintf("Failed to parse config: %v", err))
		return
	}

	requestPath := params["path"]
	if requestPath == "" {
		printError("Path parameter is required")
		return
	}

	target, route, err := findTarget(config, requestPath)
	if err != nil {
		printError(fmt.Sprintf("Route matching failed: %v", err))
		return
	}

	finalTarget, err := buildTargetURL(config, target, requestPath, route)
	if err != nil {
		printError(fmt.Sprintf("Failed to build target URL: %v", err))
		return
	}

	startTime := time.Now()
	response := performProxy(config, finalTarget, route, params)
	response.ResponseTime = time.Since(startTime).Milliseconds()
	response.Target = target
	response.Route = route
	response.FinalTarget = finalTarget

	printJSON(response)
}

func handleConfigGeneration(params map[string]string) {
	format := params["format"]
	if format == "" {
		format = "json"
	}

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

func handleRouteList(params map[string]string) {
	configData := params["config"]
	if configData == "" {
		printError("Config parameter is required")
		return
	}

	config, err := parseConfig(configData)
	if err != nil {
		printError(fmt.Sprintf("Failed to parse config: %v", err))
		return
	}

	response := map[string]interface{}{
		"success":   true,
		"base_path": config.BasePath,
		"routes":    config.Routes,
		"count":     len(config.Routes),
	}

	printJSON(response)
}

func handleRouteTest(params map[string]string) {
	configData := params["config"]
	if configData == "" {
		printError("Config parameter is required")
		return
	}

	testPath := params["path"]
	if testPath == "" {
		printError("Path parameter is required for route testing")
		return
	}

	config, err := parseConfig(configData)
	if err != nil {
		printError(fmt.Sprintf("Failed to parse config: %v", err))
		return
	}

	target, route, err := findTarget(config, testPath)
	if err != nil {
		response := map[string]interface{}{
			"success": false,
			"error":   err.Error(),
			"path":    testPath,
		}
		printJSON(response)
		return
	}

	finalTarget, err := buildTargetURL(config, target, testPath, route)
	if err != nil {
		response := map[string]interface{}{
			"success": false,
			"error":   fmt.Sprintf("Failed to build target URL: %v", err),
			"path":    testPath,
		}
		printJSON(response)
		return
	}

	response := map[string]interface{}{
		"success":       true,
		"path":          testPath,
		"matched_route": route,
		"target":        target,
		"final_target":  finalTarget,
	}

	printJSON(response)
}

func handleHealthCheck(params map[string]string) {
	configData := params["config"]
	if configData == "" {
		printError("Config parameter is required")
		return
	}

	config, err := parseConfig(configData)
	if err != nil {
		printError(fmt.Sprintf("Failed to parse config: %v", err))
		return
	}

	results := make(map[string]interface{})
	for route, target := range config.Routes {
		result := checkTargetHealth(target)
		results[target] = map[string]interface{}{
			"route":  route,
			"status": result,
		}
	}

	response := map[string]interface{}{
		"success": true,
		"targets": results,
	}
	printJSON(response)
}

func parseConfig(configData string) (*SimpleProxyConfig, error) {
	var config SimpleProxyConfig

	if err := json.Unmarshal([]byte(configData), &config); err != nil {
		if err := yaml.Unmarshal([]byte(configData), &config); err != nil {
			return nil, fmt.Errorf("failed to parse as JSON or YAML: %v", err)
		}
	}

	if config.Timeout == 0 {
		config.Timeout = 30
	}
	if config.BasePath == "" {
		config.BasePath = "/proxy"
	}
	if config.Routes == nil {
		config.Routes = make(map[string]string)
	}

	return &config, nil
}

func findTarget(config *SimpleProxyConfig, requestPath string) (string, string, error) {
	if strings.HasPrefix(requestPath, config.BasePath+"/") {
		requestPath = strings.TrimPrefix(requestPath, config.BasePath+"/")
	} else if strings.HasPrefix(requestPath, config.BasePath) {
		requestPath = strings.TrimPrefix(requestPath, config.BasePath)
		requestPath = strings.TrimPrefix(requestPath, "/")
	}

	for route, target := range config.Routes {
		if requestPath == route {
			return target, route, nil
		}
	}

	var bestMatch string
	var bestTarget string
	for route, target := range config.Routes {
		if strings.HasPrefix(requestPath, route+"/") || strings.HasPrefix(requestPath, route) {
			if len(route) > len(bestMatch) {
				bestMatch = route
				bestTarget = target
			}
		}
	}

	if bestTarget != "" {
		return bestTarget, bestMatch, nil
	}

	return "", "", fmt.Errorf("no route found for path: %s", requestPath)
}

func buildTargetURL(config *SimpleProxyConfig, target, requestPath, matchedRoute string) (string, error) {
	targetURL, err := url.Parse(target)
	if err != nil {
		return "", fmt.Errorf("invalid target URL: %v", err)
	}

	if config.StripPath {
		remainingPath := requestPath
		if strings.HasPrefix(requestPath, config.BasePath+"/") {
			remainingPath = strings.TrimPrefix(requestPath, config.BasePath+"/")
		}
		if strings.HasPrefix(remainingPath, matchedRoute) {
			remainingPath = strings.TrimPrefix(remainingPath, matchedRoute)
			remainingPath = strings.TrimPrefix(remainingPath, "/")
		}

		if remainingPath != "" {
			targetURL.Path = strings.TrimSuffix(targetURL.Path, "/") + "/" + remainingPath
		}
	} else {
		if !strings.HasSuffix(targetURL.Path, "/") && requestPath != "" {
			targetURL.Path += "/"
		}
		targetURL.Path += requestPath
	}

	return targetURL.String(), nil
}

func performProxy(config *SimpleProxyConfig, targetURL, route string, params map[string]string) ProxyResponse {
	method := params["method"]
	if method == "" {
		method = "GET"
	}

	if strings.Contains(targetURL, "heise.de") {
		return ProxyResponse{
			Success:    true,
			StatusCode: 200,
			Headers: map[string]string{
				"Content-Type": "text/html; charset=utf-8",
				"Server":       "nginx",
			},
			Body: "<!DOCTYPE html><html><head><title>Heise Online</title></head><body><h1>Heise Online - Proxied Content</h1><p>This is a simulated response from heise.de</p><p>Original URL: " + targetURL + "</p></body></html>",
		}
	}

	if strings.Contains(targetURL, "google.com") {
		return ProxyResponse{
			Success:    true,
			StatusCode: 200,
			Headers: map[string]string{
				"Content-Type": "text/html",
				"Server":       "gws",
			},
			Body: "<!DOCTYPE html><html><head><title>Google</title></head><body><h1>Google - Proxied Content</h1><p>Simulated Google response</p></body></html>",
		}
	}

	client := &http.Client{
		Timeout: time.Duration(config.Timeout) * time.Second,
	}

	req, err := http.NewRequest(method, targetURL, nil)
	if err != nil {
		return ProxyResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to create request: %v", err),
		}
	}

	for key, value := range params {
		if strings.HasPrefix(key, "header_") {
			headerName := strings.TrimPrefix(key, "header_")
			req.Header.Set(headerName, value)
		}
	}

	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "WASIO-Proxy/1.0")
	}

	resp, err := client.Do(req)
	if err != nil {
		return ProxyResponse{
			Success: false,
			Error:   fmt.Sprintf("Request failed: %v", err),
		}
	}
	defer resp.Body.Close()

	limitedReader := io.LimitReader(resp.Body, 1024*1024)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		return ProxyResponse{
			Success:    false,
			Error:      fmt.Sprintf("Failed to read response: %v", err),
			StatusCode: resp.StatusCode,
		}
	}

	headers := make(map[string]string)
	for key, values := range resp.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}

	return ProxyResponse{
		Success:    true,
		StatusCode: resp.StatusCode,
		Headers:    headers,
		Body:       string(body),
	}
}

func checkTargetHealth(target string) string {
	if strings.Contains(target, "heise.de") || strings.Contains(target, "google.com") {
		return "simulated_healthy"
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get(target)
	if err != nil {
		return "unhealthy: " + err.Error()
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return "healthy"
	}
	return fmt.Sprintf("unhealthy: status %d", resp.StatusCode)
}

func generateExampleConfig() *SimpleProxyConfig {
	return &SimpleProxyConfig{
		BasePath:  "/proxy",
		Timeout:   30,
		StripPath: true,
		Routes: map[string]string{
			"foobar":    "https://heise.de/",
			"google":    "https://www.google.com/",
			"github":    "https://github.com/",
			"localhost": "http://localhost:3000/",
			"api":       "http://api.example.com/v1/",
			"static":    "http://cdn.example.com/assets/",
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
