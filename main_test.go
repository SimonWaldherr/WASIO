package main

import "testing"

func TestGetInstrumentExamplesIncludesPrimaryAndAdditionalExamples(t *testing.T) {
	route := Route{
		Example: "?name=Alice",
		Examples: []RouteExample{
			{Label: "Workshop intro", Query: "name=WASIO"},
			{Label: "Duplicate", Query: "?name=Alice"},
			{Label: "Ignored empty", Query: ""},
		},
	}

	got := getInstrumentExamples("/hello_world", route)
	if len(got) != 2 {
		t.Fatalf("expected 2 unique examples, got %d: %#v", len(got), got)
	}

	if got[0].Label != "Default example" || got[0].Query != "?name=Alice" {
		t.Fatalf("unexpected primary example: %#v", got[0])
	}

	if got[1].Label != "Workshop intro" || got[1].Query != "name=WASIO" {
		t.Fatalf("unexpected additional example: %#v", got[1])
	}
}

func TestBuildExamplePathNormalizesQuery(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		query string
		want  string
	}{
		{name: "empty query", path: "/chat", query: "", want: "/chat"},
		{name: "already prefixed", path: "/calculator", query: "?op=add&a=1&b=2", want: "/calculator?op=add&a=1&b=2"},
		{name: "missing prefix", path: "/url_utils", query: "op=parse", want: "/url_utils?op=parse"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildExamplePath(tt.path, tt.query); got != tt.want {
				t.Fatalf("buildExamplePath(%q, %q) = %q, want %q", tt.path, tt.query, got, tt.want)
			}
		})
	}
}

func TestGetInstrumentUseCaseFallback(t *testing.T) {
	if got := getInstrumentUseCase("custom", Route{}); got != "General-purpose WebAssembly-backed endpoint" {
		t.Fatalf("unexpected fallback use case: %q", got)
	}
}
