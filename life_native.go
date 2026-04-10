package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type lifeFrame struct {
	State      string `json:"state"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Generation int    `json:"generation"`
	Alive      int    `json:"alive"`
	Pattern    string `json:"pattern,omitempty"`
	Wrap       bool   `json:"wrap"`
	Error      string `json:"error,omitempty"`
}

func init() {
	nativeHandlerRegistrations = append(nativeHandlerRegistrations, func(s *Server) {
		s.RegisterNativeHandler("/_life/stream", s.lifeStreamHandler)
	})
}

func (s *Server) lifeStreamHandler(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	route, ok := s.cfg.Routes["/life"]
	if !ok {
		http.Error(w, "life instrument not configured", http.StatusServiceUnavailable)
		return
	}

	q := r.URL.Query()
	interval := clampNativeInt(parseNativeInt(q.Get("interval"), 90), 30, 2000)
	maxGenerations := clampNativeInt(parseNativeInt(q.Get("generations"), 0), 0, 50000)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	frame, err := s.lifeInitialFrame(r, &route)
	if err != nil {
		s.lifeWriteEvent(w, flusher, "error", map[string]string{"error": err.Error()})
		return
	}
	s.lifeWriteEvent(w, flusher, "generation", frame)

	if maxGenerations > 0 && frame.Generation >= maxGenerations {
		s.lifeWriteEvent(w, flusher, "done", map[string]any{"generation": frame.Generation})
		return
	}

	ticker := time.NewTicker(time.Duration(interval) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			next, err := s.lifeStepFrame(r.Context(), &route, frame)
			if err != nil {
				s.lifeWriteEvent(w, flusher, "error", map[string]string{"error": err.Error()})
				return
			}
			frame = next
			s.lifeWriteEvent(w, flusher, "generation", frame)
			if maxGenerations > 0 && frame.Generation >= maxGenerations {
				s.lifeWriteEvent(w, flusher, "done", map[string]any{"generation": frame.Generation})
				return
			}
		}
	}
}

func (s *Server) lifeInitialFrame(r *http.Request, route *Route) (lifeFrame, error) {
	q := r.URL.Query()
	seed, _ := readRandomSeed()
	params := map[string]string{
		"op":      "seed",
		"pattern": defaultNativeString(q.Get("pattern"), "random"),
		"width":   defaultNativeString(q.Get("width"), "96"),
		"height":  defaultNativeString(q.Get("height"), "54"),
		"density": defaultNativeString(q.Get("density"), "0.23"),
	}
	if parseNativeBool(q.Get("wrap")) {
		params["wrap"] = "1"
	}
	if state := strings.TrimSpace(q.Get("state")); state != "" {
		params = map[string]string{
			"op":         "step",
			"state":      state,
			"generation": defaultNativeString(q.Get("generation"), "-1"),
		}
		if parseNativeBool(q.Get("wrap")) {
			params["wrap"] = "1"
		}
		// Special case: client resume should continue from the exact current frame,
		// not immediately skip one. We ask for generation-1 then step once below? no.
		// Instead decode the provided state by calling seedless passthrough isn't available,
		// so return a synthetic frame directly.
		gen := parseNativeInt(q.Get("generation"), 0)
		return lifeFrame{
			State:      state,
			Width:      clampNativeInt(parseNativeInt(q.Get("width"), 96), 16, 256),
			Height:     clampNativeInt(parseNativeInt(q.Get("height"), 54), 16, 144),
			Generation: gen,
			Alive:      countNativeAlive(state),
			Pattern:    defaultNativeString(q.Get("pattern"), "resume"),
			Wrap:       parseNativeBool(q.Get("wrap")),
		}, nil
	}
	return s.lifeExec(r.Context(), route, params, seed, r.Header.Get("X-Request-ID"))
}

func (s *Server) lifeStepFrame(ctx context.Context, route *Route, frame lifeFrame) (lifeFrame, error) {
	params := map[string]string{
		"op":         "step",
		"state":      frame.State,
		"generation": strconv.Itoa(frame.Generation),
	}
	if frame.Wrap {
		params["wrap"] = "1"
	}
	return s.lifeExec(ctx, route, params, 0, "")
}

func (s *Server) lifeExec(ctx context.Context, route *Route, params map[string]string, seed int64, requestID string) (lifeFrame, error) {
	payload := requestPayload{
		Params:    params,
		Headers:   map[string]string{},
		RequestID: requestID,
		Seed:      seed,
	}
	stdin, err := json.Marshal(payload)
	if err != nil {
		return lifeFrame{}, err
	}
	var out bytes.Buffer
	if err := s.runWASM(ctx, route, stdin, &out); err != nil {
		return lifeFrame{}, err
	}
	var frame lifeFrame
	if err := json.Unmarshal(out.Bytes(), &frame); err != nil {
		return lifeFrame{}, fmt.Errorf("parse life frame: %w", err)
	}
	if frame.Error != "" {
		return lifeFrame{}, fmt.Errorf(frame.Error)
	}
	return frame, nil
}

func (s *Server) lifeWriteEvent(w http.ResponseWriter, flusher http.Flusher, event string, payload any) {
	data, _ := json.Marshal(payload)
	fmt.Fprintf(w, "event: %s\n", event)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
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
