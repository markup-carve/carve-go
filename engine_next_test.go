package carve

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecisionTraceIsJSONLines(t *testing.T) {
	out, err := DecisionTrace("# Title\n\n- one\n- two\n")
	if err != nil {
		t.Fatalf("DecisionTrace: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("want a header and records, got %q", out)
	}
	for i, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("line %d is not JSON: %q", i+1, line)
		}
	}
}

func TestLintCaptionHints(t *testing.T) {
	source := "![chart](chart.png)\n\nFigure 1: Sales by region\n"
	count := func(opts LintOptions) int {
		findings, err := LintWithOptions(source, opts)
		if err != nil {
			t.Fatalf("LintWithOptions(%+v): %v", opts, err)
		}
		n := 0
		for _, f := range findings {
			if f.Rule == "caption-looking-paragraph" {
				n++
			}
		}
		return n
	}
	if got := count(LintOptions{}); got != 0 {
		t.Errorf("caption-looking-paragraph reported without CaptionHints: %d", got)
	}
	if got := count(LintOptions{CaptionHints: true}); got != 1 {
		t.Errorf("caption-looking-paragraph with CaptionHints: got %d, want 1", got)
	}
}

func TestEngineWritesThematicBreakAsStars(t *testing.T) {
	out, err := ToCarve("one\n\n---\n\ntwo\n")
	if err != nil {
		t.Fatalf("ToCarve: %v", err)
	}
	if !strings.Contains(out, "\n***\n") {
		t.Errorf("thematic break not written as ***: %q", out)
	}
}

func TestEngineReadsBracedCellAlignment(t *testing.T) {
	out, err := ToHTML("|= Item |={>} Price |\n| Tea |{>} 3 |\n")
	if err != nil {
		t.Fatalf("ToHTML: %v", err)
	}
	if strings.Count(out, `style="text-align: right;"`) != 2 {
		t.Errorf("braced right alignment lost: %q", out)
	}
}

func TestMarkdownImportReadsGitHubAlerts(t *testing.T) {
	res, err := FromMarkdown("> [!NOTE]\n> Read this.\n")
	if err != nil {
		t.Fatalf("FromMarkdown: %v", err)
	}
	if !strings.Contains(res.Value, "::: note") {
		t.Errorf("GitHub alert not imported as an admonition: %q", res.Value)
	}
}
