package carve

import (
	"encoding/json"
	"testing"
)

func TestConformanceForwardsHostDocumentTransforms(t *testing.T) {
	const row = `{
		"input": "a\n",
		"host": {"documentTransforms": [{
			"name": "sample.a",
			"after": "/children/0",
			"insert": {"type": "paragraph", "children": [{"type": "text", "value": "b"}]}
		}]}
	}`
	var entry conformanceEntry
	if err := json.Unmarshal([]byte(row), &entry); err != nil {
		t.Fatal(err)
	}
	got, err := renderConformanceEntry(entry, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if want := "<p>a</p>\n<p>b</p>"; got != want {
		t.Fatalf("host transform HTML = %q, want %q", got, want)
	}
}
