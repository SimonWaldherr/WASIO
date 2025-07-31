# WASIO - WebAssembly System Interface Orchestrator

[![DOI](https://zenodo.org/badge/884922110.svg)](https://doi.org/10.5281/zenodo.15257309)  

WASIO (WebAssembly System Interface Orchestrator) is a comprehensive Go-based server for dynamically loading, executing, and managing WebAssembly (WASM) instruments in response to HTTP requests. WASIO demonstrates the power of WebAssembly for creating isolated, efficient, and secure compute environments through a rich collection of example applications.

The project serves as both a production-ready server framework and an educational platform showcasing various WebAssembly use cases, from simple calculations to complex applications like wikis and chat systems.

**Note: While functional, this project is currently not production ready and is intended for demonstration and educational purposes.**  

## Features

- **Dynamic Routing**: Map HTTP endpoints to specific WASM instruments through a configuration file.
- **Advanced IPC**: Structured JSON-based data transfer allows complex parameter handling and efficient interaction with WASM modules.
- **File System Access**: Configurable, controlled access to server directories, enabling instruments to read/write files.
- **Caching**: Built-in response caching with configurable TTLs for optimized performance.
- **Flexible Configuration**: Define port, routes, caching options, and file system mounts in a single JSON file.

## Included Instruments (Examples)

WASIO comes with a comprehensive collection of example instruments that demonstrate various WebAssembly capabilities and use cases:

### 🌍 Basic Examples

#### Hello World (`/hello_world`)

A simple greeting service that demonstrates basic parameter handling and JSON communication.

- **Input**: `name` parameter (optional, defaults to "World")
- **Output**: Personalized greeting message
- **Example**: `curl "http://localhost:8080/hello_world?name=Alice"`
- **Use Case**: Basic WASM interaction, parameter passing demonstration

#### Random Number Generator (`/random`)

Generates random numbers using a seed-based approach for reproducible randomness.

- **Input**: Automatic seed generation from WASIO
- **Output**: Random number between 0-99
- **Example**: `curl "http://localhost:8080/random"`
- **Use Case**: Demonstrates deterministic random generation in WASM environments

#### Fibonacci Calculator (`/fibonacci`)

Computes Fibonacci numbers with caching enabled for performance optimization.

- **Input**: `n` parameter (non-negative integer)
- **Output**: nth Fibonacci number
- **Features**: Response caching (TTL: 600 seconds)
- **Example**: `curl "http://localhost:8080/fibonacci?n=10"`
- **Use Case**: CPU-intensive calculations, caching demonstration

### 🧮 Mathematical and Utility Tools

#### Calculator (`/calculator`)

A comprehensive calculator supporting multiple mathematical operations.

- **Input**: `op` (operation), `a` and `b` (numbers)
- **Operations**: add, sub, mul, div, pow, mod
- **Example**: `curl "http://localhost:8080/calculator?op=add&a=5&b=3"`
- **Features**: Error handling, multiple operation types
- **Use Case**: Mathematical computations, input validation

#### Time Utilities (`/time_utils`)

Advanced time and date manipulation with timezone support.

- **Operations**: 
  - `unix`: Get Unix timestamp
  - `iso`: ISO 8601 format
  - `add`: Add duration to current time
  - `diff`: Calculate time difference
  - `weekday`, `year`, `month`, `day`: Extract components
- **Parameters**: `tz` (timezone), `format` (output format), `duration`, `target`
- **Example**: `curl "http://localhost:8080/time_utils?op=add&duration=1h&tz=UTC"`
- **Use Case**: Time calculations, timezone handling

#### Text Utilities (`/text_utils`)

Comprehensive text processing and analysis tools.

- **Operations**: upper, lower, title, reverse, length, words, chars, trim, split, contains, replace, palindrome
- **Parameters**: `text` (input), `op` (operation), additional params per operation
- **Example**: `curl "http://localhost:8080/text_utils?op=reverse&text=hello"`
- **Features**: Character counting, word analysis, pattern matching
- **Use Case**: Text processing, string manipulation

#### URL Utilities (`/url_utils`)

URL encoding, decoding, parsing, and validation utilities.

- **Operations**: encode, decode, parse, validate, join
- **Parameters**: `input` (URL/text), `op` (operation), `base` (for join)
- **Example**: `curl "http://localhost:8080/url_utils?op=parse&input=https://example.com/path?key=value"`
- **Features**: Component extraction, query parameter parsing
- **Use Case**: URL manipulation, web development

#### Hash and Encoding Utilities (`/hash_utils`)

Cryptographic hashing and encoding/decoding utilities.

- **Operations**: md5, sha1, sha256, sha512, base64encode, base64decode, hexencode, hexdecode, all
- **Parameters**: `input` (text to process), `op` (operation)
- **Example**: `curl "http://localhost:8080/hash_utils?op=sha256&input=hello"`
- **Features**: Multiple hash algorithms, encoding formats
- **Use Case**: Security, data integrity, encoding conversion

### 🎨 Graphics and Visualization

#### Mandelbrot Set Generator (`/mandelbrot`)

Generates PNG images of the Mandelbrot fractal with customizable parameters.

- **Input**: `cx`, `cy` (center coordinates), `zoom`, `width`, `height`, `max_iter`
- **Output**: PNG image data
- **Features**: Real-time fractal rendering, parameter-driven visualization
- **Example**: `curl "http://localhost:8080/mandelbrot?cx=-0.5&cy=0&zoom=1&width=800&height=600" > mandelbrot.png`
- **Use Case**: Image generation, mathematical visualization, binary data handling

### 📁 File System Operations

#### File Processor (`/process_file`)

Demonstrates controlled file system access within WASM environments.

- **Input**: Reads from mounted `/data` directory
- **Output**: File analysis (line count and content)
- **Features**: Secure file system mounting, read-only access
- **Example**: `curl "http://localhost:8080/process_file"`
- **Use Case**: File processing, secure file system access patterns

### 🌐 Web Applications

#### Profile Generator (`/profile`)

Creates dynamic HTML profiles using template rendering.

- **Input**: `name`, `age`, `hobbies` (comma-separated) parameters
- **Output**: Rendered HTML profile page
- **Features**: Template processing, dynamic HTML generation
- **Example**: `curl "http://localhost:8080/profile?name=John&age=25&hobbies=reading,coding,hiking"`
- **Use Case**: Dynamic web content generation, template processing

#### Mini Wiki (`/wiki`)

A complete wiki system with full CRUD operations, search, and tagging.

- **Features**:
  - Page creation, editing, and deletion
  - Full-text search functionality
  - Tag-based organization
  - Backlink tracking
  - Dark/light theme support
  - Bootstrap-based responsive UI
- **Input**: Various parameters (`page`, `edit`, `search`, `tag`, `content`, etc.)
- **Output**: Complete HTML wiki interface or JSON data
- **Example**: `curl "http://localhost:8080/wiki"` or visit in browser
- **Use Case**: Complex web applications, content management, file persistence

#### Real-time Chat (`/chat`)

A functional chat application with persistent message storage.

- **Features**:
  - Real-time messaging interface
  - Message persistence to JSON files
  - Bootstrap-based responsive UI
  - Automatic message refresh
  - Username handling
- **Actions**:
  - `action=send`: Post new message
  - `action=get`: Retrieve message history
  - `action=ui` or default: Serve chat interface
- **Example**: Visit `http://localhost:8080/chat` in browser
- **Use Case**: Real-time applications, persistent data storage, interactive UIs

### 🧠 Advanced Examples

#### LLaMA Chat Integration (`/llama-chat`)

Integration endpoint for LLaMA-based AI chat functionality (requires additional setup).

- **Input**: Chat messages and conversation context
- **Output**: AI-generated responses
- **Features**: AI model integration, conversation handling
- **Use Case**: AI integration, natural language processing

Each instrument demonstrates different aspects of WebAssembly capabilities:

- **Isolation**: Each WASM module runs in its own isolated environment
- **Performance**: Near-native execution speed for computational tasks
- **Security**: Controlled access to system resources through explicit mounts
- **Portability**: Same WASM modules can run across different platforms
- **Language Flexibility**: All examples written in Go but could be any WASM-compatible language

## Built-in Features

### 🏠 Index Page (`/`)

WASIO now includes a beautiful, responsive index page that provides:

- **Instrument Discovery**: Visual overview of all available instruments
- **Quick Statistics**: Server metrics and performance indicators
- **Interactive Testing**: Direct links to test each instrument
- **Categorization**: Instruments grouped by type (Basic, Math, Graphics, etc.)
- **URL Copying**: Easy copying of instrument URLs for testing

The index page is enabled by default but can be disabled via configuration.

### 📊 Monitoring Dashboard (`/monitoring`)

Comprehensive monitoring and statistics dashboard featuring:

- **Real-time Metrics**: Request counts, success rates, response times
- **Cache Statistics**: Hit rates for both response and module caches
- **Route Analytics**: Per-route request statistics and configuration
- **System Information**: Server uptime, memory usage, performance data
- **Auto-refresh**: Automatic page refresh every 30 seconds
- **JSON API**: Machine-readable stats at `/monitoring?format=json`

The monitoring dashboard is enabled by default and provides both human-readable HTML and JSON formats.

## Getting Started

### Prerequisites

- **Go**: Version 1.20 or higher.
- **TinyGo**: Required to compile Go-based WASM instruments.
- **Wazero**: WASM runtime integrated into WASIO for executing instruments.

### Quick Setup

1. **Clone the Repository**:
   ```bash
   git clone https://github.com/SimonWaldherr/WASIO.git
   cd WASIO
   ```

2. **Build the Instruments**:
   Compile your WASM instruments with TinyGo, or use the included `build.sh` script to compile all `.go` files in the `instruments` folder.

   ```bash
   ./build.sh
   ```

3. **Configure WASIO**:
   Edit `config.json` to define routes, cache settings, and any filesystem mounts needed by the instruments.

   Example `config.json`:
   ```json
   {
     "port": "8080",
     "cache_ttl": 300,
     "routes": {
       "/hello_world": {
         "wasm_file": "instruments/hello_world.wasm",
         "cache": true,
         "ttl": 600
       },
       "/file_processor": {
         "wasm_file": "instruments/file_processor.wasm",
         "cache": false,
         "filesystem": {
           "mount": "/data",
           "path": "./data"
         }
       }
     }
   }
   ```

4. **Run WASIO**:
   ```bash
   go run main.go
   ```

   WASIO will start and listen for HTTP requests on the configured port.

### Example Requests

Once WASIO is running, you can test the various instruments:

#### Basic Examples

1. **Hello World**:

   ```bash
   curl "http://localhost:8080/hello_world?name=Alice"
   # Output: Hello, Alice! (seed: 1234567890)
   ```

2. **Random Number**:

   ```bash
   curl "http://localhost:8080/random"
   # Output: Generated Random Number: 42
   ```

3. **Fibonacci Calculation**:

   ```bash
   curl "http://localhost:8080/fibonacci?n=10"
   # Output: Fibonacci number for n=10 is 55
   ```

#### Mathematical and Utility Tools

4. **Calculator**:

   ```bash
   curl "http://localhost:8080/calculator?op=add&a=15&b=25"
   # Output: 15.00 + 25.00 = 40.00
   
   curl "http://localhost:8080/calculator?op=pow&a=2&b=8"
   # Output: 2.00 ^ 8 = 256.00
   ```

5. **Time Utilities**:

   ```bash
   curl "http://localhost:8080/time_utils?op=add&duration=2h30m"
   # Output: Time + 2h30m = 2024-07-31T17:30:00Z
   
   curl "http://localhost:8080/time_utils?tz=America/New_York&format=kitchen"
   # Output: Current time: 3:04PM (timezone: America/New_York)
   ```

6. **Text Processing**:

   ```bash
   curl "http://localhost:8080/text_utils?op=reverse&text=hello%20world"
   # Output: Reversed: dlrow olleh
   
   curl "http://localhost:8080/text_utils?op=palindrome&text=racecar"
   # Output: Is palindrome: true
   ```

7. **URL Utilities**:

   ```bash
   curl "http://localhost:8080/url_utils?op=parse&input=https://example.com/path?key=value"
   # Output: URL components breakdown
   
   curl "http://localhost:8080/url_utils?op=encode&input=hello world"
   # Output: URL encoded: hello%20world
   ```

8. **Hash Utilities**:

   ```bash
   curl "http://localhost:8080/hash_utils?op=sha256&input=hello"
   # Output: SHA256: 2cf24dba4f21d4288cff...
   
   curl "http://localhost:8080/hash_utils?op=all&input=test"
   # Output: All hash formats for 'test'
   ```

#### Graphics and Data

9. **Mandelbrot Fractal** (save as PNG):

   ```bash
   curl "http://localhost:8080/mandelbrot?cx=-0.5&cy=0&zoom=1&width=800&height=600" > mandelbrot.png
   ```

10. **File Processing**:

    ```bash
    curl "http://localhost:8080/process_file"
    # Output: File has 3 lines. Content: [file content]
    ```

#### Web Applications

11. **Profile Generation**:

    ```bash
    curl "http://localhost:8080/profile?name=John&age=25&hobbies=reading,coding,hiking"
    # Returns: Complete HTML profile page
    ```

12. **Wiki System** (best viewed in browser):

    ```bash
    # Get the main wiki interface
    curl "http://localhost:8080/wiki"
    
    # Search for content
    curl "http://localhost:8080/wiki?search=welcome"
    
    # Get a specific page
    curl "http://localhost:8080/wiki?page=about"
    ```

13. **Real-time Chat** (interactive, best in browser):

    ```bash
    # Get chat interface
    curl "http://localhost:8080/chat"
    
    # Send a message (URL encoded)
    curl "http://localhost:8080/chat?action=send&username=Alice&text=Hello%20World"
    
    # Get recent messages as JSON
    curl "http://localhost:8080/chat?action=get&n=10"
    ```

#### Built-in Endpoints

14. **Index Page** (best in browser):

    ```bash
    curl "http://localhost:8080/"
    # Returns: Beautiful index page with all instruments
    ```

15. **Monitoring Dashboard**:

    ```bash
    # HTML dashboard
    curl "http://localhost:8080/monitoring"
    
    # JSON statistics
    curl "http://localhost:8080/monitoring?format=json"
    ```

#### Advanced Testing

For the web-based instruments and dashboards, open your browser and navigate to:

- `http://localhost:8080/` - Main index page with instrument overview
- `http://localhost:8080/monitoring` - Comprehensive monitoring dashboard
- `http://localhost:8080/wiki` - Full-featured wiki with editing capabilities
- `http://localhost:8080/chat` - Real-time chat interface
- `http://localhost:8080/profile?name=YourName&age=30&hobbies=music,travel` - Profile page

## Architecture and Technical Details

### How WASIO Works

WASIO operates as a reverse proxy that routes HTTP requests to WebAssembly modules (instruments). Here's the execution flow:

1. **Request Reception**: HTTP requests are received and matched against configured routes
2. **Module Loading**: The corresponding WASM module is loaded (with LRU caching for performance)
3. **Environment Setup**: A sandboxed WASI environment is created with controlled filesystem access
4. **Data Marshaling**: Request parameters are converted to JSON and passed via stdin
5. **Execution**: The WASM module executes with access only to explicitly mounted directories
6. **Response Collection**: Output is captured from stdout and returned as HTTP response
7. **Caching**: Responses can be cached based on route configuration

### Key Components

- **Wazero Runtime**: Production-ready WebAssembly runtime for Go
- **WASI Support**: Full WASI (WebAssembly System Interface) compatibility
- **TinyGo Compilation**: Instruments are compiled using TinyGo for optimal WASM output
- **JSON IPC**: Structured communication between host and WASM modules
- **Filesystem Isolation**: Controlled directory mounting for secure file access
- **LRU Caching**: Compiled modules are cached in memory for performance
- **Response Caching**: HTTP responses can be cached with configurable TTLs

### Security Model

WASIO implements a comprehensive security model:

- **Sandboxing**: Each WASM module runs in complete isolation
- **Controlled File Access**: Only explicitly mounted directories are accessible
- **No Network Access**: WASM modules cannot make outbound network connections
- **Resource Limits**: Memory and execution time can be controlled
- **Input Validation**: All data exchange happens through structured JSON

### Performance Characteristics

- **Fast Cold Starts**: WASM modules start in microseconds
- **Near-Native Speed**: WebAssembly provides excellent performance
- **Memory Efficiency**: Small memory footprint per module
- **Compilation Caching**: Compiled modules are cached for subsequent requests
- **Concurrent Execution**: Multiple modules can run simultaneously

### Configuration

All routing and behavior is controlled through `config.json`:

```json
{
  "port": "8080",
  "cache_ttl": 300,
  "cache_size": 1024,
  "index_page": true,
  "monitoring": true,
  "routes": {
    "/endpoint": {
      "wasm_file": "path/to/module.wasm",
      "cache": true,
      "ttl": 600,
      "filesystem": {
        "mount": "/virtual/path",
        "path": "./host/directory"
      }
    }
  }
}
```

#### Configuration Options

- **port**: HTTP server port (default: "8080")
- **cache_ttl**: Global response cache TTL in seconds (default: 300)
- **cache_size**: Maximum cache entries for both module and response caches (default: 1024)
- **index_page**: Enable/disable the index page at `/` (default: true)
- **monitoring**: Enable/disable the monitoring dashboard at `/monitoring` (default: true)
- **routes**: Map of URL paths to instrument configurations

#### Route Configuration

Each route supports the following options:

- **wasm_file**: Path to the compiled WebAssembly module
- **cache**: Enable response caching for this route (default: false)
- **ttl**: Cache TTL in seconds for this route (overrides global cache_ttl)
- **filesystem**: Optional filesystem mount configuration
  - **mount**: Virtual path inside the WASM environment
  - **path**: Host directory to mount

### Building Custom Instruments

Creating new instruments is straightforward:

1. Write a Go program that reads JSON from stdin
2. Process the data and write results to stdout
3. Compile with TinyGo: `tinygo build -target=wasi -o instrument.wasm main.go`
4. Add route configuration to `config.json`
5. Restart WASIO to load the new instrument

Example minimal instrument:

```go
package main

import (
    "encoding/json"
    "fmt"
    "os"
)

type Payload struct {
    Params map[string]string `json:"params"`
}

func main() {
    var payload Payload
    json.NewDecoder(os.Stdin).Decode(&payload)
    
    // Process payload.Params as needed
    fmt.Printf("Hello from custom instrument!")
}
```

## Use Cases and Applications

WASIO's architecture makes it suitable for various applications:

### 🔧 Microservices and APIs
- **Isolated Functions**: Each endpoint runs in complete isolation
- **Language Flexibility**: Write services in any WASM-compatible language
- **Fast Deployment**: Add new endpoints without server restarts
- **Resource Efficiency**: Minimal overhead per service

### 🧮 Computational Services
- **Mathematical Computations**: Like the Fibonacci and Mandelbrot examples
- **Data Processing**: File analysis, transformation, and validation
- **Image/Video Processing**: Graphics generation and manipulation
- **Scientific Computing**: Numerical analysis and simulations

### 🌐 Dynamic Web Applications
- **Content Management**: Wiki systems, blogs, documentation sites
- **User Interfaces**: Dynamic HTML generation with templates
- **Real-time Applications**: Chat systems, live dashboards
- **Form Processing**: Data collection and validation

### 🏢 Enterprise Applications
- **Secure Execution**: Run untrusted code safely
- **Multi-tenancy**: Isolated execution environments per tenant
- **Plugin Systems**: Extensible applications with user-provided code
- **Edge Computing**: Lightweight services for edge deployment

### 🎓 Educational and Research
- **Algorithm Visualization**: Interactive demonstrations
- **Programming Education**: Safe code execution environments
- **Research Prototyping**: Rapid development and testing
- **Benchmarking**: Performance comparison across implementations

## Contributing

Contributions are welcome! Please fork the repository, create a branch, and submit a pull request for any improvements or bug fixes.

## License

WASIO is open-source and available under the MIT License.
