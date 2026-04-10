package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// NativeRouteSpec declares a host-side route owned by a WASM instrument.
//
// Examples:
//   - `adapter=openai-compatible`, `transport=https` for host-side API bridges
//   - `adapter=wasm-sse`, `transport=sse` for streamed generation loops
//
// The goal is to keep instrument-specific routing out of WASIO core. The
// server only understands generic adapters and route specs; instruments declare
// what they need in config.json or future package manifests.
type NativeRouteSpec struct {
	Path      string           `json:"path" toml:"path"`
	Transport string           `json:"transport,omitempty" toml:"transport,omitempty"`
	Adapter   string           `json:"adapter" toml:"adapter"`
	Methods   []string         `json:"methods,omitempty" toml:"methods,omitempty"`
	OpenAI    *OpenAIAdapter   `json:"openai,omitempty" toml:"openai,omitempty"`
	WASMSSE   *WASMSSEAdapter  `json:"wasm_sse,omitempty" toml:"wasm_sse,omitempty"`
}

type OpenAIAdapter struct {
	Operation    string `json:"operation" toml:"operation"`
	BaseURLEnv   string `json:"base_url_env,omitempty" toml:"base_url_env,omitempty"`
	APIKeyEnv    string `json:"api_key_env,omitempty" toml:"api_key_env,omitempty"`
	ModelEnv     string `json:"model_env,omitempty" toml:"model_env,omitempty"`
	SystemEnv    string `json:"system_env,omitempty" toml:"system_env,omitempty"`
	TimeoutMs    int    `json:"timeout_ms,omitempty" toml:"timeout_ms,omitempty"`
}

type WASMSSEAdapter struct {
	SourceRoute     string `json:"source_route,omitempty" toml:"source_route,omitempty"`
	SeedOp          string `json:"seed_op,omitempty" toml:"seed_op,omitempty"`
	StepOp          string `json:"step_op,omitempty" toml:"step_op,omitempty"`
	StateParam      string `json:"state_param,omitempty" toml:"state_param,omitempty"`
	GenerationParam string `json:"generation_param,omitempty" toml:"generation_param,omitempty"`
	IntervalParam   string `json:"interval_param,omitempty" toml:"interval_param,omitempty"`
	LimitParam      string `json:"limit_param,omitempty" toml:"limit_param,omitempty"`
	WrapParam       string `json:"wrap_param,omitempty" toml:"wrap_param,omitempty"`
	DefaultInterval int    `json:"default_interval,omitempty" toml:"default_interval,omitempty"`
	MinInterval     int    `json:"min_interval,omitempty" toml:"min_interval,omitempty"`
	MaxInterval     int    `json:"max_interval,omitempty" toml:"max_interval,omitempty"`
}

type openAIConnectionOverrides struct {
	BaseURL *string `json:"base_url,omitempty"`
	APIKey  *string `json:"api_key,omitempty"`
}

type nativeAdapterFactory func(s *Server, ownerPath string, owner Route, spec NativeRouteSpec) (http.HandlerFunc, error)

var nativeAdapterFactories = map[string]nativeAdapterFactory{
	"openai-compatible": buildOpenAICompatibleHandler,
	"wasm-sse":          buildWASMSSEHandler,
}

func (s *Server) registerConfiguredNativeRoutes() error {
	for ownerPath, owner := range s.cfg.Routes {
		for _, spec := range owner.NativeRoutes {
			if strings.TrimSpace(spec.Path) == "" {
				return fmt.Errorf("route %s declares a native route without path", ownerPath)
			}
			factory, ok := nativeAdapterFactories[strings.ToLower(strings.TrimSpace(spec.Adapter))]
			if !ok {
				return fmt.Errorf("route %s native route %s uses unknown adapter %q", ownerPath, spec.Path, spec.Adapter)
			}
			h, err := factory(s, ownerPath, owner, spec)
			if err != nil {
				return fmt.Errorf("route %s native route %s: %w", ownerPath, spec.Path, err)
			}
			s.RegisterNativeHandler(spec.Path, enforceNativeMethods(spec.Methods, h))
		}
	}
	return nil
}

func enforceNativeMethods(methods []string, next http.HandlerFunc) http.HandlerFunc {
	if len(methods) == 0 {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		for _, m := range methods {
			if strings.EqualFold(m, r.Method) {
				next(w, r)
				return
			}
		}
		w.Header().Set("Allow", strings.Join(methods, ", "))
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func buildOpenAICompatibleHandler(_ *Server, ownerPath string, owner Route, spec NativeRouteSpec) (http.HandlerFunc, error) {
	cfg := defaultOpenAIAdapter(spec.OpenAI)
	client := &http.Client{Timeout: time.Duration(cfg.TimeoutMs) * time.Millisecond}
	baseURL := strings.TrimRight(defaultEnv(owner.Env, cfg.BaseURLEnv, "http://localhost:1234/v1"), "/")
	apiKey := defaultEnv(owner.Env, cfg.APIKeyEnv, "lm-studio")
	model := defaultEnv(owner.Env, cfg.ModelEnv, "")
	systemPrompt := defaultEnv(owner.Env, cfg.SystemEnv, "You are a helpful assistant.")

	switch cfg.Operation {
	case "models":
		return func(w http.ResponseWriter, r *http.Request) {
			payload := openAIConnectionOverrides{}
			if r.Method == http.MethodPost {
				defer r.Body.Close()
				dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
				if err := dec.Decode(&payload); err != nil {
					writeNativeError(w, http.StatusBadRequest, "invalid JSON body")
					return
				}
			} else {
				q := r.URL.Query()
				if raw, ok := q["base_url"]; ok {
					value := strings.TrimSpace(firstNativeValue(raw))
					payload.BaseURL = &value
				}
				if raw, ok := q["api_key"]; ok {
					value := strings.TrimSpace(firstNativeValue(raw))
					payload.APIKey = &value
				}
			}
			effectiveBaseURL := strings.TrimRight(resolveOpenAIOverride(payload.BaseURL, baseURL), "/")
			effectiveAPIKey := resolveOpenAIOverride(payload.APIKey, apiKey)
			req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, effectiveBaseURL+"/models", nil)
			if err != nil {
				http.Error(w, `{"error":"failed to build request"}`, http.StatusInternalServerError)
				return
			}
			if strings.TrimSpace(effectiveAPIKey) != "" {
				req.Header.Set("Authorization", "Bearer "+effectiveAPIKey)
			}
			req.Header.Set("Accept", "application/json")
			resp, err := client.Do(req)
			if err != nil {
				writeNativeError(w, http.StatusBadGateway, fmt.Sprintf("upstream unreachable: %s", err.Error()))
				return
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				http.Error(w, `{"error":"failed to read response"}`, http.StatusInternalServerError)
				return
			}
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				if normalized, err := normalizeOpenAIModelsResponse(body); err == nil {
					body = normalized
				}
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(body)
		}, nil
	case "chat":
		return func(w http.ResponseWriter, r *http.Request) {
			type chatMessage struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			}
			type chatRequest struct {
				BaseURL     *string       `json:"base_url,omitempty"`
				APIKey      *string       `json:"api_key,omitempty"`
				Message     string        `json:"message"`
				System      string        `json:"system"`
				Model       string        `json:"model"`
				Temperature *float64      `json:"temperature,omitempty"`
				MaxTokens   *int          `json:"max_tokens,omitempty"`
				Stream      *bool         `json:"stream,omitempty"`
				History     []chatMessage `json:"history,omitempty"`
			}

			payload := chatRequest{}
			if r.Method == http.MethodPost {
				defer r.Body.Close()
				dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
				if err := dec.Decode(&payload); err != nil {
					writeNativeError(w, http.StatusBadRequest, "invalid JSON body")
					return
				}
			} else {
				q := r.URL.Query()
				if raw, ok := q["base_url"]; ok {
					value := strings.TrimSpace(firstNativeValue(raw))
					payload.BaseURL = &value
				}
				if raw, ok := q["api_key"]; ok {
					value := strings.TrimSpace(firstNativeValue(raw))
					payload.APIKey = &value
				}
				payload.Message = strings.TrimSpace(q.Get("message"))
				payload.System = strings.TrimSpace(q.Get("system"))
				payload.Model = strings.TrimSpace(q.Get("model"))
				if qv := strings.TrimSpace(q.Get("temperature")); qv != "" {
					if parsed, err := strconv.ParseFloat(qv, 64); err == nil {
						payload.Temperature = &parsed
					}
				}
				if qv := strings.TrimSpace(q.Get("max_tokens")); qv != "" {
					if parsed, err := strconv.Atoi(qv); err == nil {
						payload.MaxTokens = &parsed
					}
				}
				if histJSON := q.Get("history"); histJSON != "" {
					_ = json.Unmarshal([]byte(histJSON), &payload.History)
				}
				if qv := strings.TrimSpace(q.Get("stream")); qv != "" {
					stream := parseNativeBool(qv)
					payload.Stream = &stream
				}
			}

			message := strings.TrimSpace(payload.Message)
			if message == "" {
				writeNativeError(w, http.StatusBadRequest, "missing message")
				return
			}
			effectiveBaseURL := strings.TrimRight(resolveOpenAIOverride(payload.BaseURL, baseURL), "/")
			effectiveAPIKey := resolveOpenAIOverride(payload.APIKey, apiKey)
			effectiveSystem := systemPrompt
			if qv := strings.TrimSpace(payload.System); qv != "" {
				effectiveSystem = qv
			}
			effectiveModel := model
			if qv := strings.TrimSpace(payload.Model); qv != "" {
				effectiveModel = qv
			}
			temperature := 0.7
			if payload.Temperature != nil {
				temperature = *payload.Temperature
			}
			maxTokens := 2048
			if payload.MaxTokens != nil {
				maxTokens = *payload.MaxTokens
			}
			stream := payload.Stream != nil && *payload.Stream
			messages := []chatMessage{{Role: "system", Content: effectiveSystem}}
			if len(payload.History) > 0 {
				messages = append(messages, payload.History...)
			}
			messages = append(messages, chatMessage{Role: "user", Content: message})
			reqPayload := map[string]any{
				"messages":    messages,
				"temperature": temperature,
				"max_tokens":  maxTokens,
			}
			if effectiveModel != "" {
				reqPayload["model"] = effectiveModel
			}
			if stream {
				reqPayload["stream"] = true
				reqPayload["stream_options"] = map[string]any{"include_usage": true}
			}
			bodyBytes, err := json.Marshal(reqPayload)
			if err != nil {
				http.Error(w, `{"error":"failed to marshal request"}`, http.StatusInternalServerError)
				return
			}
			req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, effectiveBaseURL+"/chat/completions", bytes.NewReader(bodyBytes))
			if err != nil {
				http.Error(w, `{"error":"failed to build request"}`, http.StatusInternalServerError)
				return
			}
			req.Header.Set("Content-Type", "application/json")
			if strings.TrimSpace(effectiveAPIKey) != "" {
				req.Header.Set("Authorization", "Bearer "+effectiveAPIKey)
			}
			if stream {
				req.Header.Set("Accept", "text/event-stream")
			} else {
				req.Header.Set("Accept", "application/json")
			}
			resp, err := client.Do(req)
			if err != nil {
				writeNativeError(w, http.StatusBadGateway, fmt.Sprintf("upstream unreachable: %s", err.Error()))
				return
			}
			defer resp.Body.Close()
			if stream {
				if resp.StatusCode < 200 || resp.StatusCode >= 300 {
					body, readErr := io.ReadAll(resp.Body)
					if readErr != nil {
						writeNativeError(w, http.StatusBadGateway, "failed to read upstream error response")
						return
					}
					contentType := resp.Header.Get("Content-Type")
					if contentType == "" {
						contentType = "application/json"
					}
					w.Header().Set("Content-Type", contentType)
					w.WriteHeader(resp.StatusCode)
					_, _ = w.Write(body)
					return
				}
				if err := proxyNativeStream(w, resp.Body); err != nil {
					return
				}
				return
			}
			var apiResp struct {
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
				writeNativeError(w, http.StatusBadGateway, "failed to parse upstream response")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if apiResp.Error != nil {
				w.WriteHeader(http.StatusBadGateway)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": apiResp.Error.Message})
				return
			}
			if len(apiResp.Choices) == 0 {
				writeNativeError(w, http.StatusBadGateway, "no choices returned by upstream")
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": apiResp.Choices[0].Message.Content,
				"role":    apiResp.Choices[0].Message.Role,
				"model":   apiResp.Model,
				"usage":   apiResp.Usage,
			})
		}, nil
	default:
		return nil, fmt.Errorf("owner %s uses unsupported openai operation %q", ownerPath, cfg.Operation)
	}
}

func proxyNativeStream(w http.ResponseWriter, body io.Reader) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeNativeError(w, http.StatusInternalServerError, "streaming unsupported")
		return fmt.Errorf("streaming unsupported")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if _, err := fmt.Fprintln(w, scanner.Text()); err != nil {
			return err
		}
		flusher.Flush()
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	if err == nil {
		flusher.Flush()
	}
	return err
}

func normalizeOpenAIModelsResponse(body []byte) ([]byte, error) {
	models, err := extractOpenAIModelIDs(body)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"models": models})
}

func extractOpenAIModelIDs(body []byte) ([]string, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if models := extractModelStrings(payload["models"]); len(models) > 0 {
		return models, nil
	}
	if models := extractModelStrings(payload["data"]); len(models) > 0 {
		return models, nil
	}
	return []string{}, nil
}

func extractModelStrings(raw any) []string {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	models := make([]string, 0, len(items))
	for _, item := range items {
		switch value := item.(type) {
		case string:
			value = strings.TrimSpace(value)
			if value != "" {
				models = append(models, value)
			}
		case map[string]any:
			if id, ok := value["id"].(string); ok {
				id = strings.TrimSpace(id)
				if id != "" {
					models = append(models, id)
				}
			}
		}
	}
	return models
}

func resolveOpenAIOverride(override *string, fallback string) string {
	if override != nil {
		return strings.TrimSpace(*override)
	}
	return fallback
}

func firstNativeValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func buildWASMSSEHandler(s *Server, ownerPath string, owner Route, spec NativeRouteSpec) (http.HandlerFunc, error) {
	stream := defaultWASMSSEAdapter(spec.WASMSSE)
	sourceRoute := owner
	if stream.SourceRoute != "" && stream.SourceRoute != ownerPath {
		resolved, ok := s.cfg.Routes[stream.SourceRoute]
		if !ok {
			return nil, fmt.Errorf("unknown source_route %q", stream.SourceRoute)
		}
		sourceRoute = resolved
	}
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		q := r.URL.Query()
		interval := clampNativeInt(parseNativeInt(q.Get(stream.IntervalParam), stream.DefaultInterval), stream.MinInterval, stream.MaxInterval)
		maxGenerations := clampNativeInt(parseNativeInt(q.Get(stream.LimitParam), 0), 0, 50000)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		frame, err := s.wasmSSEInitialFrame(r, &sourceRoute, stream)
		if err != nil {
			s.writeSSEvent(w, flusher, "error", map[string]string{"error": err.Error()})
			return
		}
		s.writeSSEvent(w, flusher, "generation", frame)
		if maxGenerations > 0 && frame.Generation >= maxGenerations {
			s.writeSSEvent(w, flusher, "done", map[string]any{"generation": frame.Generation})
			return
		}
		ticker := time.NewTicker(time.Duration(interval) * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				next, err := s.wasmSSEStepFrame(r.Context(), &sourceRoute, stream, frame)
				if err != nil {
					s.writeSSEvent(w, flusher, "error", map[string]string{"error": err.Error()})
					return
				}
				frame = next
				s.writeSSEvent(w, flusher, "generation", frame)
				if maxGenerations > 0 && frame.Generation >= maxGenerations {
					s.writeSSEvent(w, flusher, "done", map[string]any{"generation": frame.Generation})
					return
				}
			}
		}
	}, nil
}

type streamFrame struct {
	State      string `json:"state"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Generation int    `json:"generation"`
	Alive      int    `json:"alive"`
	Pattern    string `json:"pattern,omitempty"`
	Wrap       bool   `json:"wrap"`
	Error      string `json:"error,omitempty"`
}

func (s *Server) wasmSSEInitialFrame(r *http.Request, route *Route, cfg WASMSSEAdapter) (streamFrame, error) {
	q := r.URL.Query()
	seed, _ := readRandomSeed()
	state := strings.TrimSpace(q.Get(cfg.StateParam))
	if state != "" {
		gen := parseNativeInt(q.Get(cfg.GenerationParam), 0)
		return streamFrame{
			State:      state,
			Width:      clampNativeInt(parseNativeInt(q.Get("width"), 96), 16, 256),
			Height:     clampNativeInt(parseNativeInt(q.Get("height"), 54), 16, 144),
			Generation: gen,
			Alive:      countNativeAlive(state),
			Pattern:    defaultNativeString(q.Get("pattern"), "resume"),
			Wrap:       parseNativeBool(q.Get(cfg.WrapParam)),
		}, nil
	}
	params := map[string]string{
		"op":      cfg.SeedOp,
		"pattern": defaultNativeString(q.Get("pattern"), "random"),
		"width":   defaultNativeString(q.Get("width"), "96"),
		"height":  defaultNativeString(q.Get("height"), "54"),
		"density": defaultNativeString(q.Get("density"), "0.23"),
	}
	if parseNativeBool(q.Get(cfg.WrapParam)) {
		params[cfg.WrapParam] = "1"
	}
	return s.execWASMFrame(r.Context(), route, params, seed, r.Header.Get("X-Request-ID"))
}

func (s *Server) wasmSSEStepFrame(ctx context.Context, route *Route, cfg WASMSSEAdapter, frame streamFrame) (streamFrame, error) {
	params := map[string]string{
		"op":                 cfg.StepOp,
		cfg.StateParam:        frame.State,
		cfg.GenerationParam:   strconv.Itoa(frame.Generation),
	}
	if frame.Wrap {
		params[cfg.WrapParam] = "1"
	}
	return s.execWASMFrame(ctx, route, params, 0, "")
}

func (s *Server) execWASMFrame(ctx context.Context, route *Route, params map[string]string, seed int64, requestID string) (streamFrame, error) {
	payload := requestPayload{Params: params, Headers: map[string]string{}, RequestID: requestID, Seed: seed}
	stdin, err := json.Marshal(payload)
	if err != nil {
		return streamFrame{}, err
	}
	var out bytes.Buffer
	if err := s.runWASM(ctx, route, stdin, &out); err != nil {
		return streamFrame{}, err
	}
	var frame streamFrame
	if err := json.Unmarshal(out.Bytes(), &frame); err != nil {
		return streamFrame{}, fmt.Errorf("parse wasm frame: %w", err)
	}
	if frame.Error != "" {
		return streamFrame{}, fmt.Errorf("%s", frame.Error)
	}
	return frame, nil
}

func (s *Server) writeSSEvent(w http.ResponseWriter, flusher http.Flusher, event string, payload any) {
	data, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(w, "event: %s\n", event)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

func defaultOpenAIAdapter(cfg *OpenAIAdapter) OpenAIAdapter {
	out := OpenAIAdapter{
		Operation:  "models",
		BaseURLEnv: "OPENAI_BASE_URL",
		APIKeyEnv:  "OPENAI_API_KEY",
		ModelEnv:   "OPENAI_MODEL",
		SystemEnv:  "OPENAI_SYSTEM",
		TimeoutMs:  120000,
	}
	if cfg == nil {
		return out
	}
	if cfg.Operation != "" { out.Operation = cfg.Operation }
	if cfg.BaseURLEnv != "" { out.BaseURLEnv = cfg.BaseURLEnv }
	if cfg.APIKeyEnv != "" { out.APIKeyEnv = cfg.APIKeyEnv }
	if cfg.ModelEnv != "" { out.ModelEnv = cfg.ModelEnv }
	if cfg.SystemEnv != "" { out.SystemEnv = cfg.SystemEnv }
	if cfg.TimeoutMs > 0 { out.TimeoutMs = cfg.TimeoutMs }
	return out
}

func defaultWASMSSEAdapter(cfg *WASMSSEAdapter) WASMSSEAdapter {
	out := WASMSSEAdapter{
		SeedOp:          "seed",
		StepOp:          "step",
		StateParam:      "state",
		GenerationParam: "generation",
		IntervalParam:   "interval",
		LimitParam:      "generations",
		WrapParam:       "wrap",
		DefaultInterval: 90,
		MinInterval:     30,
		MaxInterval:     2000,
	}
	if cfg == nil {
		return out
	}
	if cfg.SourceRoute != "" { out.SourceRoute = cfg.SourceRoute }
	if cfg.SeedOp != "" { out.SeedOp = cfg.SeedOp }
	if cfg.StepOp != "" { out.StepOp = cfg.StepOp }
	if cfg.StateParam != "" { out.StateParam = cfg.StateParam }
	if cfg.GenerationParam != "" { out.GenerationParam = cfg.GenerationParam }
	if cfg.IntervalParam != "" { out.IntervalParam = cfg.IntervalParam }
	if cfg.LimitParam != "" { out.LimitParam = cfg.LimitParam }
	if cfg.WrapParam != "" { out.WrapParam = cfg.WrapParam }
	if cfg.DefaultInterval > 0 { out.DefaultInterval = cfg.DefaultInterval }
	if cfg.MinInterval > 0 { out.MinInterval = cfg.MinInterval }
	if cfg.MaxInterval > 0 { out.MaxInterval = cfg.MaxInterval }
	return out
}

func defaultEnv(env map[string]string, key, fallback string) string {
	if env != nil {
		if v := strings.TrimSpace(env[key]); v != "" {
			return v
		}
	}
	return fallback
}

func writeNativeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func parseNativeInt(v string, fallback int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
		return n
	}
	return fallback
}

func clampNativeInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func parseNativeBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func defaultNativeString(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func countNativeAlive(state string) int {
	total := 0
	for i := 0; i < len(state); i++ {
		if state[i] == '1' {
			total++
		}
	}
	return total
}
