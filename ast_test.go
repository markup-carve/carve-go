package carve_test

import (
	"encoding/json"
	"strings"
	"testing"

	carve "github.com/markup-carve/carve-go"
)

// The PART 12 exchange shape, published by this module.
//
// The tree is what every integration that is not "source to HTML" needs, and
// this module had no way to reach one (#7). It comes from the ENGINE's own
// serializer through the embedded CLI, so it is byte-identical to what every
// other binding over carve-rs publishes rather than a Go-side reimplementation.

type node struct {
	Type          string          `json:"type"`
	Children      []node          `json:"children"`
	Value         string          `json:"value"`
	Label         string          `json:"label"`
	Format        string          `json:"format"`
	Content       string          `json:"content"`
	SrcByteLength *int            `json:"srcByteLength"`
	Pos           *position       `json:"pos"`
	Rows          json.RawMessage `json:"rows"`
}

type position struct {
	StartLine   int `json:"startLine"`
	EndLine     int `json:"endLine"`
	StartColumn int `json:"startColumn"`
	EndColumn   int `json:"endColumn"`
	StartOffset int `json:"startOffset"`
	EndOffset   int `json:"endOffset"`
}

func parse(t *testing.T, source string) node {
	t.Helper()
	raw, err := carve.ParseAST(source)
	if err != nil {
		t.Fatalf("ParseAST: %v", err)
	}
	var doc node
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return doc
}

func TestParseASTRootCarriesExactlyThreeFields(t *testing.T) {
	// PART 12 section 7. Frontmatter and footnote definitions are block nodes in
	// the tree, not root fields.
	raw, err := carve.ParseAST("---\ntitle: T\n---\n\nBody[^a].\n\n[^a]: note\n")
	if err != nil {
		t.Fatalf("ParseAST: %v", err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(root) != 3 {
		t.Fatalf("root has %d fields, want 3: %v", len(root), keys(root))
	}
	for _, want := range []string{"type", "children", "srcByteLength"} {
		if _, ok := root[want]; !ok {
			t.Errorf("root is missing %q (has %v)", want, keys(root))
		}
	}

	doc := parse(t, "---\ntitle: T\n---\n\nBody[^a].\n\n[^a]: note\n")
	var types []string
	for _, child := range doc.Children {
		types = append(types, child.Type)
	}
	want := "frontmatter,paragraph,footnote"
	if got := strings.Join(types, ","); got != want {
		t.Errorf("children = %s, want %s", got, want)
	}
}

func TestParseASTFrontmatterIsRawNotParsed(t *testing.T) {
	// A parsed mapping cannot be serialized back to the bytes the author wrote,
	// so the wire carries the block verbatim plus its format.
	doc := parse(t, "---toml\nx = 1\n---\n\nBody.\n")
	frontmatter := doc.Children[0]

	if frontmatter.Type != "frontmatter" || frontmatter.Format != "toml" {
		t.Fatalf("first child = %+v, want a toml frontmatter node", frontmatter)
	}
	if frontmatter.Content != "x = 1" {
		t.Errorf("content = %q, want %q", frontmatter.Content, "x = 1")
	}
}

func TestParseASTPositionsAreCodepoints(t *testing.T) {
	// PART 12 section 4. The astral character is the point: bytes and UTF-16
	// units agree with codepoints below U+10000, so a wrong unit is unobservable
	// without one. The emoji is 4 bytes and 1 codepoint, so a byte-indexed
	// engine would report column 6 here.
	doc := parse(t, "\U0001F600 *b*\n")
	inlines := doc.Children[0].Children
	strong := inlines[len(inlines)-1]

	if strong.Type != "strong" {
		t.Fatalf("last inline = %q, want strong", strong.Type)
	}
	if strong.Pos == nil {
		t.Fatal("strong carries no position")
	}
	if strong.Pos.StartColumn != 3 || strong.Pos.StartOffset != 2 {
		t.Errorf("pos = %+v, want startColumn 3 / startOffset 2", *strong.Pos)
	}
}

func TestParseASTOmitsASpanItCannotPlace(t *testing.T) {
	// Section 4: "MUST NOT emit pos with invented values". The engine refuses a
	// span only where no source range holds the text.
	//
	// A `+` line continues the cell above it, and the two halves are joined by
	// a MANUFACTURED space: the source has a line break there, so that space
	// selects nothing and carries no position. The halves on either side are
	// verbatim slices and do carry one.
	//
	// This used to assert that a plain cell's text was unplaceable, on the
	// grounds that unescaping `\|` reassembles it. That stopped being true -
	// an escaped pipe now splits into `text` / `escaped_text` / `text`, each a
	// verbatim slice with its own span, so a cell with no escape at all was
	// never reassembled in the first place.
	doc := parse(t, "|= H |\n| a |\n+ b |\n")

	var placed []string
	var unplaced int
	var walk func(n node)
	walk = func(n node) {
		if n.Type == "text" {
			if n.Pos == nil {
				unplaced++
			} else {
				placed = append(placed, n.Value)
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
		// A table keeps its cells under `rows`, not `children`, so the walk has
		// to step through them explicitly or it never reaches a cell's text.
		if len(n.Rows) > 0 {
			var rows []struct {
				Cells []node `json:"cells"`
			}
			if err := json.Unmarshal(n.Rows, &rows); err != nil {
				t.Fatalf("unmarshal rows: %v", err)
			}
			for _, row := range rows {
				for _, cell := range row.Cells {
					walk(cell)
				}
			}
		}
	}
	for _, child := range doc.Children {
		raw, _ := json.Marshal(child)
		var n node
		if err := json.Unmarshal(raw, &n); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		walk(n)
	}

	if unplaced != 1 {
		t.Errorf("unplaced text nodes = %d, want exactly the manufactured joiner", unplaced)
	}
	if len(placed) == 0 {
		t.Error("every verbatim half of a continued cell should carry a position")
	}
}

func TestParseASTRejectsNonJSONFromTheEngine(t *testing.T) {
	// Not reachable with the committed artifact, which is the point: the guard
	// exists so a .wasm predating --json fails loudly instead of handing the
	// caller an empty document. Asserting the happy path keeps the guard from
	// being the only untested branch.
	raw, err := carve.ParseAST("x")
	if err != nil {
		t.Fatalf("ParseAST: %v", err)
	}
	if !json.Valid(raw) {
		t.Fatal("ParseAST returned invalid JSON")
	}
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
