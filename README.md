# WASIO - WebAssembly System Interface Orchestrator

[![DOI](https://zenodo.org/badge/884922110.svg)](https://doi.org/10.5281/zenodo.15257309)

[![Watch the video](https://img.youtube.com/vi/5B7Q5rJZhc0/maxresdefault.jpg)](https://youtu.be/5B7Q5rJZhc0)

WASIO is a self-hosted HTTP runtime for WebAssembly instruments. It loads WASI modules on demand, routes requests to them, injects structured request data via stdin, captures stdout as the response, and keeps the whole system in a single Go binary.

The product direction is deliberately pragmatic:

- Docker: simple packaging, install, and local workflows
- Cloudflare: fast edge-style request handling and native host bridges where WASI cannot reach the network
- wasmCloud: modular components and extensible host capabilities
- Spin: HTTP-first developer ergonomics for WebAssembly apps
- Caddy: one-binary deployment, strong defaults, and minimal operational friction

WASIO is not there yet, but the current codebase already has the right foundations: hot reload, structured config, native host extensions, observability, and a CLI.

## Current Status

WASIO currently provides:

- Dynamic route-to-WASM mapping through `config.json`
- WASM module caching with automatic mtime-based hot reload
- Response caching with per-route TTLs
- Structured JSON request payloads over stdin
- Optional filesystem mounts for WASI guests
- Native host handlers for features that pure WASI cannot do safely or directly
- A built-in instrument catalog UI and monitoring dashboard
- A CLI for operating and extending a deployment

WASIO is usable today as a local platform, internal tool runner, or self-hosted WASM gateway. It is not yet a full registry/orchestration platform.

## What Changed Recently

The project has evolved substantially beyond the original demo server. The important additions are:

- Declarative `native_routes` instead of hardcoded instrument-specific host files
- Host-side OpenAI-compatible LLM bridge for LM Studio / OpenAI-style APIs
- Automatic hot reload for rebuilt `.wasm` files plus `/_reload`
- Structured JSON access logs with optional request IDs
- CLI subcommands: `serve`, `list`, `info`, `reload`, `add`, `validate`, `version`, `help`
- Packaging manifest support via `wasio.toml`
- New packaging commands: `init`, `pull`, `keygen`, `sign`
- Signed capability declarations verified during `wasio pull`
- Global WASM execution timeout via `default_timeout_ms`
- Global WASM memory cap via `max_memory_pages`
- Request headers and request IDs forwarded into the WASM payload
- Improved instrument overview with category filters and search
- Rust instrument support via `make all-with-rust`
- New example instrument: `/hello_rust`

## Why WASIO Exists

Containers are powerful, but they are heavier than necessary for many request-driven tools. WASM gives better isolation and startup characteristics for small, single-purpose units. WASIO aims to make that model operationally boring:

- one binary
- one config file
- explicit capability exposure
- fast request startup
- easy local development
- no daemon or cluster required

The realistic positioning is closer to “Caddy for WASM HTTP tools” than “Kubernetes for everything.” If the operator experience becomes excellent, the platform can grow from there.

## Quick Start

### Requirements

- Go 1.25+
- TinyGo for Go-based instruments
- Rust + `wasm32-wasip1` target if you want Rust instruments

### Build

```bash
git clone https://github.com/SimonWaldherr/WASIO.git
cd WASIO

make build
make instruments
make run
```

To also build Rust instruments:

```bash
rustup target add wasm32-wasip1
make all-with-rust
```

### Run

```bash
./wasio serve
```

Then open:

- `http://localhost:8080/`
- `http://localhost:8080/monitoring`
- `http://localhost:8080/hello_world?name=WASIO`
- `http://localhost:8080/hello_rust?name=WASIO&lang=de`
- `http://localhost:8080/life?op=ui`

## CLI

WASIO now includes an operator-friendly CLI.

```bash
./wasio help
./wasio version
./wasio list
./wasio list --json
./wasio info /calculator
./wasio init --dir ./my-tool --lang go --name my-tool
./wasio pull --require-signature examples/manifests/hello_rust.toml
./wasio keygen --out keys/wasio
./wasio sign --private-key keys/wasio.key ./wasio.toml
./wasio validate
./wasio reload
./wasio reload /llm
./wasio add https://example.com/my_tool.wasm --route /my-tool --category Utils --desc "My custom tool"
```

This is one of the key steps toward the Docker/Caddy goal: users should operate the platform without editing internals manually.

## Packaging

WASIO now has a portable package manifest: `wasio.toml`.

It describes:

- package name and version
- language/toolchain
- module source and build command
- route metadata
- example requests
- capability declarations
- optional ed25519 signature over the capability declaration set

Example:

```toml
schema = 1
name = "hello_rust"
version = "0.1.0"
language = "rust"

[module]
source = "../../instruments/hello_rust.wasm"
build = "cargo build --target wasm32-wasip1 --release && cp target/wasm32-wasip1/release/hello_rust.wasm ../../instruments/hello_rust.wasm"

[route]
path = "/hello_rust"
methods = ["GET"]
timeout_ms = 1000

[metadata]
category = "Basic"
description = "Multilingual greeting service compiled from Rust"

[capabilities]
names = ["wasi:stdio", "wasi:env"]
native_routes = []
```

Author workflow:

```bash
./wasio init --dir ./my-tool --lang go --name my-tool
./wasio keygen --out keys/wasio
./wasio sign --private-key keys/wasio.key ./my-tool/wasio.toml
```

Operator workflow:

```bash
./wasio pull --require-signature https://example.com/wasio.toml
./wasio reload /my-tool
```

This is the first real step toward a Docker-like packaging and distribution model.

## Runtime Model

For each request, WASIO:

1. Matches the HTTP path to a configured route.
2. Reuses or recompiles the `.wasm` module.
3. Builds a JSON request payload.
4. Instantiates the guest in a WASI sandbox.
5. Passes structured data on stdin.
6. Returns stdout as the HTTP response.

The request payload currently includes:

```json
{
  "params": {
    "name": "WASIO"
  },
  "headers": {
    "User-Agent": "curl/8.7.1"
  },
  "request_id": "6b16ea56a896fa53",
  "seed": 42
}
```

Notes:

- `Cookie` and `Set-Cookie` are intentionally not forwarded into guest payloads.
- Pure WASI modules do not have general outbound networking. If an instrument needs network access, implement it as a native host bridge.

## Native Routes

Some capabilities do not belong inside pure WASI modules. WASIO now supports declarative host-side companion routes via `native_routes` on any instrument.

The important design change is that these routes are no longer implemented as one-off files like `llm_native.go` or `life_native.go`. Instead, each instrument declares what it needs, and WASIO binds the matching generic adapter dynamically at startup.

Current built-in adapters:

- `openai-compatible`: host-side HTTP/HTTPS bridge for OpenAI-style APIs
- `wasm-sse`: streamed Server-Sent Events driven by repeated WASM execution

Example:

```json
{
  "/llm": {
    "wasm_file": "instruments/llm.wasm",
    "native_routes": [
      {
        "path": "/_llm/models",
        "transport": "https",
        "adapter": "openai-compatible",
        "methods": ["GET", "POST"],
        "openai": {
          "operation": "models"
        }
      },
      {
        "path": "/_llm/chat",
        "transport": "https",
        "adapter": "openai-compatible",
        "methods": ["POST"],
        "openai": {
          "operation": "chat"
        }
      }
    ]
  }
}
```

`/_llm/models` supports both `GET` and JSON `POST`. The `POST` form is useful when the UI wants to probe a different OpenAI-compatible base URL or API key without leaking credentials into the URL. `/_llm/chat` expects a JSON `POST` body so chat history, prompts, and token settings do not leak into URLs or access logs.

If the request body includes `"stream": true`, the same `POST /_llm/chat` route returns an OpenAI-compatible `text/event-stream` response for incremental token delivery.

The LLM demo uses this to auto-load available models for the currently selected provider or custom endpoint and then exposes them directly in the model picker.

And for Conway's Game of Life:

```json
{
  "/life": {
    "wasm_file": "instruments/life.wasm",
    "native_routes": [
      {
        "path": "/_life/stream",
        "transport": "sse",
        "adapter": "wasm-sse",
        "methods": ["GET"]
      }
    ]
  }
}
```

This is the bridge toward Cloudflare/wasmCloud-style host capabilities: keep the guest small and pure, move privileged integration to the host, and describe the host behavior declaratively.

## Hot Reload

WASIO automatically recompiles a module when its `.wasm` file changes.

Manual reload endpoints are also available:

```bash
curl http://localhost:8080/_reload
curl http://localhost:8080/_reload?route=/calculator
curl http://localhost:8080/_reload?all=1
./wasio reload
./wasio reload /calculator
```

This is one of the biggest developer-experience upgrades compared to the original version.

## Logging And Observability

WASIO now supports structured JSON access logs and request correlation.

Example log line:

```json
{"bytes":73009,"duration_ms":0,"method":"GET","path":"/","remote":"::1","request_id":"6b16ea56a896fa53","status":200,"time":"2026-04-10T18:44:45.816192Z","user_agent":"Safari/..."}
```

Current observability features:

- JSON or Apache combined access logs
- optional `X-Request-ID` generation
- built-in `/monitoring` dashboard
- `/monitoring?format=json` machine-readable stats

## Configuration

Example `config.json`:

```json
{
  "port": "8080",
  "cache_ttl": 300,
  "cache_size": 1024,
  "index_page": true,
  "monitoring": true,
  "default_timeout_ms": 30000,
  "max_memory_pages": 0,
  "logging": {
    "format": "json",
    "request_id": true
  },
  "routes": {
    "/hello_world": {
      "wasm_file": "instruments/hello_world.wasm",
      "cache": false,
      "category": "Basic",
      "description": "Simple greeting service",
      "example": "?name=WASIO"
    },
    "/fibonacci": {
      "wasm_file": "instruments/fibonacci.wasm",
      "cache": true,
      "ttl": 600,
      "timeout_ms": 1000,
      "category": "Math"
    }
  }
}
```

### Global Config Fields

- `port`: listen port
- `cache_ttl`: default response cache TTL in seconds
- `cache_size`: shared cache capacity
- `index_page`: enable `/`
- `monitoring`: enable `/monitoring`
- `default_timeout_ms`: default WASM execution timeout in milliseconds
- `max_memory_pages`: global WASM linear-memory cap in 64 KiB pages
- `logging.format`: `json` or `combined`
- `logging.request_id`: inject `X-Request-ID` when missing

### Route Fields

- `wasm_file`: path to the module
- `cache`: enable response caching
- `ttl`: route-specific cache TTL
- `filesystem.mount` / `filesystem.path`: explicit directory mount
- `description`, `category`, `example`, `examples`, `use_case`: catalog metadata
- `config`: module-specific config object
- `methods`: allowed HTTP methods
- `env`: environment variables injected into the guest
- `timeout_ms`: per-route execution timeout override

## Included Instruments

WASIO includes a growing set of example instruments across several categories:

- Basic: `hello_world`, `random`, `hello_rust`
- Math: `calculator`, `fibonacci`, `stats_utils`
- Utils: `text_utils`, `time_utils`, `url_utils`, `json_utils`, `uuid`
- Security: `hash_utils`
- Network: `ip_utils`, `proxy`
- Graphics: `mandelbrot`, `life`
- File I/O: `process_file`
- Web: `profile`, `wiki`, `chat`
- AI: `llm`

The most important meta-point is that they are no longer all Go-only examples. `/hello_rust` proves the platform can host multiple toolchains under the same runtime model.

## Product Roadmap: From Good Tool To Platform

If the goal is “Docker + Cloudflare + wasmCloud + Spin + Caddy for WASM,” these are the remaining big pieces.

### 1. Packaging And Registry

This is the Docker/Spin part.

Needed:

- a `wasio.toml` or `instrument.toml` manifest format
- signed downloadable instrument bundles
- a public registry with install metadata, examples, and compatibility info
- `wasio pull`, `wasio push`, `wasio search`
- versioned dependencies and reproducible builds

Without this, WASIO is still a runtime, not a distribution ecosystem.

### 2. Better App Model

This is the next step beyond a flat route map.

Needed:

- multi-route applications
- named services/components
- shared secrets/config references
- route groups / prefixes / middleware chains
- importable app bundles instead of manual JSON editing

This is the bridge from single tools to real apps.

### 3. Host Capabilities

This is the wasmCloud/Cloudflare side.

Needed:

- first-class outbound HTTP capability
- KV / object store / queue / pub-sub host bridges
- cron / scheduled jobs
- streaming request and response support
- WebSocket / SSE support
- capability permission model per route/app

The native handler registry is the current foundation for this.

### 4. Edge And Deployment Story

This is the Cloudflare/Caddy operator story.

Needed:

- TLS and automatic certificate management
- reverse-proxy and upstream routing features
- config reload without process restart
- static asset serving
- edge caching rules and CDN integration
- optional deploy targets for edge runtimes

Right now WASIO is excellent for self-hosted local or internal deployments, but not yet an edge platform.

### 5. Multi-Node Control Plane

This is the Kubernetes/wasmCloud part.

Needed:

- multiple WASIO instances managed as a fleet
- route placement and rollout logic
- remote module distribution
- service discovery
- health checks and auto-recovery
- leader election or external control plane

This should come later. Doing this too early would overcomplicate the project.

### 6. Security And Production Hardening

Needed:

- authn/authz for admin endpoints
- signed module verification
- secrets management instead of plain env in config
- quotas / concurrency limits / rate limiting
- audit logging
- better failure isolation and clearer runtime error reporting

### 7. Developer Experience

This is where Caddy and Docker both win.

Needed:

- great docs and tutorials
- copy-paste examples that work immediately
- SDK examples for Go, Rust, TinyGo, Zig, AssemblyScript
- better `wasio add` and validation workflows
- one-command project scaffolding for new instruments
- local test harness for request payloads

## Recommended Next Priorities

If the goal is traction, these are the best next moves in order:

1. Add a manifest format and a simple remote registry.
2. Introduce signed capability declarations for native host bridges.
3. Implement TLS / reverse-proxy features and a cleaner deployment story.
4. Add KV, queue, and outbound HTTP as first-class host capabilities.
5. Ship multi-language starter templates and a `wasio init` scaffold command.

Those five items do more for adoption than jumping directly to cluster orchestration.

## Design Principles

WASIO should stay opinionated about a few things:

- explicit capabilities
- one-binary deployment
- readable config
- fast local iteration
- boring operations

If WASIO keeps those properties while adding registry, capabilities, and deployment polish, it can become genuinely useful and memorable.

## Development Commands

```bash
make build
make instruments
make all-with-rust
make run
make test
make clean

./wasio help
./wasio list
./wasio validate
./wasio reload /hello_rust
```

## License And Status

WASIO is currently best understood as a serious experimental platform: useful today, not yet a finished platform product.

That is fine. Docker did not win because it launched as the final form. It won because the workflow was obvious and the operator experience was better than the alternatives. WASIO should optimize for the same outcome.
