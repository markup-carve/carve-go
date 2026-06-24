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
// lifetime of the process. Each render call instantiates a fresh module from
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

// Options configures a render call.
//
// The zero value (Options{}) is the interactive default and matches ToHTML:
// live HTML, no bundled extensions enabled. Set Static to true to flatten
// interactive constructs and degrade diagrams/math to their source form (see
// ToHTMLStatic for the full behavior and its limitation).
type Options struct {
	// Static selects self-contained HTML: interactive constructs are
	// flattened (details rendered open, spoilers revealed) and diagram/math
	// fences degrade to their source as a <pre><code> block. It maps to the
	// engine CLI flag --static.
	//
	// Static implies the bundled extensions (--extensions), since those are
	// what produce the constructs static mode flattens; you do not also need
	// to populate Extensions for the static behavior to apply.
	//
	// Build-time renderer injection (turning mermaid/math into an image or
	// SSR markup) is NOT available in carve-go: that path needs host closures
	// passed into the engine, which cannot cross the WASI/CLI stdio boundary.
	// Static mode in carve-go is therefore flatten + source fallback only.
	Static bool

	// Extensions, when non-empty, enables the bundled interactive extensions
	// in the engine (it maps to the CLI flag --extensions). The carve-rs
	// engine exposes a single on/off switch rather than a per-extension list,
	// so ANY non-empty slice enables the full bundle (details, spoiler,
	// mermaid, chart, math). Enabling them is required for Static to have the
	// interactive constructs to flatten or degrade.
	//
	// It is modeled as a slice so the API can grow into per-extension
	// selection if the engine gains it, without a breaking change. The
	// element strings are advisory today.
	Extensions []string
}

// ToHTML renders Carve source to HTML using the embedded engine.
//
// It is safe to call concurrently from multiple goroutines: the wasm module is
// compiled once and a fresh instance is created per call with isolated stdio.
//
// This is the interactive default: live HTML with no bundled extensions
// enabled. For self-contained static HTML, see ToHTMLStatic or ToHTMLOptions.
func ToHTML(source string) (string, error) {
	return ToHTMLContext(context.Background(), source)
}

// ToHTMLContext is ToHTML with a caller-supplied context. The context bounds
// both wasm compilation (first call) and the per-call module execution.
func ToHTMLContext(ctx context.Context, source string) (string, error) {
	return ToHTMLOptionsContext(ctx, source, Options{})
}

// ToHTMLStatic renders Carve source to self-contained static HTML.
//
// Compared with ToHTML it flattens interactive constructs (details become
// <details open>, spoilers are revealed) and degrades diagram/math fences
// (mermaid, chart, graphviz, math) to their source as a <pre><code> block.
// It enables the bundled extensions automatically so those constructs exist
// to be flattened.
//
// Limitation: build-time renderer injection (mermaid/math -> image or SSR
// markup) is NOT supported in carve-go, because it would require host closures
// to cross the WASI/CLI stdio boundary. carve-go static mode is flatten +
// source fallback only. For image rendering, pre-render the diagrams or use
// one of the in-process engines (carve-js, carve-php, carve-py, carve-rb).
//
// It is safe to call concurrently from multiple goroutines.
func ToHTMLStatic(source string) (string, error) {
	return ToHTMLOptionsContext(context.Background(), source, Options{Static: true})
}

// ToHTMLOptions renders Carve source to HTML with the given options.
//
// The zero Options value is equivalent to ToHTML (interactive, no extensions).
// Set Options.Static for the static flatten/source behavior described on
// ToHTMLStatic. Safe to call concurrently from multiple goroutines.
func ToHTMLOptions(source string, opts Options) (string, error) {
	return ToHTMLOptionsContext(context.Background(), source, opts)
}

// ToHTMLOptionsContext is ToHTMLOptions with a caller-supplied context that
// bounds wasm compilation (first call) and the per-call module execution.
func ToHTMLOptionsContext(ctx context.Context, source string, opts Options) (string, error) {
	eng, err := loadEngine(ctx)
	if err != nil {
		return "", err
	}

	// argv[0] is the program name; the engine reads source from stdin when no
	// file argument is given. --html is the default but is passed explicitly
	// so the contract is self-documenting.
	args := []string{"carve", "--html"}
	if opts.Static {
		args = append(args, "--static")
	}
	// Static mode exists to flatten/degrade the interactive constructs, which
	// only the bundled extensions produce, so Static implies --extensions even
	// when the caller did not list any. (The engine exposes a single on/off
	// switch, not a per-extension selector.)
	if opts.Static || len(opts.Extensions) > 0 {
		args = append(args, "--extensions")
	}

	stdin := strings.NewReader(source)
	var stdout, stderr bytes.Buffer

	config := wazero.NewModuleConfig().
		WithStdin(stdin).
		WithStdout(&stdout).
		WithStderr(&stderr).
		WithArgs(args...).
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
