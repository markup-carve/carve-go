package carve

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	expsys "github.com/tetratelabs/wazero/experimental/sys"
	"github.com/tetratelabs/wazero/experimental/sysfs"
)

// guestRoot is where the containment root is mounted inside the guest.
//
// The engine sees only this path, so its warnings name `/carve-root/...` and no
// host path crosses back into a rendered document (spec I7).
const guestRoot = "/carve-root"

// Include turns on processor-level file inclusion for one render (spec PART 9
// section 19). The zero value leaves `{{ path }}` directives literal, which is
// what every other entry point in this package does.
//
// Resolution happens INSIDE the guest. Root is mounted read-only as the guest's
// only filesystem, and the engine's own resolver decides containment over
// canonical paths. There is no host callback: a WASI guest cannot call back into
// Go, and it does not need to.
type Include struct {
	// Root is the containment root (spec I10): an ABSOLUTE host directory.
	//
	// A relative Root is REFUSED rather than absolutized. Section 19 forbids
	// containment defaulting to the process working directory, and every
	// canonicalizer resolves a relative path against exactly that, so the
	// refusal is the rule rather than a strictness. The engine's own resolver
	// refuses one for the same reason; carve-go refuses it here so the message
	// names the Go field.
	//
	// Root is mounted as written: not cleaned, not canonicalized, not joined
	// with anything. What the caller configured is what bounds the expansion.
	Root string

	// SourcePath is the document's own path, which must be absolute and inside
	// Root. Relative directives resolve against ITS directory (spec I1), and it
	// is the identity warnings raised in the root document are attributed to.
	//
	// The file need not hold the source being rendered. carve-go serves the
	// `source` argument for this one path, so a caller that has already
	// transformed the bytes - Hugo strips front matter before rendering - gets
	// its own text expanded while relative includes still resolve from where
	// the document actually lives.
	SourcePath string
}

// IncludeWarning is one degradation the include pass reported (spec I7).
//
// Every failure mode leaves the offending directive LITERAL in the output;
// inclusion never silently drops one.
type IncludeWarning struct {
	// Rule is the stable, cross-engine id: "include-unresolved",
	// "include-denied", "include-cycle", "include-depth", "include-budget",
	// "include-call-limit", "include-section-missing", "include-heading-clamp".
	Rule string
	// Message is the engine's human-readable explanation. Section I7 keeps host
	// paths out of it: "outside the root" and "not there" both surface as
	// include-unresolved so a rendered page cannot report the difference.
	Message string
	// File is the host path of the document the warning arose IN, empty when
	// the engine named none.
	File string
}

// IncludeDependency is one include target touched during expansion (spec I11).
//
// A host watching these for changes re-renders when any of them moves. Refused
// targets are reported too: a host watching only what it read would never learn
// that a previously-missing target now exists.
type IncludeDependency struct {
	// Path is the host path of a target that was READ. For a target that was
	// not, it is the directive path exactly as written, because no host path
	// was ever established for it.
	Path string
	// Resolved reports whether the target's source was read. It says nothing
	// about whether the content was merged: a file that was read and then
	// rejected (a cycle, an exhausted budget) stays true, because editing it
	// has to invalidate the host's cache.
	Resolved bool
}

// IncludeResult is what RenderWithIncludes produced.
type IncludeResult struct {
	// Output is the rendered document.
	Output string
	// Warnings are the degradations the pass reported, in engine order.
	Warnings []IncludeWarning
	// SuppressedWarnings counts warnings raised but not retained once the
	// engine's cap was reached. Non-zero means Warnings is a sample - and,
	// because refused dependencies are read off the warnings, that Dependencies
	// is a sample of its own refused half.
	SuppressedWarnings int
	// Dependencies are the targets touched, de-duplicated, read targets first
	// in the order the guest opened them.
	//
	// READ targets are complete: they come from the mount, which sees every
	// file the guest opened. REFUSED targets come from Warnings and are
	// therefore capped with them - past 100 warnings, a host cannot get the
	// rest from here. Treat SuppressedWarnings > 0 as "rebuild unconditionally"
	// rather than trusting the refused half of this list.
	//
	// Closing that needs the ENGINE to publish its own I11 list; its CLI has no
	// flag for it today, and reconstructing the missing entries from filesystem
	// traces would invent directory entries and still miss the targets refused
	// for containment, which never reach the filesystem at all.
	Dependencies []IncludeDependency
}

// RenderWithIncludes renders source with `{{ path }}` directives expanded.
//
// It uses context.Background(); prefer RenderWithIncludesContext for untrusted
// input, for the same reason ToHTML does.
func RenderWithIncludes(source string, format OutputFormat, opts Options, inc Include) (IncludeResult, error) {
	return RenderWithIncludesContext(context.Background(), source, format, opts, inc)
}

// RenderWithIncludesContext is RenderWithIncludes with a caller-supplied context.
//
// The engine's section 19 limits apply with their spec defaults and are not
// configurable from here: transitive depth 16, a byte budget of max(1 MB, 8x the
// root source), 1000 resolver calls, 100 retained warnings and a 4 MiB per-file
// read cap. The engine CLI exposes no flag for any of them, so carve-go cannot
// widen or narrow one, and silently accepting a knob it could not honor would be
// worse than not offering it.
func RenderWithIncludesContext(
	ctx context.Context,
	source string,
	format OutputFormat,
	opts Options,
	inc Include,
) (IncludeResult, error) {
	if opts.Static && format != OutputHTML {
		return IncludeResult{}, fmt.Errorf("carve: Options.Static applies to HTML only, not %q", format.flag())
	}
	docPath, err := inc.guestDocPath()
	if err != nil {
		return IncludeResult{}, err
	}

	eng, err := loadEngine()
	if err != nil {
		return IncludeResult{}, err
	}
	args, err := renderArgs(format, opts)
	if err != nil {
		return IncludeResult{}, err
	}
	args = append(args, "--include-root", guestRoot, path.Join(guestRoot, docPath))

	mount := newIncludeFS(inc.Root, docPath, source)
	fsCfg, ok := wazero.NewFSConfig().(sysfs.FSConfig)
	if !ok {
		return IncludeResult{}, fmt.Errorf("carve: wazero FSConfig does not accept a sys.FS mount")
	}

	// Stdin stays empty: the document reaches the engine through the mount, at
	// the path relative resolution has to key off.
	out, code, err := runEngineFS(ctx, eng, args, "", fsCfg.WithSysFSMount(mount, guestRoot))
	if err != nil {
		return IncludeResult{}, err
	}
	if code != 0 {
		return IncludeResult{}, fmt.Errorf("carve: engine exited with code %d: %s", code, out.stderr)
	}

	warnings, suppressed := parseIncludeWarnings(out.stderr, inc.Root)
	return IncludeResult{
		Output:             out.stdout,
		Warnings:           warnings,
		SuppressedWarnings: suppressed,
		Dependencies:       dependencies(mount.read(), warnings, inc.Root, docPath),
	}, nil
}

// guestDocPath checks the pair and returns SourcePath relative to Root, in
// guest (slash-separated) form.
func (inc Include) guestDocPath() (string, error) {
	if inc.Root == "" {
		return "", fmt.Errorf("carve: Include.Root is required to expand includes")
	}
	if !filepath.IsAbs(inc.Root) {
		return "", fmt.Errorf(
			"carve: Include.Root must be an absolute path, got %q: a relative root would "+
				"resolve against the process working directory, which spec section 19 forbids "+
				"containment defaulting to", inc.Root)
	}
	if inc.SourcePath == "" {
		return "", fmt.Errorf("carve: Include.SourcePath is required to expand includes")
	}
	if !filepath.IsAbs(inc.SourcePath) {
		return "", fmt.Errorf("carve: Include.SourcePath must be an absolute path, got %q", inc.SourcePath)
	}
	rel, err := filepath.Rel(inc.Root, inc.SourcePath)
	if err != nil {
		return "", fmt.Errorf("carve: Include.SourcePath %q is not inside Include.Root %q", inc.SourcePath, inc.Root)
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("carve: Include.SourcePath %q is not inside Include.Root %q", inc.SourcePath, inc.Root)
	}
	return rel, nil
}

// includeWarningLine matches the engine's include warning format:
//
//	carve: <file>: <message> [<rule>]
//
// The rule id is what makes the line identifiable; it is the stable,
// cross-engine token, and no other engine diagnostic ends in one.
var includeWarningLine = regexp.MustCompile(`^carve: (.*): (.*) \[([a-z0-9-]+)\]$`)

// includeSuppressedLine matches the engine's warning-cap notice.
var includeSuppressedLine = regexp.MustCompile(`^carve: (\d+) additional include warning\(s\) suppressed$`)

// includeQuotedPath pulls the directive path out of a warning message. The
// engine quotes it, and it is the only quoted run in any include warning.
var includeQuotedPath = regexp.MustCompile(`"([^"]*)"`)

func parseIncludeWarnings(stderr, root string) ([]IncludeWarning, int) {
	var (
		warnings   []IncludeWarning
		suppressed int
	)
	for _, line := range strings.Split(stderr, "\n") {
		if m := includeWarningLine.FindStringSubmatch(line); m != nil {
			warnings = append(warnings, IncludeWarning{
				Rule:    m[3],
				Message: m[2],
				File:    hostPath(root, m[1]),
			})
			continue
		}
		if m := includeSuppressedLine.FindStringSubmatch(line); m != nil {
			n, err := strconv.Atoi(m[1])
			if err == nil {
				suppressed = n
			}
		}
	}
	return warnings, suppressed
}

// hostPath maps a guest path under the mount back to the host path it came
// from. Anything else - "<stdin>", a path outside the mount - is left alone
// rather than joined onto the root, which would invent a file.
func hostPath(root, guest string) string {
	if guest == guestRoot {
		return root
	}
	rel, ok := strings.CutPrefix(guest, guestRoot+"/")
	if !ok {
		return ""
	}
	return filepath.Join(root, filepath.FromSlash(rel))
}

// dependencies assembles the section I11 report from the two things carve-go
// can observe.
//
// READ targets come from the mount, which sees every file the guest opened: an
// exact list of host paths, in open order, with the root document itself
// dropped because it is not one of its own includes.
//
// REFUSED targets come from the warnings, which name the directive path as
// written. No host path exists for them - that is what refused means - and the
// engine deliberately does not say which refusal it was, so a rendered page
// cannot tell "outside the root" from "not there" (spec I7).
func dependencies(read []string, warnings []IncludeWarning, root, docPath string) []IncludeDependency {
	var (
		deps     []IncludeDependency
		seenRead = map[string]bool{}
		seenName = map[string]bool{}
	)
	for _, rel := range read {
		if rel == docPath || seenRead[rel] {
			continue
		}
		seenRead[rel] = true
		deps = append(deps, IncludeDependency{Path: filepath.Join(root, filepath.FromSlash(rel)), Resolved: true})
	}
	for _, w := range warnings {
		if !strings.HasPrefix(w.Rule, "include-") {
			continue
		}
		m := includeQuotedPath.FindStringSubmatch(w.Message)
		if m == nil || seenName[m[1]] {
			continue
		}
		seenName[m[1]] = true
		deps = append(deps, IncludeDependency{Path: m[1]})
	}
	return deps
}

// includeFS is the guest's whole filesystem for one include expansion.
//
// It is the containment root, read-only, with two additions: it serves the
// caller's in-memory source at the document's own path, and it records what the
// guest opened so the caller learns its dependencies.
//
// CONTAINMENT IS NOT ENFORCED HERE, and saying so matters. A wazero preopen
// bounds which paths the guest can NAME; it does not stop the host from
// following a symlink out of the mounted tree. Measured against wazero 1.12 on
// this artifact: a guest read through a symlink pointing outside the mount
// returns the outside file's bytes. What refuses it is the engine's own
// resolver, which canonicalizes each candidate and compares it against the
// canonical root before opening anything - and under WASI that canonicalization
// walks the link itself, so the escape never reaches a read. The mount is the
// namespace; the engine is the guard.
type includeFS struct {
	expsys.FS
	root    string
	docPath string
	doc     expsys.FS

	mu   sync.Mutex
	open []string
}

func newIncludeFS(root, docPath, source string) *includeFS {
	return &includeFS{
		FS:      &sysfs.ReadFS{FS: sysfs.DirFS(root)},
		root:    root,
		docPath: docPath,
		doc:     &sysfs.AdaptFS{FS: singleFileFS(source)},
	}
}

func (i *includeFS) OpenFile(name string, flag expsys.Oflag, perm fs.FileMode) (expsys.File, expsys.Errno) {
	if name == i.docPath {
		f, errno := i.doc.OpenFile(singleFileName, flag, perm)
		if errno == 0 {
			i.record(name)
		}
		return f, errno
	}
	f, errno := i.FS.OpenFile(name, flag, perm)
	// Directories are opened too - the preopen itself, and every component the
	// guest walks - and none of them is an include target.
	if errno == 0 {
		if dir, dirErrno := f.IsDir(); dirErrno != 0 || !dir {
			i.record(name)
		}
	}
	return f, errno
}

func (i *includeFS) record(name string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.open = append(i.open, name)
}

func (i *includeFS) read() []string {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]string(nil), i.open...)
}

// singleFileName is the only entry singleFileFS carries.
const singleFileName = "source"

// singleFileFS serves one in-memory document, so the caller's own bytes can
// stand in for the file at Include.SourcePath.
type singleFileFS string

func (s singleFileFS) Open(name string) (fs.File, error) {
	if name != singleFileName {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return &memFile{name: name, data: string(s)}, nil
}

type memFile struct {
	name string
	data string
	off  int
}

func (m *memFile) Stat() (fs.FileInfo, error) {
	return memInfo{name: m.name, size: int64(len(m.data))}, nil
}
func (m *memFile) Close() error { return nil }

func (m *memFile) Read(p []byte) (int, error) {
	if m.off >= len(m.data) {
		return 0, io.EOF
	}
	n := copy(p, m.data[m.off:])
	m.off += n
	return n, nil
}

type memInfo struct {
	name string
	size int64
}

func (m memInfo) Name() string       { return m.name }
func (m memInfo) Size() int64        { return m.size }
func (m memInfo) Mode() fs.FileMode  { return 0o444 }
func (m memInfo) ModTime() time.Time { return time.Time{} }
func (m memInfo) IsDir() bool        { return false }
func (m memInfo) Sys() any           { return nil }
