# WASIO - WebAssembly System Interface Orchestrator

[![DOI](https://zenodo.org/badge/884922110.svg)](https://doi.org/10.5281/zenodo.15257309)  

WASIO (WebAssembly System Interface Orchestrator) is a Go-based server for dynamically loading, executing, and managing WebAssembly (WASM) instruments in response to HTTP requests. WASIO is designed to handle structured data transfer, caching, and controlled file system access, making it ideal for applications that need isolated, efficient, and secure compute environments.  

But it is currently not production ready.  

## Features

- **Dynamic Routing**: Map HTTP endpoints to specific WASM instruments through a configuration file.
- **Advanced IPC**: Structured JSON-based data transfer allows complex parameter handling and efficient interaction with WASM modules.
- **File System Access**: Configurable, controlled access to server directories, enabling instruments to read/write files.
- **Caching**: Built-in response caching with configurable TTLs for optimized performance.
- **Flexible Configuration**: Define port, routes, caching options, and file system mounts in a single JSON file.

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

1. **Hello World**:
   ```bash
   curl "http://localhost:8080/hello_world?name=Alice"
   ```

2. **File Processor**:
   ```bash
   curl "http://localhost:8080/file_processor"
   ```

## Contributing

Contributions are welcome! Please fork the repository, create a branch, and submit a pull request for any improvements or bug fixes.

## License

WASIO is open-source and available under the MIT License.
