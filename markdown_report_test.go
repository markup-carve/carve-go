package carve

import (
	"testing"
)

// An ordered task item is a loss the engine classifies exactly, so it is the
// probe for whether the engine's report reaches the caller at all.
const orderedTaskSource = "1. [ ] ordered task\n"

func TestFromMarkdownReportsTheEnginesDiagnostics(t *testing.T) {
	res, err := FromMarkdown(orderedTaskSource)
	if err != nil {
		t.Fatalf("FromMarkdown: %v", err)
	}
	var found *MigrationDiagnostic
	for i := range res.Report.Diagnostics {
		if res.Report.Diagnostics[i].Code == "structure-unspellable" {
			found = &res.Report.Diagnostics[i]
		}
	}
	if found == nil {
		t.Fatalf("the engine's structure-unspellable diagnostic never reached the caller; got %+v",
			res.Report.Diagnostics)
	}
	if found.Fidelity != "dropped" {
		t.Errorf("fidelity = %q, want dropped", found.Fidelity)
	}
	if found.Confidence != "exact" {
		t.Errorf("confidence = %q, want exact", found.Confidence)
	}
	if found.Severity != "warning" {
		t.Errorf("severity = %q, want warning", found.Severity)
	}
	if found.Message == "" {
		t.Error("the diagnostic carries no message")
	}
}

func TestFromMarkdownKeepsTheReportEnvelope(t *testing.T) {
	res, err := FromMarkdown(orderedTaskSource)
	if err != nil {
		t.Fatalf("FromMarkdown: %v", err)
	}
	if res.Report.SchemaVersion != 2 {
		t.Errorf("schemaVersion = %d, want 2", res.Report.SchemaVersion)
	}
	if res.Report.SourceFormat != "markdown" {
		t.Errorf("sourceFormat = %q, want markdown", res.Report.SourceFormat)
	}
	if res.Value != orderedTaskSource {
		t.Errorf("value = %q, want %q", res.Value, orderedTaskSource)
	}
}

func TestFromMarkdownUsesConstructEvidence(t *testing.T) {
	res, err := FromMarkdown("# hi\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Report.Diagnostics) == 0 {
		t.Fatal("missing construct evidence")
	}
	for _, d := range res.Report.Diagnostics {
		if d.Code == "fidelity-unverified" || d.Fidelity != "preserved" || d.Confidence != "exact" {
			t.Fatalf("unexpected heading assessment: %+v", d)
		}
	}
}

func TestEmptyMarkdownReportIsPreserved(t *testing.T) {
	report, err := decodeMarkdownReport(`{"schemaVersion":2,"sourceFormat":"markdown","diagnostics":[]}`)
	if err != nil {
		t.Fatal(err)
	}
	if report.Diagnostics == nil || len(report.Diagnostics) != 0 {
		t.Fatalf("an empty engine report gained diagnostics: %+v", report)
	}
}

func TestAbsentMarkdownReportIsUnverified(t *testing.T) {
	report, err := decodeMarkdownReport("")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Diagnostics) != 1 || report.Diagnostics[0].Code != "fidelity-unverified" {
		t.Fatalf("missing report was treated as verified: %+v", report)
	}
}

func TestInvalidMarkdownReportIsRefused(t *testing.T) {
	for _, source := range []string{
		"not JSON", `{}`, `null`,
		`{"schemaVersion":2,"sourceFormat":"html","diagnostics":[]}`,
		`{"schemaVersion":3,"sourceFormat":"markdown","diagnostics":[]}`,
		`{"schemaVersion":2,"sourceFormat":"markdown","diagnostics":null}`,
	} {
		if _, err := decodeMarkdownReport(source); err == nil {
			t.Errorf("accepted invalid report %s", source)
		}
	}
}
