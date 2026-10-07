package carve

import (
	"strings"
	"testing"
)

func TestHTMLAndMarkdownMigrationExposeReports(t *testing.T) {
	html, err := FromHTML("<p>Hello <strong>world</strong></p>")
	if err != nil {
		t.Fatal(err)
	}
	if html.Value != "Hello *world*\n" || len(html.Report.Diagnostics) != 0 {
		t.Fatalf("unexpected HTML migration: %#v", html)
	}
	if html.Report.SchemaVersion != 2 {
		t.Fatalf("unexpected schema: %#v", html.Report)
	}
	if html.Report.SourceFormat != "html" {
		t.Fatalf("unexpected source format: %#v", html.Report)
	}
	lossy, err := FromHTML("<p onclick=\"x\">text</p>")
	if err != nil || len(lossy.Report.Diagnostics) != 1 || lossy.Report.Diagnostics[0].Fidelity != "dropped" {
		t.Fatalf("HTML loss should be classified: %#v, %v", lossy, err)
	}

	markdown, err := FromMarkdown("*em* and **strong**")
	if err != nil {
		t.Fatal(err)
	}
	if markdown.Value != "/em/ and *strong*\n" || markdown.Report.SourceFormat != "markdown" {
		t.Fatalf("unexpected Markdown migration: %#v", markdown)
	}
	if len(markdown.Report.Diagnostics) != 1 || markdown.Report.Diagnostics[0].Code != "fidelity-unverified" || markdown.Report.Diagnostics[0].Fidelity != "dropped" || markdown.Report.Diagnostics[0].Confidence != "fallback" {
		t.Fatalf("unexpected diagnostics: %#v", markdown.Report.Diagnostics)
	}
	// The engine verifies a plain-text document rather than giving up on it, so
	// this is exactly the reading the hard-coded placeholder used to hide.
	plain, err := FromMarkdown("plain text\r\n\r\n")
	if err != nil || len(plain.Report.Diagnostics) != 1 ||
		plain.Report.Diagnostics[0].Code != "literal-text-verified" ||
		plain.Report.Diagnostics[0].Fidelity != "preserved" ||
		plain.Report.Diagnostics[0].Confidence != "exact" {
		t.Fatalf("Markdown report should be the engine's own: %#v, %v", plain, err)
	}
}

func TestHTMLDiagnosticClassification(t *testing.T) {
	tests := map[string][2]string{
		"element-dropped":       {"dropped", "exact"},
		"attribute-dropped":     {"dropped", "exact"},
		"structure-unspellable": {"dropped", "exact"},
		"element-unwrapped":     {"degraded", "exact"},
		"style-unmapped":        {"degraded", "exact"},
		"table-degraded":        {"degraded", "exact"},
		"encoding-assumed":      {"degraded", "inferred"},
		"diagnostics-truncated": {"dropped", "fallback"},
		"attribute-preserved":   {"preserved", "exact"},
		"raw-preserved":         {"degraded", "exact"},
		"future-code":           {"dropped", "fallback"},
	}
	for code, want := range tests {
		fidelity, confidence := classifyHTMLDiagnostic(code)
		if fidelity != want[0] || confidence != want[1] {
			t.Errorf("%s: got %s/%s, want %s/%s", code, fidelity, confidence, want[0], want[1])
		}
	}
}

// TestHTMLImportModeIsSelectable holds the Mode field to the engine's answer.
// Before an import mode could be passed, the argument list said "safe" and the
// field could only ever read back "safe", so nothing here could fail.
func TestHTMLImportModeIsSelectable(t *testing.T) {
	const src = `<p onclick="x()">hi</p>`

	for _, tc := range []struct {
		name string
		opts ImportOptions
		want string
	}{
		{"zero value stays safe", ImportOptions{}, "safe"},
		{"explicit safe", ImportOptions{Mode: ImportSafe}, "safe"},
		{"semantic", ImportOptions{Mode: ImportSemantic}, "semantic"},
	} {
		got, err := FromHTMLOptions(src, tc.opts)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got.Report.Mode != tc.want {
			t.Errorf("%s: Mode = %q, want %q", tc.name, got.Report.Mode, tc.want)
		}
	}

	if got, err := FromHTML(src); err != nil || got.Report.Mode != "safe" {
		t.Errorf("FromHTML must keep the safe default: %q, %v", got.Report.Mode, err)
	}

	// The accepted names are not duplicated here, so an unaccepted one comes
	// back as the engine's own usage error rather than a list that can drift.
	_, err := FromHTMLOptions(src, ImportOptions{Mode: "preserve"})
	if err == nil || !strings.Contains(err.Error(), "unknown mode preserve") {
		t.Errorf("an unaccepted mode must surface the engine's error, got %v", err)
	}
}
