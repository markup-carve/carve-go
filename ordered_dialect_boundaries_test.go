package carve

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestOrderedDialectBoundaries(t *testing.T) {
	data, err := os.ReadFile("testdata/ordered-dialect-boundaries.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name      string `json:"name"`
		Source    string `json:"source"`
		HTML      string `json:"html"`
		InputHTML string `json:"inputHtml"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 17 {
		t.Fatalf("got %d boundary cases, want 17", len(cases))
	}
	for _, row := range cases {
		t.Run(row.Name, func(t *testing.T) {
			html, err := ToHTML(row.Source)
			if err != nil || strings.TrimRight(html, "\n") != row.HTML {
				t.Fatalf("render: %q, %v; want %q", html, err, row.HTML)
			}
			formatted, report, err := RenderChecked(row.Source, OutputCarve, CheckedOptions{Strict: true})
			if err != nil || formatted != row.Source || report.TotalLosses != 0 {
				t.Fatalf("format: %q, %v; want %q", formatted, err, row.Source)
			}
			imported, err := FromHTML(row.InputHTML)
			if err != nil {
				t.Fatal(err)
			}
			if imported.Value != row.Source || len(imported.Report.Diagnostics) != 0 {
				t.Fatalf("import: %#v; want lossless source %q", imported, row.Source)
			}
			html, err = ToHTML(imported.Value)
			if err != nil || strings.TrimRight(html, "\n") != row.HTML {
				t.Fatalf("import render: %q, %v; want %q", html, err, row.HTML)
			}
		})
	}
}

func TestArticleRetainsRawPayloadAsCode(t *testing.T) {
	html, err := ToHTMLOptions("``` =html\n<b>x</b>\n```", Options{Profile: "article"})
	want := "<pre><code class=\"language-html\">&lt;b&gt;x&lt;/b&gt;\n</code></pre>"
	if err != nil || strings.TrimRight(html, "\n") != want {
		t.Fatalf("article: %q, %v; want %q", html, err, want)
	}
}

func TestMarkdownRetainsAuthoredOrderedDelimiters(t *testing.T) {
	data, err := os.ReadFile("testdata/markdown-ordered-delimiters.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name     string `json:"name"`
		Markdown string `json:"markdown"`
		Source   string `json:"source"`
		HTML     string `json:"html"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 3 {
		t.Fatalf("got %d Markdown cases, want 3", len(cases))
	}
	for _, row := range cases {
		t.Run(row.Name, func(t *testing.T) {
			imported, err := FromMarkdown(row.Markdown)
			if err != nil {
				t.Fatal(err)
			}
			if imported.Value != row.Source {
				t.Fatalf("import: %q; want %q", imported.Value, row.Source)
			}
			html, err := ToHTML(imported.Value)
			if err != nil || strings.TrimRight(html, "\n") != row.HTML {
				t.Fatalf("render: %q, %v; want %q", html, err, row.HTML)
			}
			if len(imported.Report.Diagnostics) == 0 {
				t.Fatal("missing fidelity assessment")
			}
			for _, diagnostic := range imported.Report.Diagnostics {
				if diagnostic.Fidelity != "preserved" {
					t.Fatalf("unexpected loss: %#v", diagnostic)
				}
			}
		})
	}
}
