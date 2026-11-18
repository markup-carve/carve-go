package carve2

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// LintOptions selects catalog rules and platform profiles. Rule values are
// "on" or "off"; the engine validates names and values.
type LintOptions struct {
	Rules     map[string]string
	Platforms []string
	Include   *Include
}

func Lint(ctx context.Context, source string, opts LintOptions) ([]Diagnostic, error) {
	options := map[string]any{}
	if opts.Rules != nil {
		options["rules"] = opts.Rules
	}
	if opts.Platforms != nil {
		options["platforms"] = opts.Platforms
	}
	var result struct {
		Diagnostics []Diagnostic `json:"diagnostics"`
	}
	if opts.Include == nil {
		err := Call(ctx, map[string]any{"call": "lint", "source": source, "options": options}, &result)
		return result.Diagnostics, err
	}
	file, err := opts.Include.guestFile()
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(opts.Include.Root)
	if err != nil {
		return nil, fmt.Errorf("carve2: open include root: %w", err)
	}
	defer root.Close()
	options["includes"] = map[string]any{"root": "/carve-root", "file": file}
	err = callFS(ctx, map[string]any{"call": "lint", "source": source, "options": options}, &result, confinedFS{root: root})
	if err != nil && strings.Contains(err.Error(), "include root cannot be read") {
		return nil, fmt.Errorf("carve2: WASI include resolver not available yet: %w", err)
	}
	return result.Diagnostics, err
}
