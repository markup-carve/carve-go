// Package carve runs the Carve 0.2 Rust engine through its WASI JSON call boundary.
// It is separate from the root package, which retains the Carve 0.1 contract.
package carve

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

//go:embed internal/wasm/carve.wasm
var wasm []byte

const maxMemoryPages = 8192

var compiled struct {
	once    sync.Once
	runtime wazero.Runtime
	module  wazero.CompiledModule
	err     error
}

func engine() (wazero.Runtime, wazero.CompiledModule, error) {
	compiled.once.Do(func() {
		ctx := context.Background()
		cfg := wazero.NewRuntimeConfig().WithCloseOnContextDone(true).WithMemoryLimitPages(maxMemoryPages)
		compiled.runtime = wazero.NewRuntimeWithConfig(ctx, cfg)
		if _, err := wasi_snapshot_preview1.Instantiate(ctx, compiled.runtime); err != nil {
			compiled.err = fmt.Errorf("carve: initialize WASI: %w", err)
			return
		}
		compiled.module, compiled.err = compiled.runtime.CompileModule(ctx, wasm)
		if compiled.err != nil {
			compiled.err = fmt.Errorf("carve: compile WASI engine: %w", compiled.err)
		}
	})
	return compiled.runtime, compiled.module, compiled.err
}

// EngineError is an error returned by the 0.2 JSON boundary.
type EngineError struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Status   int    `json:"status"`
	Message  string `json:"message"`
}

func (e *EngineError) Error() string { return "carve: " + e.Message }

// Call invokes a 0.2 tooling call. Request and result use the JSON names in
// the public 0.2 tooling contract. Each call has a fresh WASI instance.
func Call(ctx context.Context, request any, result any) error {
	return callFS(ctx, request, result, nil)
}

// runCLI invokes a command of the same embedded engine used by Call.
func runCLI(ctx context.Context, args []string, source string) ([]byte, []byte, int, error) {
	if ctx == nil {
		return nil, nil, 0, errors.New("carve: nil context")
	}
	rt, mod, err := engine()
	if err != nil {
		return nil, nil, 0, err
	}
	var stdout, stderr bytes.Buffer
	argv := append([]string{"carve"}, args...)
	cfg := wazero.NewModuleConfig().WithName("").WithArgs(argv...).
		WithStdin(strings.NewReader(source)).WithStdout(&stdout).WithStderr(&stderr)
	instance, runErr := rt.InstantiateModule(ctx, mod, cfg)
	if instance != nil {
		_ = instance.Close(ctx)
	}
	if ctx.Err() != nil {
		return nil, nil, 0, fmt.Errorf("carve: call interrupted: %w", ctx.Err())
	}
	if runErr != nil {
		if exit, ok := runErr.(*sys.ExitError); ok {
			return stdout.Bytes(), stderr.Bytes(), int(exit.ExitCode()), nil
		}
		return nil, nil, 0, fmt.Errorf("carve: WASI command: %w", runErr)
	}
	return stdout.Bytes(), stderr.Bytes(), 0, nil
}

func callFS(ctx context.Context, request any, result any, mount fs.FS) error {
	if ctx == nil {
		return errors.New("carve: nil context")
	}
	data, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("carve: encode request: %w", err)
	}
	rt, mod, err := engine()
	if err != nil {
		return err
	}
	var stdout, stderr bytes.Buffer
	cfg := wazero.NewModuleConfig().WithName("").WithArgs("carve", "call").
		WithStdin(bytes.NewReader(data)).WithStdout(&stdout).WithStderr(&stderr)
	if mount != nil {
		cfg = cfg.WithFSConfig(wazero.NewFSConfig().WithFSMount(mount, "/carve-root"))
	}
	instance, runErr := rt.InstantiateModule(ctx, mod, cfg)
	if instance != nil {
		_ = instance.Close(ctx)
	}
	if ctx.Err() != nil {
		return fmt.Errorf("carve: call interrupted: %w", ctx.Err())
	}
	if runErr != nil {
		if _, ok := runErr.(*sys.ExitError); !ok {
			return fmt.Errorf("carve: WASI call: %w", runErr)
		}
	}
	var response struct {
		OK    json.RawMessage `json:"ok"`
		Error *EngineError    `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return fmt.Errorf("carve: invalid response: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	if response.Error != nil {
		return response.Error
	}
	if len(response.OK) == 0 {
		return errors.New("carve: response has no result")
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(response.OK, result); err != nil {
		return fmt.Errorf("carve: decode result: %w", err)
	}
	return nil
}
