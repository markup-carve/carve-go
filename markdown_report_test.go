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

// A clean document still gets the engine's own fidelity-unverified row, and
// exactly one copy of it: this package used to synthesize its own.
func TestFromMarkdownDoesNotDuplicateFidelityUnverified(t *testing.T) {
	res, err := FromMarkdown("# hi\n")
	if err != nil {
		t.Fatalf("FromMarkdown: %v", err)
	}
	n := 0
	for _, d := range res.Report.Diagnostics {
		if d.Code == "fidelity-unverified" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("fidelity-unverified appears %d times, want 1: %+v", n, res.Report.Diagnostics)
	}
}
