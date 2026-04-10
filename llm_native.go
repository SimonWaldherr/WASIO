// llm_native.go
//
// Native (non-WASM) handlers for the LLM instrument.
//
// WASI preview1 has no socket support, so all outbound HTTP calls to the LLM
// API (e.g. LM Studio / OpenAI-compatible endpoints) are handled here in the
// WASIO host rather than inside the llm.wasm module.
//
// Configuration is read from the /llm route's "env" block in config.json:
//
//	OPENAI_BASE_URL  – base URL of the OpenAI-compatible API (default: http://localhost:1234/v1)
//	OPENAI_API_KEY   – API key (default: "lm-studio")
//	OPENAI_MODEL     – default model name (empty = server default)
//	OPENAI_SYSTEM    – system prompt sent to the model
//
// The handlers are registered automatically via init(), so adding this file to
// the build is all that is required – nothing in main.go needs to change.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func init() {
	nativeHandlerRegistrations = append(nativeHandlerRegistrations, func(s *Server) {
		s.RegisterNativeHandler("/_llm/models", s.llmModelsHandler)
		s.RegisterNativeHandler("/_llm/chat", s.llmChatHandler)
	})
}

// llmSettings holds the resolved LLM API connection settings.
type llmSettings struct {
	BaseURL      string
	APIKey       string
	Model        string
	SystemPrompt string
}

// llmBaseConfig reads LLM settings from the /llm route's env block in config.json.
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

// newLLMClient returns an HTTP client suitable for LLM API calls.
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
//	message     – user's current message (required)
//	system      – override system prompt (optional)
//	model       – override model name (optional)
//	temperature – float, default 0.7 (optional)
//	max_tokens  – integer, default 2048 (optional)
//	history     – JSON array of {"role":"user"|"assistant","content":"..."} (optional)
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
	if sp := q.Get("system"); sp != "" {
		systemPrompt = sp
	}
	model := cfg.Model
	if m := q.Get("model"); m != "" {
		model = m
	}
	temperature := 0.7
	if t := q.Get("temperature"); t != "" {
		if n, err := fmt.Sscanf(t, "%f", &temperature); n != 1 || err != nil {
			temperature = 0.7
		}
	}
	maxTokens := 2048
	if mt := q.Get("max_tokens"); mt != "" {
		if n, err := fmt.Sscanf(mt, "%d", &maxTokens); n != 1 || err != nil {
			maxTokens = 2048
		}
	}

	type chatMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	messages := []chatMessage{{Role: "system", Content: systemPrompt}}

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

	reqPayload := chatRequest{
		Model:       model,
		Messages:    messages,
		Temperature: temperature,
		MaxTokens:   maxTokens,
	}
	bodyBytes, err := json.Marshal(reqPayload)
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
