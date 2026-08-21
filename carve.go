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
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

//go:embed internal/wasm/carve.wasm
var carveWasm []byte

// maxMemoryPages caps the linear memory a single render instance may grow to.
//
// Wasm memory is paged at 64 KiB per page, and wazero's default ceiling is
// 65536 pages (4 GiB) per instance. That default lets one untrusted document
// drive the host toward 4 GiB of allocation per concurrent call. We cap it at
// 8192 pages (512 MiB), which is comfortably more than any reasonable Carve
// document needs to parse and render, while keeping a runaway/adversarial input
// from exhausting host memory. An allocation past this cap fails inside the
// guest (memory.grow returns -1) and surfaces as a non-zero engine exit rather
// than OOM-killing the host process.
const maxMemoryPages uint32 = 8192

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

func loadEngine() (*compiledEngine, error) {
	engineOnce.Do(func() {
		// The one-time runtime build and wasm compilation use a fresh
		// background context, NOT a caller's per-call context. Because this is
		// guarded by sync.Once, a caller passing an already-canceled or
		// short-deadline context must not be able to abort compilation and
		// cache that error permanently, which would poison the shared engine
		// for every later caller. wazero's compiler honors ctx.Err() during
		// compilation, so binding the build to a call context would expose
		// exactly that hazard. Per-call cancellation is enforced separately at
		// InstantiateModule time using the caller's context.
		ctx := context.Background()

		// WithCloseOnContextDone(true) is what makes a caller's context
		// deadline/cancellation actually interrupt CPU-bound guest code:
		// without it, wazero never checks the context once a wasm function is
		// running, so a per-call timeout is a no-op against a long parse loop.
		// WithMemoryLimitPages caps linear memory per instance (see
		// maxMemoryPages) so one input cannot drive the host toward 4 GiB.
		cfg := wazero.NewRuntimeConfig().
			WithCloseOnContextDone(true).
			WithMemoryLimitPages(maxMemoryPages)
		rt := wazero.NewRuntimeWithConfig(ctx, cfg)
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

	// Safe escapes =html raw blocks and spans instead of emitting them. It
	// maps to the engine CLI flag --safe (--no-raw-html).
	//
	// Carve's normative hardening is always on and needs no option here:
	// dangerous URL schemes are blanked, event-handler attributes dropped,
	// and the bidi override/isolate characters behind Trojan Source removed.
	// Raw passthrough is the deliberate exception - a =html block renders
	// verbatim by design - so it is the one thing untrusted input must turn
	// off. Set Safe for anything you did not author yourself.
	Safe bool

	// Symbols maps a shortcode name to the text that replaces it, so
	// `:NAME:` in the source renders as VALUE. Each entry maps to one
	// repeatable engine CLI flag --symbol NAME=VALUE.
	//
	// Unlike build-time renderer injection (see Static), this crosses the
	// WASI/CLI boundary without trouble: a symbol map is plain data, not a
	// host closure, so there is nothing for the stdio contract to lose.
	//
	// A name the map does not carry is left alone - `:unknown:` stays
	// literal text rather than becoming an error or an empty string. The
	// engine's word-boundary rule is unchanged by the map: a shortcode is
	// only recognized after a boundary, so `a:NAME:b` and `3:NAME:4` stay
	// literal however the map is populated.
	//
	// The keys are sorted before they are emitted, so the same map always
	// produces the same argument list. Go randomizes map iteration order by
	// design, and passing that order straight through would make each call
	// build a different command line - harmless for the output, but it makes
	// the invocation irreproducible and any test asserting on it flake
	// intermittently, which is a miserable thing to debug later.
	//
	// SECURITY: values are substituted RAW, exactly as written, and are NOT
	// escaped. That is deliberate across every Carve engine - it is what lets
	// a symbol expand to markup such as an <img> tag - but it means the map
	// is trusted processor configuration, on the same footing as the code
	// calling this package. NEVER build a symbols map out of untrusted or
	// user-supplied input: a value is a script-injection vector, and Safe
	// does not constrain it (Safe governs =html raw blocks in the DOCUMENT,
	// not this configuration). Populate it from your own site or application
	// config and nowhere else.
	//
	// An entry that cannot reach the engine intact is refused with an error
	// rather than silently reshaped: a name may not be empty or contain "=",
	// and neither half may contain a NUL. The "=" rule is the load-bearing
	// one - the engine splits the argument at its FIRST "=", so a name of
	// "a=b" with a value of "c" would arrive as the name "a" mapped to "b=c",
	// a different map than the caller wrote, and nothing would report it.
	//
	// A name the engine's shortcode grammar cannot match is NOT rejected; it
	// is simply inert, the same as an entry nothing references.
	Symbols map[string]string

	// Profile restricts which constructs are allowed at all and caps input
	// length: "full", "article", "comment" or "minimal". Empty means no
	// profile. It maps to the engine CLI flag --profile.
	//
	// The name is validated by the engine rather than duplicated here, so a
	// profile added to the engine works without a change in this package; an
	// unknown name comes back as an error carrying the engine's message.
	Profile string
}

// ToHTML renders Carve source to HTML using the embedded engine.
//
// It is safe to call concurrently from multiple goroutines: the wasm module is
// compiled once and a fresh instance is created per call with isolated stdio.
//
// This is the interactive default: live HTML with no bundled extensions
// enabled. For self-contained static HTML, see ToHTMLStatic or ToHTMLOptions.
//
// It uses context.Background() and is therefore unbounded in time. For
// untrusted input, prefer ToHTMLContext with a deadline so a pathological
// (super-linear) document cannot occupy a goroutine indefinitely; see the
// "Resource limits and untrusted input" section of the package README.
func ToHTML(source string) (string, error) {
	return ToHTMLContext(context.Background(), source)
}

// ToHTMLContext is ToHTML with a caller-supplied context. The context bounds
// the per-call module execution: a deadline or cancellation interrupts
// CPU-bound parsing inside the engine and returns an error satisfying
// errors.Is(err, context.DeadlineExceeded) / context.Canceled. Use this with a
// deadline for untrusted input. (The one-time wasm compilation runs under a
// background context, so a single caller cannot poison the shared engine.)
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
// bounds the per-call module execution (a deadline/cancellation interrupts the
// running render). The one-time wasm compilation runs under a background
// context, so a single caller's canceled context cannot poison the shared
// engine for later callers.
func ToHTMLOptionsContext(ctx context.Context, source string, opts Options) (string, error) {
	return RenderContext(ctx, source, OutputHTML, opts)
}

// OutputFormat selects what the engine writes. It mirrors the engine CLI's
// output-format flags, which are mutually exclusive there and here.
type OutputFormat string

const (
	// OutputHTML is HTML (the engine default). The zero OutputFormat means
	// this, so an existing caller that passes no format keeps HTML.
	OutputHTML OutputFormat = ""
	// OutputMarkdown is Markdown (CLI --markdown).
	OutputMarkdown OutputFormat = "--markdown"
	// OutputPlainText is unstyled plain text (CLI --plain).
	OutputPlainText OutputFormat = "--plain"
	// OutputANSI is plain text with ANSI terminal styling (CLI --ansi).
	OutputANSI OutputFormat = "--ansi"
	// OutputCarve is canonical Carve source (CLI --carve) - the formatter's
	// output. Rendering to this format is how you format a document.
	OutputCarve OutputFormat = "--carve"
)

func (f OutputFormat) flag() string {
	if f == OutputHTML {
		// Passed explicitly rather than relying on the engine default, so the
		// argv this package builds is self-documenting.
		return "--html"
	}
	return string(f)
}

// ToMarkdown renders Carve source to Markdown.
//
// Uses context.Background(); prefer ToMarkdownContext for untrusted input, for
// the same reason ToHTML does.
func ToMarkdown(source string) (string, error) {
	return RenderContext(context.Background(), source, OutputMarkdown, Options{})
}

// ToMarkdownContext is ToMarkdown with a caller-supplied context bounding the
// per-call module execution.
func ToMarkdownContext(ctx context.Context, source string) (string, error) {
	return RenderContext(ctx, source, OutputMarkdown, Options{})
}

// ToPlainText renders Carve source to unstyled plain text.
//
// Uses context.Background(); prefer ToPlainTextContext for untrusted input.
func ToPlainText(source string) (string, error) {
	return RenderContext(context.Background(), source, OutputPlainText, Options{})
}

// ToPlainTextContext is ToPlainText with a caller-supplied context.
func ToPlainTextContext(ctx context.Context, source string) (string, error) {
	return RenderContext(ctx, source, OutputPlainText, Options{})
}

// ToANSI renders Carve source to plain text with ANSI terminal styling.
//
// Uses context.Background(); prefer ToANSIContext for untrusted input.
func ToANSI(source string) (string, error) {
	return RenderContext(context.Background(), source, OutputANSI, Options{})
}

// ToANSIContext is ToANSI with a caller-supplied context.
func ToANSIContext(ctx context.Context, source string) (string, error) {
	return RenderContext(ctx, source, OutputANSI, Options{})
}

// ToCarve renders Carve source back to CANONICAL Carve source - that is, it
// formats the document. The engine's writer decides the canonical spelling of
// every construct (PART 11), so this is the same transformation `carve fmt`
// performs, returned as a string instead of written to a file.
//
// Uses context.Background(); prefer ToCarveContext for untrusted input.
func ToCarve(source string) (string, error) {
	return RenderContext(context.Background(), source, OutputCarve, Options{})
}

// ToCarveContext is ToCarve with a caller-supplied context.
func ToCarveContext(ctx context.Context, source string) (string, error) {
	return RenderContext(ctx, source, OutputCarve, Options{})
}

// Render renders Carve source to the given format with the given options.
//
// The named helpers (ToHTML, ToMarkdown, ToPlainText, ToANSI, ToCarve) cover
// the common cases; use this when you need options with a non-HTML format.
//
// Uses context.Background(); prefer RenderContext for untrusted input.
func Render(source string, format OutputFormat, opts Options) (string, error) {
	return RenderContext(context.Background(), source, format, opts)
}

// RenderContext is Render with a caller-supplied context that bounds the
// per-call module execution (a deadline/cancellation interrupts the running
// render). The one-time wasm compilation runs under a background context, so a
// single caller's canceled context cannot poison the shared engine for later
// callers.
//
// Options.Static is HTML-only and is REJECTED with a non-nil error for any
// other format, rather than silently ignored: a caller that asked for static
// output and got interactive output back would have no way to notice.
func RenderContext(ctx context.Context, source string, format OutputFormat, opts Options) (string, error) {
	if opts.Static && format != OutputHTML {
		return "", fmt.Errorf("carve: Options.Static applies to HTML only, not %q", format.flag())
	}

	eng, err := loadEngine()
	if err != nil {
		return "", err
	}

	// argv[0] is the program name; the engine reads source from stdin when no
	// file argument is given.
	args := []string{"carve", format.flag()}
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
	if opts.Safe {
		args = append(args, "--safe")
	}
	if opts.Profile != "" {
		args = append(args, "--profile", opts.Profile)
	}
	symArgs, err := symbolArgs(opts.Symbols)
	if err != nil {
		return "", err
	}
	args = append(args, symArgs...)

	out, code, err := runEngine(ctx, eng, args, source)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("carve: engine exited with code %d: %s", code, out.stderr)
	}
	return out.stdout, nil
}

// ParseAST parses Carve source and returns its AST as JSON.
//
// The PART 12 exchange shape (https://markup-carve.github.io/carve/ast-json):
// the same tree every Carve engine publishes, so a consumer written against one
// implementation reads another's output. The root carries exactly "type",
// "children" and "srcByteLength"; frontmatter and footnote definitions are
// block nodes inside "children", not root fields.
//
// Every node except the root carries "pos" when the engine could place it -
// 1-based lines and columns, 0-based offsets, ends exclusive, counted in
// Unicode CODEPOINTS, not bytes. A node the engine could not place, such as
// reassembled table-cell text, carries no "pos" at all rather than an invented
// one.
//
// json.RawMessage rather than a typed tree on purpose: the node set is spec
// surface that grows, and a Go struct hierarchy would either lag it or force a
// breaking change every time it does. Unmarshal into whatever shape the caller
// actually needs.
//
// It uses context.Background(); prefer ParseASTContext for untrusted input, for
// the same reason ToHTML does.
func ParseAST(source string) (json.RawMessage, error) {
	return ParseASTContext(context.Background(), source)
}

// ParseASTContext is ParseAST with a caller-supplied context that bounds the
// per-call module execution.
func ParseASTContext(ctx context.Context, source string) (json.RawMessage, error) {
	eng, err := loadEngine()
	if err != nil {
		return nil, err
	}

	out, code, err := runEngine(ctx, eng, []string{"carve", "--json"}, source)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("carve: engine exited with code %d: %s", code, out.stderr)
	}
	// Validated rather than returned raw: the engine writes the JSON, so invalid
	// output means the embedded artifact is not what this module thinks it is -
	// a stale .wasm predating --json answers with a usage error on stderr and an
	// empty stdout, which would otherwise reach the caller as "valid" JSON.
	if !json.Valid([]byte(out.stdout)) {
		return nil, fmt.Errorf("carve: engine did not return JSON (stderr: %s)", out.stderr)
	}
	return json.RawMessage(out.stdout), nil
}

// symbolArgs turns a symbol map into the engine's repeatable --symbol
// arguments, in a deterministic order.
//
// The keys are sorted rather than ranged over: Go randomizes map iteration on
// purpose, so an unsorted range would hand the engine a different argument
// list on every call for the same map. The rendered HTML would not differ, but
// the invocation would not be reproducible, and anything asserting on it would
// fail on a schedule nobody can reconstruct.
//
// It refuses exactly the entries that cannot reach the engine INTACT, and
// nothing else:
//
//   - a name containing "=", because the engine splits each --symbol value at
//     its FIRST "=". A name of "a=b" with a value of "c" would arrive as the
//     name "a" mapped to "b=c" - a different map than the caller wrote, with
//     no error raised anywhere. This is the one genuine corruption.
//   - an empty name, which can never be spelled as a shortcode and is caller
//     error rather than configuration.
//   - a NUL in either half, which the wasm runtime refuses outright. Left
//     alone it fails the whole render with an opaque "args invalid" that names
//     neither the entry nor the field, so it is caught here where the message
//     can point at the offending symbol.
//
// Nothing else is rejected, deliberately. A name the engine's shortcode
// grammar will not match - "a b", "a.b", "ä" - is dead configuration, not a
// corrupted argument: it arrives exactly as written and simply never fires.
// Screening for it would mean duplicating the engine's name grammar in Go and
// re-breaking it here every time the engine widens it, which is the tradeoff
// Profile already settles the other way. Newlines are allowed through for the
// same reason plus a positive one: a value is substituted raw, so a multi-line
// value is a legitimate thing to want, and it was measured to round-trip into
// the output unchanged.
func symbolArgs(symbols map[string]string) ([]string, error) {
	if len(symbols) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(symbols))
	for name := range symbols {
		names = append(names, name)
	}
	sort.Strings(names)

	args := make([]string, 0, len(names)*2)
	for _, name := range names {
		value := symbols[name]
		if name == "" {
			return nil, errors.New("carve: symbol name must not be empty")
		}
		if strings.Contains(name, "=") {
			return nil, fmt.Errorf("carve: symbol name %q must not contain %q: the engine "+
				"splits the argument at its first %q, so this would register a different "+
				"name and value than you wrote", name, "=", "=")
		}
		if strings.ContainsRune(name, 0) {
			return nil, fmt.Errorf("carve: symbol name %q must not contain a NUL", name)
		}
		if strings.ContainsRune(value, 0) {
			return nil, fmt.Errorf("carve: value for symbol %q must not contain a NUL", name)
		}
		args = append(args, "--symbol", name+"="+value)
	}
	return args, nil
}

// engineOutput is what one engine invocation produced.
type engineOutput struct {
	stdout string
	stderr string
}

// runEngine invokes the embedded engine with `args`, feeding `source` on stdin,
// and returns its output together with its EXIT CODE.
//
// The code is returned rather than folded into an error because not every
// non-zero exit is a failure: `--stamp-check` answers a question with its exit
// status, so a caller needs to tell "the document predates this engine" from
// "the engine could not run".
func runEngine(
	ctx context.Context,
	eng *compiledEngine,
	args []string,
	source string,
) (engineOutput, int, error) {
	var stdout, stderr bytes.Buffer

	config := wazero.NewModuleConfig().
		WithStdin(strings.NewReader(source)).
		WithStdout(&stdout).
		WithStderr(&stderr).
		WithArgs(args...).
		// Anonymous module name avoids "module already instantiated"
		// collisions when called concurrently.
		WithName("")

	mod, err := eng.runtime.InstantiateModule(ctx, eng.module, config)
	out := func() engineOutput {
		return engineOutput{stdout: stdout.String(), stderr: strings.TrimSpace(stderr.String())}
	}
	if err != nil {
		// A context deadline/cancellation interrupts the guest (thanks to
		// WithCloseOnContextDone) and surfaces as a *sys.ExitError whose
		// special exit code matches context.Canceled / DeadlineExceeded via
		// errors.Is. Translate that back into the caller's context error so an
		// interrupted untrusted input is reported as a timeout, not a generic
		// non-zero engine exit.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return out(), -1, fmt.Errorf("carve: render canceled: %w", ctxErr)
			}
			return out(), -1, fmt.Errorf("carve: render canceled: %w", err)
		}
		if exitErr, ok := err.(*sys.ExitError); ok {
			return out(), int(exitErr.ExitCode()), nil
		}
		return out(), -1, fmt.Errorf("carve: run wasm: %w", err)
	}
	// Module returned without calling proc_exit; close it and return output.
	_ = mod.Close(ctx)
	return out(), 0, nil
}

// Stamp is a document's provenance marker, as written by `carve fmt --stamp`.
//
// It records the Carve spec version a document was last processed under, so
// tooling can flag documents predating a breaking spec change. See
// https://markup-carve.github.io/carve/versioning.
type Stamp struct {
	// Version is the spec version the document was last processed under.
	Version string
	// GeneratedBy is the engine that wrote the marker, empty when the marker
	// records no writer.
	GeneratedBy string
}

// ReadStamp reports the provenance marker a document carries.
//
// The second result is false when the document carries no marker, which is the
// normal case for a hand-written document and means "unknown", not "current".
//
// It uses context.Background(); prefer ReadStampContext for untrusted input, for
// the same reason ToHTML does.
func ReadStamp(source string) (Stamp, bool, error) {
	return ReadStampContext(context.Background(), source)
}

// ReadStampContext is ReadStamp with a caller-supplied context.
func ReadStampContext(ctx context.Context, source string) (Stamp, bool, error) {
	out, code, err := stampQuery(ctx, "--stamp-info", source)
	if err != nil {
		return Stamp{}, false, err
	}
	if code != 0 {
		return Stamp{}, false, fmt.Errorf("carve: engine exited with code %d: %s", code, out.stderr)
	}
	return parseStampInfo(out.stdout)
}

// NeedsReview reports whether a document was last processed under an OLDER spec
// version than the embedded engine targets, so the `[behavior]` changelog
// entries between the two are worth reviewing.
//
// An unstamped document reports true: its provenance is unknown, and assuming it
// is current is the unsafe direction. This mirrors carve-php's
// Stamp::needsReview, carve-js's needsReview and carve-rs's needs_review, so the
// four agree on the same document.
func NeedsReview(source string) (bool, error) {
	return NeedsReviewContext(context.Background(), source)
}

// NeedsReviewContext is NeedsReview with a caller-supplied context.
func NeedsReviewContext(ctx context.Context, source string) (bool, error) {
	// --stamp-check answers with its EXIT STATUS: 1 when the document predates
	// this engine, 0 when it does not. Reading the code rather than the text
	// keeps this independent of the report's wording.
	out, code, err := stampQuery(ctx, "--stamp-check", source)
	if err != nil {
		return false, err
	}
	switch code {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, fmt.Errorf("carve: engine exited with code %d: %s", code, out.stderr)
	}
}

// stampQuery runs one of the engine's stamp modes over `source`.
func stampQuery(ctx context.Context, flag string, source string) (engineOutput, int, error) {
	eng, err := loadEngine()
	if err != nil {
		return engineOutput{}, -1, err
	}

	return runEngine(ctx, eng, []string{"carve", flag}, source)
}

// parseStampInfo reads the engine's --stamp-info report.
//
// The report is three labeled lines, or a single "unstamped …" line. Parsing our
// own stable output is the cost of driving the engine over a stdio boundary; the
// shape is pinned by tests here and by the cross-engine fixtures in carve-rs.
func parseStampInfo(report string) (Stamp, bool, error) {
	var stamp Stamp
	found := false
	for _, line := range strings.Split(report, "\n") {
		switch {
		case strings.HasPrefix(line, "unstamped"):
			return Stamp{}, false, nil
		case strings.HasPrefix(line, "carve-version:"):
			stamp.Version = strings.TrimSpace(strings.TrimPrefix(line, "carve-version:"))
			found = true
		case strings.HasPrefix(line, "generated-by:"):
			writer := strings.TrimSpace(strings.TrimPrefix(line, "generated-by:"))
			// The engine prints this placeholder when the marker records no
			// writer; an empty GeneratedBy says the same thing in Go.
			if writer != "(unrecorded)" {
				stamp.GeneratedBy = writer
			}
		}
	}
	if !found {
		return Stamp{}, false, fmt.Errorf("carve: could not read stamp report: %q", report)
	}

	return stamp, true, nil
}
