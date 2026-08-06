package carve

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The spec corpus, run through ParseAST rather than ToHTML.
//
// TestSpecCorpus compares rendered HTML byte for byte, and that was the only
// corpus-driven check here. It cannot see an AST-only change: a node that
// renders nothing renders nothing in both engines. carve-rb sat 44 commits
// behind on an engine that had lost a whole node type and every corpus pair
// still matched (markup-carve/carve-rb#46, markup-carve/carve-py#24).
//
// Two checks, because they answer different questions:
//
//	types   is every TYPE still produced? An engine that drops one drops it from
//	        all 664 documents at once, so the whole corpus answers with one fact
//	        and there is no per-document reasoning.
//	fields  do the FIELD NAMES still match resources/ast-schema.json? Catches a
//	        rename, which the type check cannot see - the type is still
//	        produced, under a different property.
//
// WHY NOT A PER-DOCUMENT ASSERTION. The obvious check - a document with a
// definition line must produce a link_reference_definition - does not survive
// contact with the corpus: 64 documents have that source shape and 36
// legitimately produce no such node, because "[^f]: note" is the same shape and
// because several documents exist precisely to pin that a definition-shaped
// line is NOT a definition. Modelling that needs an allowlist of exactly the
// documents whose rules the check cannot model.
//
// THE FIELD CHECK IS NOT JSON SCHEMA VALIDATION. It reads two keywords -
// additionalProperties:false and required - and ignores types, enums, formats
// and conditionals. Those two are what a drifted engine actually trips.

// Recorded by walking every corpus document through this module. An explicit
// list rather than a count: a count says "something went missing" and this says
// which.
var expectedNodeTypes = strings.Fields(`
	abbreviation abbreviation_def admonition autolink block_quote caption_number
	code code_block comment critic_comment definition_description definition_list
	definition_term delete div document emphasis escaped_text figure footnote
	footnote_ref frontmatter hard_break heading heading_ref highlight image
	inline_extension inline_footnote insert line_block link
	link_reference_definition list list_item literal_inline math mention
	paragraph raw_block raw_inline smart_punctuation soft_break span strike
	strong subscript substitution superscript symbol table table_cell table_row
	tag text thematic_break underline
`)

func corpusDocuments(t *testing.T) []string {
	t.Helper()
	dir := os.Getenv("CARVE_SPEC_CORPUS")
	if dir == "" {
		t.Skip("CARVE_SPEC_CORPUS not set; see .github/workflows/ci.yml for the corpus job")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var paths []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".crv") {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(paths)
	// Without this an empty or mistyped directory yields no documents, every
	// assertion below is vacuous, and the run reads as clean - the same failure
	// shape these tests exist to remove.
	if len(paths) < 400 {
		t.Fatalf("only %d corpus documents under %s; the corpus has ~650, so this is a wiring problem, not a clean run", len(paths), dir)
	}
	return paths
}

func walkNodes(node any, visit func(map[string]any)) {
	switch value := node.(type) {
	case map[string]any:
		if _, ok := value["type"].(string); ok {
			visit(value)
		}
		for _, child := range value {
			walkNodes(child, visit)
		}
	case []any:
		for _, child := range value {
			walkNodes(child, visit)
		}
	}
}

func corpusTrees(t *testing.T) []any {
	t.Helper()
	var trees []any
	for _, path := range corpusDocuments(t) {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		raw, err := ParseAST(string(source))
		if err != nil {
			t.Fatalf("ParseAST(%s): %v", filepath.Base(path), err)
		}
		var tree any
		if err := json.Unmarshal(raw, &tree); err != nil {
			t.Fatalf("decoding the tree for %s: %v", filepath.Base(path), err)
		}
		trees = append(trees, tree)
	}
	return trees
}

func producedNodeTypes(t *testing.T) map[string]bool {
	t.Helper()
	seen := map[string]bool{}
	for _, tree := range corpusTrees(t) {
		walkNodes(tree, func(node map[string]any) {
			seen[node["type"].(string)] = true
		})
	}
	return seen
}

func TestEveryRecordedNodeTypeStillReachesTheTree(t *testing.T) {
	produced := producedNodeTypes(t)
	var missing []string
	for _, want := range expectedNodeTypes {
		if !produced[want] {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%d node type(s) the corpus used to produce are gone: %s. The embedded wasm is "+
			"probably behind a change that renamed or removed them; rebuild it with ./build-wasm.sh "+
			"and commit internal/wasm/REV. If a type was removed from the language on purpose, delete "+
			"it from expectedNodeTypes in the same commit.", len(missing), strings.Join(missing, ", "))
	}

	// The ablation. Without it the loop above passes identically whether the
	// corpus was walked or the walk quietly found nothing.
	if produced["a_type_no_engine_emits"] {
		t.Fatal("the type sweep is not reading the trees")
	}
	if len(produced) < len(expectedNodeTypes) {
		t.Fatalf("the sweep found %d types, fewer than the %d recorded", len(produced), len(expectedNodeTypes))
	}
}

func schemaDefinitions(t *testing.T) map[string]any {
	t.Helper()
	// The schema ships beside the corpus in the spec checkout, so CI needs no
	// second variable.
	path := filepath.Join(os.Getenv("CARVE_SPEC_CORPUS"), "..", "..", "resources", "ast-schema.json")
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no schema at %s: %v", path, err)
	}
	var schema struct {
		Defs map[string]any `json:"$defs"`
	}
	if err := json.Unmarshal(blob, &schema); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	if len(schema.Defs) < 40 {
		t.Fatalf("the schema has only %d type definitions, which is too few to be the spec's", len(schema.Defs))
	}
	return schema.Defs
}

func schemaFindings(t *testing.T, defs map[string]any, trees []any) map[string]int {
	t.Helper()
	findings := map[string]int{}
	for _, tree := range trees {
		walkNodes(tree, func(node map[string]any) {
			nodeType := node["type"].(string)
			raw, ok := defs[nodeType]
			if !ok {
				findings[nodeType+": no $defs entry in the schema"]++
				return
			}
			definition, ok := raw.(map[string]any)
			if !ok {
				return
			}
			properties, ok := definition["properties"].(map[string]any)
			if !ok {
				return
			}
			if closed, ok := definition["additionalProperties"].(bool); ok && !closed {
				for key := range node {
					if _, named := properties[key]; !named {
						findings[nodeType+"."+key+": not a property the schema names"]++
					}
				}
			}
			required, _ := definition["required"].([]any)
			for _, name := range required {
				key, ok := name.(string)
				if !ok {
					continue
				}
				if _, present := node[key]; !present {
					findings[nodeType+": required property "+key+" is missing"]++
				}
			}
		})
	}
	return findings
}

func TestEveryNodeUsesFieldNamesTheSchemaNames(t *testing.T) {
	trees := corpusTrees(t)
	defs := schemaDefinitions(t)

	if findings := schemaFindings(t, defs, trees); len(findings) > 0 {
		var lines []string
		for label, count := range findings {
			lines = append(lines, strings.TrimSpace(label))
			_ = count
		}
		sort.Strings(lines)
		if len(lines) > 10 {
			lines = lines[:10]
		}
		t.Fatalf("nodes do not match the schema's field names: %s. The embedded wasm is probably "+
			"behind a rename; rebuild it with ./build-wasm.sh.", strings.Join(lines, "; "))
	}

	// The ablation, in the test rather than in a commit message: rename a
	// property the corpus certainly produces and confirm the sweep reports it.
	// Without this the assertion above passes identically whether the schema is
	// being read or quietly ignored.
	textDef, ok := defs["text"].(map[string]any)
	if !ok {
		t.Fatal(`the schema has no "text" definition to mutate`)
	}
	properties, ok := textDef["properties"].(map[string]any)
	if !ok {
		t.Fatal(`the "text" definition has no properties to mutate`)
	}
	mutatedProperties := map[string]any{}
	for key, value := range properties {
		if key != "value" {
			mutatedProperties[key] = value
		}
	}
	mutatedText := map[string]any{}
	for key, value := range textDef {
		mutatedText[key] = value
	}
	mutatedText["properties"] = mutatedProperties
	mutatedDefs := map[string]any{}
	for key, value := range defs {
		mutatedDefs[key] = value
	}
	mutatedDefs["text"] = mutatedText

	if _, reported := schemaFindings(t, mutatedDefs, trees)["text.value: not a property the schema names"]; !reported {
		t.Fatal("the schema sweep is not reading the schema")
	}
}
