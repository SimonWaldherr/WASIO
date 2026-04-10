// cli.go
//
// WASIO command-line interface.
//
// This file adds Docker-/Caddy-style subcommands to the wasio binary so
// operators can manage a running server without editing files manually.
//
// Usage:
//
//	wasio serve  [--config path] [--port port]   # start server (default)
//	wasio list   [--config path] [--json]         # list configured instruments
//	wasio info   [--config path] <route>          # show route details
//	wasio reload [--server url]  [route]          # hot-reload WASM module(s)
//	wasio add    [--config path] [--route /path]  # install instrument from URL/file
//	             [--category x]  [--desc "..."]   <wasm-url-or-path>
//	wasio validate [--config path]                # validate config.json
//	wasio version                                 # print version
//	wasio help                                    # print this help

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
)

// Version is the WASIO release string embedded at link time via -ldflags.
var Version = "0.1.0"

// cmdHelp prints usage information and exits.
func cmdHelp() {
	fmt.Print(`wasio – WebAssembly System Interface Orchestrator ` + Version + `

Usage:  wasio [command] [flags]

Commands:
  serve      Start the WASIO HTTP server (default when no command is given)
  list       List all configured instruments
  info       Show detailed information for a single route
  reload     Hot-reload instrument(s) without restarting the server
	init       Scaffold a new WASIO package with wasio.toml
	pull       Install an instrument from a wasio.toml manifest
  add        Install a new instrument from a URL or local file
	keygen     Generate an ed25519 signing keypair
	sign       Sign manifest capability declarations
  validate   Validate the configuration file
  version    Print version
  help       Show this help

Flags – serve:
  --config string   Config file path (default: config.json)
  --port   string   Override the listen port from the config

Flags – list / info / validate / add:
  --config string   Config file path (default: config.json)

Flags – init:
	--dir      string   Package directory to create (default: .)
	--lang     string   Package language: go or rust

Flags – pull:
	--config            Config file path (default: config.json)
	--require-signature Fail if the manifest is unsigned or invalid
	--public-key string Override the manifest public key

Flags – reload:
  --server string   Base URL of the running WASIO server (default: http://localhost:8080)

Flags – add:
  --route    string   URL path for the new instrument  (default: /<basename>)
  --category string   Instrument category shown in the UI (default: Custom)
  --desc     string   Short description shown in the UI

Examples:
  wasio serve --config /etc/wasio/config.json
  wasio list
  wasio list --json
  wasio info /calculator
	wasio init --dir ./my-tool --lang go --name my-tool
	wasio pull https://example.com/wasio.toml --require-signature
  wasio reload
  wasio reload /calculator
  wasio add https://example.com/my_tool.wasm --route /my-tool --desc "My custom tool"
	wasio keygen --out keys/wasio
	wasio sign --private-key keys/wasio.key ./wasio.toml
  wasio validate
`)
}

// cmdList reads config.json and prints a table of all configured routes.
func cmdList(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	configPath := fs.String("config", "config.json", "path to config file")
	jsonOut := fs.Bool("json", false, "output JSON instead of a table")
	fs.Parse(args) //nolint:errcheck

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(cfg.Routes) //nolint:errcheck
		return
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ROUTE\tCATEGORY\tCACHE\tTIMEOUT\tWASM FILE\tDESCRIPTION")
	fmt.Fprintln(tw, "-----\t--------\t-----\t-------\t---------\t-----------")

	for _, path := range sortedKeys(cfg.Routes) {
		r := cfg.Routes[path]
		cat := r.Category
		if cat == "" {
			cat = "–"
		}
		cache := "no"
		if r.Cache {
			ttl := r.TTL
			if ttl <= 0 {
				ttl = cfg.CacheTTL
			}
			cache = fmt.Sprintf("%ds", ttl)
		}
		timeout := "–"
		if r.TimeoutMs > 0 {
			timeout = fmt.Sprintf("%dms", r.TimeoutMs)
		} else if cfg.DefaultTimeout > 0 {
			timeout = fmt.Sprintf("%dms*", cfg.DefaultTimeout)
		}
		desc := r.Description
		if len(desc) > 60 {
			desc = desc[:57] + "…"
		}
		if desc == "" {
			desc = "–"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			path, cat, cache, timeout, r.WASMFile, desc)
	}
	tw.Flush()
}

// cmdInfo prints full details for a single route.
func cmdInfo(args []string) {
	fs := flag.NewFlagSet("info", flag.ExitOnError)
	configPath := fs.String("config", "config.json", "path to config file")
	fs.Parse(args) //nolint:errcheck

	route := fs.Arg(0)
	if route == "" {
		fmt.Fprintln(os.Stderr, "usage: wasio info <route>")
		os.Exit(1)
	}

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	r, ok := cfg.Routes[route]
	if !ok {
		fmt.Fprintf(os.Stderr, "route %q not found in %s\n", route, *configPath)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(map[string]interface{}{ //nolint:errcheck
		"path":        route,
		"wasm_file":   r.WASMFile,
		"category":    r.Category,
		"description": r.Description,
		"use_case":    r.UseCase,
		"cache":       r.Cache,
		"ttl":         getTTL(r, cfg.CacheTTL),
		"timeout_ms":  r.TimeoutMs,
		"methods":     r.Methods,
		"env":         r.Env,
		"examples":    r.Examples,
	})
}

// cmdReload calls the /_reload endpoint on a running WASIO server.
//
//	wasio reload              – flush all module caches
//	wasio reload /calculator  – flush only the /calculator module
//	wasio reload --all        – flush module + response caches
func cmdReload(args []string) {
	fs := flag.NewFlagSet("reload", flag.ExitOnError)
	serverURL := fs.String("server", "http://localhost:8080", "base URL of running WASIO server")
	all := fs.Bool("all", false, "also flush the response cache")
	fs.Parse(args) //nolint:errcheck

	target := *serverURL + "/_reload"
	params := url.Values{}
	if route := fs.Arg(0); route != "" {
		params.Set("route", route)
	}
	if *all {
		params.Set("all", "1")
	}
	if len(params) > 0 {
		target += "?" + params.Encode()
	}

	resp, err := http.Get(target) //nolint:gosec // URL constructed from user flags
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "server returned %d: %s\n", resp.StatusCode, body)
		os.Exit(1)
	}
	var result map[string]interface{}
	if json.Unmarshal(body, &result) == nil {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(result) //nolint:errcheck
	} else {
		fmt.Println(string(body))
	}
}

// cmdAdd downloads (or copies) a .wasm file and registers it as a new instrument
// in config.json.
//
//	wasio add https://example.com/tool.wasm --route /tool --category Utils --desc "My tool"
//	wasio add ./local.wasm --route /tool
func cmdAdd(args []string) {
	fs := flag.NewFlagSet("add", flag.ExitOnError)
	configPath := fs.String("config", "config.json", "path to config file")
	routePath := fs.String("route", "", "URL path for the new instrument (default: /<basename>)")
	category := fs.String("category", "Custom", "instrument category shown in the UI")
	desc := fs.String("desc", "", "short description")
	useCase := fs.String("use-case", "", "practical use case description")
	fs.Parse(args) //nolint:errcheck

	src := fs.Arg(0)
	if src == "" {
		fmt.Fprintln(os.Stderr, "usage: wasio add <wasm-url-or-path> [flags]")
		fs.PrintDefaults()
		os.Exit(1)
	}

	baseName := filepath.Base(src)
	if !strings.HasSuffix(baseName, ".wasm") {
		baseName += ".wasm"
	}
	destFile := filepath.Join("instruments", baseName)

	if *routePath == "" {
		stem := strings.TrimSuffix(baseName, ".wasm")
		*routePath = "/" + stem
	}

	// Download or copy the WASM file.
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		fmt.Printf("downloading %s → %s\n", src, destFile)
		if err := downloadFile(src, destFile); err != nil {
			fmt.Fprintf(os.Stderr, "download failed: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Printf("copying %s → %s\n", src, destFile)
		if err := copyFile(src, destFile); err != nil {
			fmt.Fprintf(os.Stderr, "copy failed: %v\n", err)
			os.Exit(1)
		}
	}

	// Load, patch, and save config.json.
	cfg, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	if _, exists := cfg.Routes[*routePath]; exists {
		fmt.Fprintf(os.Stderr, "warning: route %q already exists – overwriting\n", *routePath)
	}

	cfg.Routes[*routePath] = Route{
		WASMFile:    destFile,
		Category:    *category,
		Description: *desc,
		UseCase:     *useCase,
		Examples: []RouteExample{
			{Label: "Default", Query: ""},
		},
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error serialising config: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*configPath, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ registered %s at %s\n", destFile, *routePath)
	fmt.Printf("  reload with: wasio reload %s\n", *routePath)
}

// cmdValidate loads and validates the configuration file, printing any errors.
func cmdValidate(args []string) {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	configPath := fs.String("config", "config.json", "path to config file")
	fs.Parse(args) //nolint:errcheck

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ invalid: %v\n", err)
		os.Exit(1)
	}

	// Basic sanity checks
	warnings := 0
	for path, route := range cfg.Routes {
		if _, err := os.Stat(route.WASMFile); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "  warning: %s → WASM file not found: %s\n", path, route.WASMFile)
			warnings++
		}
	}

	fmt.Printf("✓ config valid  (%d routes, %d warning(s))\n", len(cfg.Routes), warnings)
	if warnings > 0 {
		os.Exit(2) // exit 2 = valid JSON but with warnings
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func sortedKeys(m map[string]Route) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// simple insertion sort (small maps)
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

func downloadFile(rawURL, dest string) error {
	resp, err := http.Get(rawURL) //nolint:gosec // URL provided by operator via CLI
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func copyFile(src, dest string) error {
	in, err := os.Open(src) //nolint:gosec // path provided by operator via CLI
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
