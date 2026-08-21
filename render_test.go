package carve

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The engine writes every one of these formats; the point of the assertions is
// that carve-go asks for the RIGHT one. A wrapper that silently rendered HTML
// for all five would satisfy "no error" but nothing else here.

func TestToMarkdown_TranslatesCarveEmphasisToMarkdown(t *testing.T) {
	// The two languages disagree about the delimiter, which is what makes this
	// a real conversion rather than a passthrough: /x/ in Carve is *x* in
	// Markdown, and *x* in Carve is **x**.
	out, err := ToMarkdown("/em/ and *strong*\n")
	if err != nil {
		t.Fatalf("ToMarkdown error: %v", err)
	}
	if !strings.Contains(out, "*em*") || !strings.Contains(out, "**strong**") {
		t.Fatalf("expected Markdown delimiters, got %q", out)
	}
	if strings.Contains(out, "<em>") {
		t.Fatalf("got HTML, not Markdown: %q", out)
	}
}

func TestToPlainText_DropsEveryDelimiter(t *testing.T) {
	out, err := ToPlainText("# Title\n\n/em/ and *strong*\n")
	if err != nil {
		t.Fatalf("ToPlainText error: %v", err)
	}
	if !strings.Contains(out, "em and strong") {
		t.Fatalf("expected undecorated text, got %q", out)
	}
	for _, marker := range []string{"<", "*", "/em/", "\x1b["} {
		if strings.Contains(out, marker) {
			t.Fatalf("plain text still carries %q: %q", marker, out)
		}
	}
}

func TestToANSI_CarriesEscapeSequences(t *testing.T) {
	out, err := ToANSI("*strong*\n")
	if err != nil {
		t.Fatalf("ToANSI error: %v", err)
	}
	// The distinguishing feature against --plain is the escape sequence, so
	// assert on that rather than on the text both formats share.
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("expected ANSI escapes, got %q", out)
	}
}

func TestToCarve_NormalizesToTheCanonicalSpelling(t *testing.T) {
	// Formatting, reached as an output format. Three spaces after the marker is
	// legal input and not canonical output, so a passthrough fails this.
	out, err := ToCarve("#   Title\n")
	if err != nil {
		t.Fatalf("ToCarve error: %v", err)
	}
	if out != "# Title\n" {
		t.Fatalf("expected canonical %q, got %q", "# Title\n", out)
	}
}

func TestToCarve_IsIdempotent(t *testing.T) {
	// PART 11's own invariant. Cheap to assert here and it fails loudly if the
	// embedded engine is ever swapped for one whose writer is not canonical.
	once, err := ToCarve("- a\n-  b\n")
	if err != nil {
		t.Fatalf("ToCarve error: %v", err)
	}
	twice, err := ToCarve(once)
	if err != nil {
		t.Fatalf("ToCarve error on second pass: %v", err)
	}
	if once != twice {
		t.Fatalf("not idempotent: %q then %q", once, twice)
	}
}

func TestRender_StaticIsRejectedForNonHTMLFormats(t *testing.T) {
	// Silently ignoring it would hand back interactive output to a caller who
	// asked for static and had no way to tell.
	for _, format := range []OutputFormat{OutputMarkdown, OutputPlainText, OutputANSI, OutputCarve} {
		if _, err := Render("# x\n", format, Options{Static: true}); err == nil {
			t.Fatalf("format %q accepted Options.Static", format)
		}
	}
	if _, err := Render("# x\n", OutputHTML, Options{Static: true}); err != nil {
		t.Fatalf("HTML rejected Options.Static: %v", err)
	}
}

func TestOutputHTML_IsTheZeroValue(t *testing.T) {
	// Guards the compatibility claim: a caller that never mentions a format
	// keeps getting HTML.
	var unset OutputFormat
	if unset != OutputHTML {
		t.Fatalf("zero OutputFormat is %q, expected OutputHTML", unset)
	}
	out, err := Render("# x\n", unset, Options{})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(out, "<h1") {
		t.Fatalf("zero format did not render HTML: %q", out)
	}
}

func TestRender_OptionsReachEveryFormat(t *testing.T) {
	// Extensions is the cheapest option to observe on a non-HTML target, and it
	// proves the argv builder is shared rather than reimplemented per format:
	// with the details extension off, the container's title is prose the plain
	// renderer emits; with it on, the extension owns the block and the title is
	// widget chrome that plain text has no place for.
	const source = "::: details \"Title\"\nbody\n:::\n"
	off, err := Render(source, OutputPlainText, Options{})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	on, err := Render(source, OutputPlainText, Options{Extensions: []string{"details"}})
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if off == on {
		t.Fatalf("Options.Extensions changed nothing for a non-HTML format: %q", on)
	}
	if !strings.Contains(off, "Title") || strings.Contains(on, "Title") {
		t.Fatalf("unexpected plain output: without extensions %q, with %q", off, on)
	}
}

func TestRender_SymbolMapIsHTMLOnly(t *testing.T) {
	// Pinning ENGINE behavior, not asserting it is right. render_markdown.rs,
	// render_plain.rs and render_ansi.rs each emit a Symbol node as
	// `format!(":{}:", symbol.name)` and never consult the map, so a `:tick:`
	// survives literally on those targets while HTML resolves it.
	//
	// Re-emitting the shortcode is defensible for Markdown (the consumer may
	// have its own shortcode support) and correct for OutputCarve (canonical
	// source keeps what the author wrote). For plain text and ANSI it is a
	// question worth a ruling rather than a behavior worth relying on - see
	// markup-carve/carve. This test exists so that ruling cannot land silently.
	const source = ":tick: done\n"
	symbols := Options{Symbols: map[string]string{"tick": "OK"}}

	html, err := Render(source, OutputHTML, symbols)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(html, "OK done") {
		t.Fatalf("HTML did not resolve the symbol: %q", html)
	}

	for _, format := range []OutputFormat{OutputMarkdown, OutputPlainText, OutputANSI, OutputCarve} {
		out, err := Render(source, format, symbols)
		if err != nil {
			t.Fatalf("Render(%q) error: %v", format, err)
		}
		if !strings.Contains(out, ":tick:") {
			t.Fatalf("format %q resolved the symbol; the engine's behavior changed and carve-go's README is now wrong: %q", format, out)
		}
	}
}

func TestRenderContext_DeadlineIsHonoredForEveryFormat(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	if _, err := RenderContext(ctx, "# x\n", OutputMarkdown, Options{}); err == nil {
		t.Fatal("expected an expired context to fail the render")
	}
}
