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

	markdown, err := FromMarkdown("*em* and **strong**")
	if err != nil {
		t.Fatal(err)
	}
	if markdown.Value != "/em/ and *strong*\n" || markdown.Report.SourceFormat != "markdown" {
		t.Fatalf("unexpected Markdown migration: %#v", markdown)
	}
}
