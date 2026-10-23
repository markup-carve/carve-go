package carve

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/experimental/sysfs"
)

// LintFinding is one construct the engine's lint pass reports: something that
// parses but does not reach the page the way the author meant.
type LintFinding struct {
	// Path is the engine's name for the input. Source given to Lint is read
	// from stdin, so it is "<stdin>"; under LintOptions.Include it is the host
	// path of the file the finding arose in.
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
	// File is the included file the finding arose in, slash-separated and
	// relative to Include.Root. It is empty for the document's own findings
	// and whenever LintOptions.Include is nil.
	File string
	// IncludedBy is the chain of include directives that reached File, the
	// linted document first. It is empty for the document's own findings.
	IncludedBy []LintSite
}

// LintSite is one include directive in a LintFinding's chain.
type LintSite struct {
	// File is relative to Include.Root, slash-separated.
	File string
	// Line and Column locate the directive, 1-based, Column in codepoints.
	Line   int
	Column int
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
	// Include, when set, expands include directives under Include.Root and
	// lints every included Carve file too, plus the directives that stay
	// literal (include-unresolved, include-cycle, ...). Those findings carry
	// File and IncludedBy. The source is served at Include.SourcePath, as for
	// RenderWithIncludes.
	Include *Include
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
	var (
		out    engineOutput
		status int
	)
	if opts.Include == nil {
		out, status, err = runEngine(ctx, eng, args, source)
	} else {
		docPath, pathErr := opts.Include.guestDocPath()
		if pathErr != nil {
			return nil, pathErr
		}
		args = append(args, "--include-root", guestRoot, path.Join(guestRoot, docPath))
		fsCfg, ok := wazero.NewFSConfig().(sysfs.FSConfig)
		if !ok {
			return nil, fmt.Errorf("carve: wazero FSConfig does not accept a sys.FS mount")
		}
		mount := newIncludeFS(opts.Include.Root, docPath, source)
		out, status, err = runEngineFS(ctx, eng, args, "", fsCfg.WithSysFSMount(mount, guestRoot))
	}
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
	findings, err := parseLintFindings(out.stdout)
	if err != nil || opts.Include == nil {
		return findings, err
	}
	for i := range findings {
		resolveIncludeFinding(&findings[i], opts.Include)
	}
	return findings, nil
}

// lintChain matches the include chain the engine appends to a finding that
// arose in an included file: ` (included from DOC:L:C > a.crv:L:C)`.
var (
	lintChain = regexp.MustCompile(`^(.*) \(included from (.+)\)$`)
	lintSite  = regexp.MustCompile(`^(.+):(\d+):(\d+)$`)
)

// resolveIncludeFinding splits the chain off the message and maps the guest
// paths back: the document itself to Include.SourcePath, an included file to
// its root-relative File.
func resolveIncludeFinding(f *LintFinding, inc *Include) {
	f.IncludedBy = []LintSite{}
	m := lintChain.FindStringSubmatch(f.Message)
	if m == nil {
		f.Path = inc.SourcePath
		return
	}
	sites := []LintSite{}
	for _, part := range strings.Split(m[2], " > ") {
		s := lintSite.FindStringSubmatch(part)
		if s == nil {
			// Not a chain after all; leave the message whole.
			f.Path = inc.SourcePath
			return
		}
		line, _ := strconv.Atoi(s[2])
		column, _ := strconv.Atoi(s[3])
		sites = append(sites, LintSite{File: rootRelative(s[1]), Line: line, Column: column})
	}
	f.Message = m[1]
	f.IncludedBy = sites
	f.File = rootRelative(f.Path)
	f.Path = filepath.Join(inc.Root, filepath.FromSlash(f.File))
}

// rootRelative turns a guest path under the mount into a root-relative one;
// the engine already prints included files relative to the root.
func rootRelative(name string) string {
	if rel, ok := strings.CutPrefix(name, guestRoot+"/"); ok {
		return rel
	}
	return name
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
