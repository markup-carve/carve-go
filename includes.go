package carve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Include identifies a source under an absolute containment root.
type Include struct {
	Root       string
	SourcePath string
}

// Dependency records a target named by an include. Read is true when the
// resolver opened it, including when later processing refused its content.
type Dependency struct {
	Path     string `json:"path"`
	From     string `json:"from,omitempty"`
	File     string `json:"file,omitempty"`
	Read     bool   `json:"read"`
	Resolved bool   `json:"-"`
	HostPath string `json:"-"`
}

type IncludeDependency = Dependency
type IncludeWarning struct {
	Rule    string
	Message string
	File    string
}

// SelectIncludeRegion returns the 1-based inclusive content line range of a
// named region. Code mode also accepts source-comment region markers.
func SelectIncludeRegion(ctx context.Context, source, name string, code bool) (int, int, error) {
	args := []string{"region"}
	if code {
		args = append(args, "--code")
	}
	args = append(args, name)
	stdout, _, status, err := runCLI(ctx, args, source)
	if err != nil {
		return 0, 0, err
	}
	if status != 0 {
		return 0, 0, fmt.Errorf("carve: include-region-invalid: %q", name)
	}
	var result struct {
		Start int `json:"start"`
		End   int `json:"end"`
	}
	if err := json.Unmarshal(stdout, &result); err != nil {
		return 0, 0, fmt.Errorf("carve: invalid region response: %w", err)
	}
	return result.Start, result.End, nil
}

func IncludeDependencies(ctx context.Context, tree json.RawMessage, file string) ([]Dependency, error) {
	var result struct {
		Dependencies []Dependency `json:"dependencies"`
	}
	err := Call(ctx, map[string]any{"call": "includeDependencies", "tree": tree, "file": file}, &result)
	return result.Dependencies, err
}

type IncludeResult struct {
	Output                string
	Diagnostics           []Diagnostic
	SuppressedDiagnostics int
	Dependencies          []Dependency
	Warnings              []IncludeWarning
	SuppressedWarnings    int
}

func includeWarnings(entries []Diagnostic, root string) []IncludeWarning {
	warnings := []IncludeWarning{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Code, "include-") {
			continue
		}
		file := entry.File
		if file != "" {
			file = filepath.Join(root, filepath.FromSlash(file))
		}
		warnings = append(warnings, IncludeWarning{Rule: entry.Code, Message: entry.Message, File: file})
	}
	return warnings
}

func hostDependencies(entries []Dependency, root string) []Dependency {
	for i := range entries {
		entries[i].Resolved = entries[i].Read
		if entries[i].Read && entries[i].File != "" {
			entries[i].HostPath = filepath.Join(root, filepath.FromSlash(entries[i].File))
		}
	}
	return entries
}

// os.Root confines symlink traversal as well as lexical paths. A plain
// os.DirFS would let a symlink under the include root reach a host file outside.
type confinedFS struct{ root *os.Root }

func (f confinedFS) Open(name string) (fs.File, error) { return f.root.Open(name) }

func (inc Include) guestFile() (string, error) {
	if inc.Root == "" || inc.SourcePath == "" || !filepath.IsAbs(inc.Root) || !filepath.IsAbs(inc.SourcePath) {
		return "", errors.New("carve: Include.Root and Include.SourcePath must be absolute paths")
	}
	rel, err := filepath.Rel(inc.Root, inc.SourcePath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("carve: source path is outside the include root")
	}
	return filepath.ToSlash(rel), nil
}

// RenderWithIncludes expands source against the contained root, then writes
// the resolved tree. The 0.2 record supplies diagnostics and dependencies.
func RenderWithIncludes(source string, format OutputFormat, opts Options, inc Include) (IncludeResult, error) {
	return RenderWithIncludesContext(context.Background(), source, format, opts, inc)
}

func RenderWithIncludesContext(ctx context.Context, source string, format OutputFormat, opts Options, inc Include) (IncludeResult, error) {
	file, err := inc.guestFile()
	if err != nil {
		return IncludeResult{}, err
	}
	parseOpts, err := opts.data()
	if err != nil {
		return IncludeResult{}, err
	}
	for _, key := range []string{"mode", "profile", "rawHtml", "urlPolicy", "strictLosses", "roundtrip", "sourceLines", "symbols", "renderers"} {
		delete(parseOpts, key)
	}
	parseOpts["includes"] = map[string]any{"root": "/carve-root", "file": file}
	root, err := os.OpenRoot(inc.Root)
	if err != nil {
		return IncludeResult{}, fmt.Errorf("carve: open include root: %w", err)
	}
	defer root.Close()
	mount := confinedFS{root: root}
	var parsed ParseResult
	err = callFS(ctx, map[string]any{"call": "parseWithDiagnostics", "source": source, "options": parseOpts}, &parsed, mount)
	if err != nil {
		return IncludeResult{}, err
	}
	deps, err := IncludeDependencies(ctx, parsed.Tree, file)
	if err != nil {
		return IncludeResult{}, err
	}
	deps = hostDependencies(deps, inc.Root)
	renderOpts, err := opts.data()
	if err != nil {
		return IncludeResult{}, err
	}
	if format == "" || format == OutputHTML {
		// Rendered from source, not from parsed.Tree: a tree render drops the
		// expanded include children (carve-rs#4331). The record holds the
		// parse entries too.
		renderOpts["includes"] = parseOpts["includes"]
		var rendered RenderResult
		err = callFS(ctx, map[string]any{"call": "renderWithDiagnostics", "source": source, "options": renderOpts}, &rendered, mount)
		if err != nil {
			return IncludeResult{}, err
		}
		return IncludeResult{Output: rendered.HTML, Diagnostics: rendered.Diagnostics, SuppressedDiagnostics: rendered.SuppressedDiagnostics, Dependencies: deps, Warnings: includeWarnings(rendered.Diagnostics, inc.Root), SuppressedWarnings: rendered.SuppressedDiagnostics}, nil
	}
	var written struct {
		Output      string       `json:"output"`
		Diagnostics []Diagnostic `json:"diagnostics"`
	}
	err = Call(ctx, map[string]any{"call": "write", "tree": parsed.Tree, "target": string(format)}, &written)
	if unavailable(err) {
		return IncludeResult{}, fmt.Errorf("carve: %s writer not available yet: %w", format, err)
	}
	if err != nil {
		return IncludeResult{}, err
	}
	all := append(parsed.Diagnostics, written.Diagnostics...)
	return IncludeResult{Output: written.Output, Diagnostics: all, SuppressedDiagnostics: parsed.SuppressedDiagnostics, Dependencies: deps, Warnings: includeWarnings(all, inc.Root), SuppressedWarnings: parsed.SuppressedDiagnostics}, nil
}
