// Package carve renders Carve markup to HTML.
//
// It embeds a WASI (wasm32-wasip1) build of the reference Carve engine
// (carve-rs) and runs it with the pure-Go wazero runtime. There is no cgo
// dependency and no JavaScript host required: the engine is driven over the
// WASI stdio contract (Carve source on stdin, HTML on stdout).
package carve

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"strings"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

//go:embed internal/wasm/carve.wasm
var carveWasm []byte

// compiled holds the once-compiled module and its runtime. Compilation is
// relatively expensive, so it is done lazily on first use and reused for the
// lifetime of the process. Each ToHTML call instantiates a fresh module from
// this compiled artifact, which keeps per-call state isolated and concurrent
// calls safe.
type compiledEngine struct {
	runtime  wazero.Runtime
	module   wazero.CompiledModule
	compiled bool
	err      error
}

var (
	engine     compiledEngine
	engineOnce sync.Once
)

func loadEngine(ctx context.Context) (*compiledEngine, error) {
	engineOnce.Do(func() {
		rt := wazero.NewRuntime(ctx)
		// WASI host functions satisfy the engine's __wasi_* imports
		// (fd_read for stdin, fd_write for stdout, proc_exit, etc.).
		wasi_snapshot_preview1.MustInstantiate(ctx, rt)

		mod, err := rt.CompileModule(ctx, carveWasm)
		if err != nil {
			engine.err = fmt.Errorf("carve: compile wasm: %w", err)
			_ = rt.Close(ctx)
			return
		}
		engine.runtime = rt
		engine.module = mod
		engine.compiled = true
	})
	if engine.err != nil {
		return nil, engine.err
	}
	return &engine, nil
}

// ToHTML renders Carve source to HTML using the embedded engine.
//
// It is safe to call concurrently from multiple goroutines: the wasm module is
// compiled once and a fresh instance is created per call with isolated stdio.
func ToHTML(source string) (string, error) {
	return ToHTMLContext(context.Background(), source)
}

// ToHTMLContext is ToHTML with a caller-supplied context. The context bounds
// both wasm compilation (first call) and the per-call module execution.
func ToHTMLContext(ctx context.Context, source string) (string, error) {
	eng, err := loadEngine(ctx)
	if err != nil {
		return "", err
	}

	stdin := strings.NewReader(source)
	var stdout, stderr bytes.Buffer

	config := wazero.NewModuleConfig().
		WithStdin(stdin).
		WithStdout(&stdout).
		WithStderr(&stderr).
		// Anonymous module name avoids "module already instantiated"
		// collisions when called concurrently.
		WithName("")

	mod, err := eng.runtime.InstantiateModule(ctx, eng.module, config)
	if err != nil {
		// A clean exit code 0 surfaces as *sys.ExitError, not a failure.
		if exitErr, ok := err.(*sys.ExitError); ok {
			if exitErr.ExitCode() == 0 {
				return stdout.String(), nil
			}
			return "", fmt.Errorf("carve: engine exited with code %d: %s",
				exitErr.ExitCode(), strings.TrimSpace(stderr.String()))
		}
		return "", fmt.Errorf("carve: run wasm: %w", err)
	}
	// Module returned without calling proc_exit; close it and return output.
	_ = mod.Close(ctx)
	return stdout.String(), nil
}
