package carve

import "testing"

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
	plain, err := FromMarkdown("plain text\r\n\r\n")
	if err != nil || len(plain.Report.Diagnostics) != 1 || plain.Report.Diagnostics[0].Code != "fidelity-unverified" {
		t.Fatalf("Markdown fidelity limitation should be explicit: %#v, %v", plain, err)
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
		"structure-split":       {"degraded", "exact"},
		"encoding-assumed":      {"degraded", "inferred"},
		"diagnostics-truncated": {"degraded", "fallback"},
		"attribute-preserved":   {"preserved", "exact"},
		"raw-preserved":         {"preserved", "exact"},
		"future-code":           {"degraded", "fallback"},
	}
	for code, want := range tests {
		fidelity, confidence := classifyHTMLDiagnostic(code)
		if fidelity != want[0] || confidence != want[1] {
			t.Errorf("%s: got %s/%s, want %s/%s", code, fidelity, confidence, want[0], want[1])
		}
	}
}
