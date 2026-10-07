package carve

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// LintFinding is one construct the engine's lint pass reports: something that
// parses but does not reach the page the way the author meant.
type LintFinding struct {
	// Path is the engine's name for the input. Source given to Lint is read
	// from stdin, so it is "<stdin>".
	Path string
	// Line and Column are 1-based, and Column COUNTS UNICODE CODEPOINTS
	// rather than bytes, the same unit the engine's positions use
	// everywhere. Index a Go string by it only after []rune conversion.
	Line   int
	Column int
	// Rule is the engine's rule id, such as broken-fragment-link.
	Rule string
	// Message is the engine's prose for the finding.
	Message string
}

// LintOptions selects what the lint pass runs. The zero value is the engine's
// own default, which is the core rules and no extension.
type LintOptions struct {
	// Extensions enables registry extensions for the pass, one
	// --extension KEY each. The accepted keys are not listed here, so an
	// unaccepted one comes back as the engine's own error naming what it
	// knows. `citations` is the only key the pass takes at carve-rs 0.1.8.
	Extensions []string
	// Bundle enables the bundled extensions (--extensions). It changes which
	// findings the pass reports, not only which it adds: with the bundle on,
	// the embedded engine resolves fragment links that the core rules report
	// as broken.
	Bundle bool
}

// lintFindingLine matches `PATH:LINE:COLUMN RULE <sep> MESSAGE`.
//
// The engine separates rule from message with an em dash. A hyphen is accepted
// too so a cosmetic change upstream does not turn every finding into an error,
// and the path pattern is greedy on purpose: a Windows path carries its own
// colons and only the last two are the position.
var lintFindingLine = regexp.MustCompile(`^(.+):(\d+):(\d+) (\S+) (?:\x{2014}|-) (.*)$`)

// Lint reports the constructs the engine's lint pass finds in source.
//
// Findings are the answer, not a failure: a document with findings returns them
// with a nil error, and a clean document returns an empty, non-nil slice. An
// error means the pass could not run.
//
// Uses context.Background(); prefer LintContext for untrusted input.
func Lint(source string) ([]LintFinding, error) {
	return LintWithOptionsContext(context.Background(), source, LintOptions{})
}

// LintContext is Lint with a caller-supplied context.
func LintContext(ctx context.Context, source string) ([]LintFinding, error) {
	return LintWithOptionsContext(ctx, source, LintOptions{})
}

// LintWithOptions is Lint with explicit options.
func LintWithOptions(source string, opts LintOptions) ([]LintFinding, error) {
	return LintWithOptionsContext(context.Background(), source, opts)
}

// LintWithOptionsContext is LintWithOptions with a caller-supplied context.
func LintWithOptionsContext(ctx context.Context, source string, opts LintOptions) ([]LintFinding, error) {
	eng, err := loadEngine()
	if err != nil {
		return nil, err
	}
	args := []string{"carve", "lint"}
	if opts.Bundle {
		args = append(args, "--extensions")
	}
	for _, key := range opts.Extensions {
		args = append(args, "--extension", key)
	}
	out, status, err := runEngine(ctx, eng, args, source)
	if err != nil {
		return nil, err
	}
	// 0 is no findings and 1 is findings reported; 2 is an unreadable input or
	// an option the engine did not understand, which is the only failure.
	if status != 0 && status != 1 {
		detail := out.stderr
		if detail == "" {
			detail = fmt.Sprintf("exit %d", status)
		}
		return nil, fmt.Errorf("carve: lint failed: %s", detail)
	}
	return parseLintFindings(out.stdout)
}

func parseLintFindings(stdout string) ([]LintFinding, error) {
	findings := []LintFinding{}
	for _, line := range strings.Split(stdout, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		match := lintFindingLine.FindStringSubmatch(line)
		if match == nil {
			return nil, fmt.Errorf("carve: unreadable lint finding: %q", line)
		}
		// The pattern only matches digit runs, so the sole way these parses
		// fail is a number too large for an int, which is not a finding.
		lineNo, err := strconv.Atoi(match[2])
		if err != nil {
			return nil, fmt.Errorf("carve: unreadable lint finding: %q", line)
		}
		column, err := strconv.Atoi(match[3])
		if err != nil {
			return nil, fmt.Errorf("carve: unreadable lint finding: %q", line)
		}
		findings = append(findings, LintFinding{
			Path:    match[1],
			Line:    lineNo,
			Column:  column,
			Rule:    match[4],
			Message: match[5],
		})
	}
	return findings, nil
}
