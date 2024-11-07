package main

import (
    "context"
    "fmt"
    "io"
    "os"
    "sync"

    "github.com/tetratelabs/wazero"
    "github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

type ModuleCache struct {
    cache map[string]wazero.CompiledModule // Cache compiled modules instead of instantiated ones
    mu    sync.RWMutex
    rt    wazero.Runtime
}

func NewModuleCache() *ModuleCache {
    ctx := context.Background()
    rt := wazero.NewRuntime(ctx)

    // Instantiate the WASI module, which provides the `wasi_snapshot_preview1` functionality
    wasi_snapshot_preview1.MustInstantiate(ctx, rt)

    return &ModuleCache{
        cache: make(map[string]wazero.CompiledModule),
        rt:    rt,
    }
}

// GetCompiledModule returns a cached compiled module or loads it if not present
func (mc *ModuleCache) GetCompiledModule(wasmFile string) (wazero.CompiledModule, error) {
    mc.mu.RLock()
    compiledModule, found := mc.cache[wasmFile]
    mc.mu.RUnlock()
    if found {
        return compiledModule, nil
    }

    // Load and compile the module
    wasmBytes, err := os.ReadFile(wasmFile)
    if err != nil {
        return nil, fmt.Errorf("failed to read WASM file: %v", err)
    }

    compiledModule, err = mc.rt.CompileModule(context.Background(), wasmBytes)
    if err != nil {
        return nil, fmt.Errorf("failed to compile module: %v", err)
    }

    // Cache the compiled module
    mc.mu.Lock()
    mc.cache[wasmFile] = compiledModule
    mc.mu.Unlock()

    return compiledModule, nil
}

// InstantiateAndRunModuleWithArgs creates a new instance, runs it, and passes arguments to capture output
func (mc *ModuleCache) InstantiateAndRunModuleWithArgs(wasmFile string, output io.Writer, args []string) error {
    compiledModule, err := mc.GetCompiledModule(wasmFile)
    if err != nil {
        return err
    }
    
    // Create a new module configuration with WASI, stdout, and args
    moduleConfig := wazero.NewModuleConfig().WithStdout(output).WithArgs(args...)
    
    // Instantiate the module with the configuration
    module, err := mc.rt.InstantiateModule(context.Background(), compiledModule, moduleConfig)
    if err != nil {
        return fmt.Errorf("failed to instantiate module: %v", err)
    }
    defer module.Close(context.Background())
    
    // Run the WASM module's `_start` function
    if mainFunc := module.ExportedFunction("_start"); mainFunc != nil {
        _, err := mainFunc.Call(context.Background())
        if err != nil {
            return fmt.Errorf("error running module: %v", err)
        }
    } else {
        return fmt.Errorf("no _start function found in module")
    }
    
    return nil
}

// Close releases all cached compiled modules and the runtime
func (mc *ModuleCache) Close() {
    mc.mu.Lock()
    defer mc.mu.Unlock()
    for _, compiledModule := range mc.cache {
        compiledModule.Close(context.Background())
    }
    mc.rt.Close(context.Background())
}
