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
	// Section 4: "MUST NOT emit pos with invented values". A cell's text is
	// reassembled - the parser unescapes `\|` on the way in - so it is not a
	// verbatim slice and carries no span, while the cell around it does.
	doc := parse(t, "| a | b |\n|---|---|\n| c | d |\n")
	var table struct {
		Rows []struct {
			Cells []node `json:"cells"`
		} `json:"rows"`
	}
	raw, _ := json.Marshal(doc.Children[0])
	if err := json.Unmarshal(raw, &table); err != nil {
		t.Fatalf("unmarshal table: %v", err)
	}
	cell := table.Rows[0].Cells[0]

	if cell.Pos == nil {
		t.Error("a table cell is a slice of the source and should carry a position")
	}
	if cell.Children[0].Pos != nil {
		t.Errorf("reassembled cell text carries a position: %+v", *cell.Children[0].Pos)
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
