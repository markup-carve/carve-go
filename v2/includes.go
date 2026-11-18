package carve2

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
		return "", errors.New("carve2: Include.Root and Include.SourcePath must be absolute paths")
	}
	rel, err := filepath.Rel(inc.Root, inc.SourcePath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("carve2: source path is outside the include root")
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
	for _, key := range []string{"mode", "profile", "rawHtml", "urlPolicy", "strictLosses", "roundtrip", "sourceLines", "symbols"} {
		delete(parseOpts, key)
	}
	parseOpts["includes"] = map[string]any{"root": "/carve-root", "file": file}
	root, err := os.OpenRoot(inc.Root)
	if err != nil {
		return IncludeResult{}, fmt.Errorf("carve2: open include root: %w", err)
	}
	defer root.Close()
	mount := confinedFS{root: root}
	var parsed ParseResult
	err = callFS(ctx, map[string]any{"call": "parseWithDiagnostics", "source": source, "options": parseOpts}, &parsed, mount)
	if err != nil {
		if strings.Contains(err.Error(), "include root cannot be read") {
			return IncludeResult{}, fmt.Errorf("carve2: WASI include resolver not available yet: %w", err)
		}
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
		var rendered RenderResult
		err = Call(ctx, map[string]any{"call": "renderWithDiagnostics", "tree": json.RawMessage(parsed.Tree), "options": renderOpts}, &rendered)
		if err != nil {
			return IncludeResult{}, err
		}
		all := append(parsed.Diagnostics, rendered.Diagnostics...)
		return IncludeResult{Output: rendered.HTML, Diagnostics: all, SuppressedDiagnostics: parsed.SuppressedDiagnostics + rendered.SuppressedDiagnostics, Dependencies: deps, Warnings: includeWarnings(all, inc.Root), SuppressedWarnings: parsed.SuppressedDiagnostics}, nil
	}
	if format == OutputCarve {
		return IncludeResult{}, errors.New("carve2: carve writer not available yet for included trees")
	}
	var written struct {
		Output      string       `json:"output"`
		Diagnostics []Diagnostic `json:"diagnostics"`
	}
	err = Call(ctx, map[string]any{"call": "write", "tree": parsed.Tree, "target": string(format)}, &written)
	if unavailable(err) {
		return IncludeResult{}, fmt.Errorf("carve2: %s writer not available yet: %w", format, err)
	}
	if err != nil {
		return IncludeResult{}, err
	}
	all := append(parsed.Diagnostics, written.Diagnostics...)
	return IncludeResult{Output: written.Output, Diagnostics: all, SuppressedDiagnostics: parsed.SuppressedDiagnostics, Dependencies: deps, Warnings: includeWarnings(all, inc.Root), SuppressedWarnings: parsed.SuppressedDiagnostics}, nil
}
