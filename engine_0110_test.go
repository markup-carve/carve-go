package carve

import (
	"strings"
	"testing"
)

func TestRust0110EngineFeatures(t *testing.T) {
	for _, source := range []string{"[x]()", "![alt]()"} {
		out, err := ToHTML(source)
		if err != nil {
			t.Fatalf("ToHTML(%q): %v", source, err)
		}
		if !strings.Contains(out, `=""`) {
			t.Errorf("empty destination lost for %q: %q", source, out)
		}
	}

	for _, table := range []string{
		"{table}\n```csv\nA,B\n1,2\n```",
		"{table}\n```tsv\nA\tB\n1\t2\n```",
	} {
		out, err := ToHTML(table)
		if err != nil {
			t.Fatalf("ToHTML table: %v", err)
		}
		if !strings.Contains(out, "<table>") {
			t.Errorf("table rendered without table element: %q", out)
		}
	}

	out, err := ToPlainText("- first\n\n  continuation\n")
	if err != nil {
		t.Fatal(err)
	}
	if out != "- first\n\n  continuation\n" {
		t.Errorf("list continuation lost its column: %q", out)
	}
}
