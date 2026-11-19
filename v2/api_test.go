package carve

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestRenderAndPolicy(t *testing.T) {
	html, err := ToHTML("Hi *there*")
	if err != nil {
		t.Fatal(err)
	}
	if html != "<p>Hi <strong>there</strong></p>" {
		t.Fatalf("HTML = %q", html)
	}
	safe, err := ToHTML("```=html\n<b>raw</b>\n```")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := ToHTMLOptions("```=html\n<b>raw</b>\n```", Options{RawHTML: true})
	if err != nil {
		t.Fatal(err)
	}
	if safe == raw || !strings.Contains(raw, "<b>raw</b>") {
		t.Fatalf("rawHtml default/opt-in: %q / %q", safe, raw)
	}
	_, err = ToHTMLOptions("[x](javascript:alert(1))", Options{URLPolicy: &URLPolicy{Deny: []string{"javascript"}}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentCalls(t *testing.T) {
	var group sync.WaitGroup
	for i := 0; i < 12; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			html, err := ToHTML("Hi *there*")
			if err != nil || html != "<p>Hi <strong>there</strong></p>" {
				t.Errorf("concurrent render: %q (%v)", html, err)
			}
		}()
	}
	group.Wait()
}

func TestParseAndDiagnostics(t *testing.T) {
	parsed, err := Parse(context.Background(), "# Title", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(parsed.Tree) {
		t.Fatalf("invalid tree: %s", parsed.Tree)
	}
	var root struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(parsed.Tree, &root); err != nil || root.Type == "" {
		t.Fatalf("tree root: %s (%v)", parsed.Tree, err)
	}
	if _, err := Parse(context.Background(), "Hi", Options{RawHTML: true}); err == nil {
		t.Fatal("parse accepted an option it does not read")
	}
	withRecord, err := RenderWithDiagnostics(context.Background(), "Hi", Options{})
	if err != nil || withRecord.HTML != "<p>Hi</p>" {
		t.Fatalf("render record: %+v (%v)", withRecord, err)
	}
	fromTree, err := RenderTree(context.Background(), parsed.Tree, Options{})
	if err != nil || !strings.Contains(fromTree.HTML, "Title") {
		t.Fatalf("render tree: %+v (%v)", fromTree, err)
	}
	encoded, err := EncodeTree(context.Background(), parsed.Tree)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeTree(context.Background(), encoded)
	if err != nil || !json.Valid(decoded) {
		t.Fatalf("decode tree: %s (%v)", decoded, err)
	}
	if err := ValidateTree(context.Background(), decoded); err != nil {
		t.Fatal(err)
	}
}

func TestWriterRegistryHTMLAndRawCall(t *testing.T) {
	written, err := Write(context.Background(), "Hi *there*", "html", Options{})
	if err != nil || written != "<p>Hi <strong>there</strong></p>" {
		t.Fatalf("HTML writer: %q (%v)", written, err)
	}
	var result struct {
		HTML string `json:"html"`
	}
	err = Call(context.Background(), map[string]any{"call": "render", "source": "Hi"}, &result)
	if err != nil || result.HTML != "<p>Hi</p>" {
		t.Fatalf("raw call: %+v (%v)", result, err)
	}
	tree, err := ParseAST("Hi")
	if err != nil {
		t.Fatal(err)
	}
	writtenTree, err := WriteTree(context.Background(), tree, "html", WriteOptions{})
	if err != nil || writtenTree.Output != "<p>Hi</p>" {
		t.Fatalf("write tree: %+v (%v)", writtenTree, err)
	}
	heading, err := Write(context.Background(), "# Title", "html", Options{HeadingIDs: "off"})
	if err != nil || strings.Contains(heading, "id=") {
		t.Fatalf("write ignored heading IDs option: %q (%v)", heading, err)
	}
	raw, err := Write(context.Background(), "```=html\n<b>raw</b>\n```", "html", Options{RawHTML: true})
	if err != nil || raw != "<b>raw</b>" {
		t.Fatalf("write ignored raw HTML option: %q (%v)", raw, err)
	}
}

func TestLintAndValidation(t *testing.T) {
	entries, err := Lint(context.Background(), "# Title", LintOptions{})
	if err != nil || entries == nil {
		t.Fatalf("lint: %#v (%v)", entries, err)
	}
	_, err = Lint(context.Background(), "Hi", LintOptions{Rules: map[string]string{"not-a-rule": "on"}})
	var engineErr *EngineError
	if !errors.As(err, &engineErr) || engineErr.Category != "usage" {
		t.Fatalf("rule error: %v", err)
	}
	_, err = ToHTMLOptions("Hi", Options{Extensions: []string{"spoiler"}})
	if !errors.As(err, &engineErr) || engineErr.Name != "CarveOptionError" {
		t.Fatalf("unknown extension: %v", err)
	}
}

func TestStandardExtensions(t *testing.T) {
	html, err := ToHTMLOptions(":wiki[Page]", Options{Extensions: []string{"carve.wiki"}})
	if err != nil || html != "<p>Page</p>" {
		t.Fatalf("carve.wiki: %q (%v)", html, err)
	}
	source := "::: tabs\n::: tab {title=A}\nx\n:::\n:::\n"
	html, err = ToHTMLOptions(source, Options{Extensions: []string{"carve.tabs"}, Renderers: map[string]string{"carve.tabs": "standard"}})
	if err != nil || !strings.Contains(html, `class="tabs"`) {
		t.Fatalf("carve.tabs: %q (%v)", html, err)
	}
	_, err = ToHTMLOptions(source, Options{Extensions: []string{"carve.tabs"}, Renderers: map[string]string{"carve.tabs": "host"}})
	var engineErr *EngineError
	if !errors.As(err, &engineErr) || !strings.Contains(engineErr.Message, "standard") {
		t.Fatalf("host renderer: %v", err)
	}
	for _, name := range StandardExtensions {
		if _, err := ToHTMLOptions("Hi", Options{Extensions: []string{name}}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestIncludesAndDependencies(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "part.crv"), []byte("*inside*"), 0600); err != nil {
		t.Fatal(err)
	}
	inc := Include{Root: root, SourcePath: filepath.Join(root, "main.crv")}
	result, err := RenderWithIncludes("{{ part.crv }}", OutputHTML, Options{}, inc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "<strong>inside</strong>") {
		t.Fatalf("include HTML: %q", result.Output)
	}
	if len(result.Dependencies) != 1 || !result.Dependencies[0].Read || result.Dependencies[0].HostPath != filepath.Join(root, "part.crv") {
		t.Fatalf("dependencies: %#v", result.Dependencies)
	}
	missing, err := RenderWithIncludes("{{ missing.crv }}", OutputHTML, Options{}, inc)
	if err != nil {
		t.Fatal(err)
	}
	codes := []string{}
	for _, warning := range missing.Warnings {
		codes = append(codes, warning.Rule)
	}
	if !slices.Contains(codes, "include-not-found") {
		t.Fatalf("missing include warnings: %v", codes)
	}
	outside, err := RenderWithIncludes("{{ ../outside.crv }}", OutputHTML, Options{}, inc)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(outside.Diagnostics, func(d Diagnostic) bool { return d.Code == "include-outside-root" }) {
		t.Fatalf("outside include: %#v", outside.Diagnostics)
	}
}

func TestIncludeLint(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "part.crv"), []byte("Text\n"), 0600); err != nil {
		t.Fatal(err)
	}
	inc := Include{Root: root, SourcePath: filepath.Join(root, "main.crv")}
	entries, err := Lint(context.Background(), "{{ part.crv }}\n\n{{ missing.crv }}\n", LintOptions{Include: &inc})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(entries, func(d Diagnostic) bool { return d.Code == "include-not-found" }) {
		t.Fatalf("include lint: %#v", entries)
	}
}

func TestIncludeWithExtensions(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "part.crv"), []byte(":wiki[Page]"), 0600); err != nil {
		t.Fatal(err)
	}
	inc := Include{Root: root, SourcePath: filepath.Join(root, "main.crv")}
	result, err := RenderWithIncludes("{{ part.crv }}", OutputHTML, Options{Extensions: []string{"carve.wiki"}}, inc)
	if err == nil && result.Output == "<p>{{ part.crv }}</p>" {
		t.Skip("carve-rs#4352: the extensions option turns include expansion off")
	}
	if err != nil || result.Output != "<p>Page</p>" {
		t.Fatalf("include with extension: %q (%v)", result.Output, err)
	}
}

func TestUnresolvedIncludeLossNamesItsFile(t *testing.T) {
	root := t.TempDir()
	inc := Include{Root: root, SourcePath: filepath.Join(root, "main.crv")}
	result, err := RenderWithIncludes("{{ missing.crv }}", OutputHTML, Options{}, inc)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range result.Diagnostics {
		if d.Code != "include-unresolved" {
			continue
		}
		if d.Message == `Include "" could not be resolved.` {
			t.Skip("carve-rs#4351: include-unresolved names an empty path")
		}
		if d.Message != `Include "missing.crv" could not be resolved.` {
			t.Fatalf("include-unresolved: %q", d.Message)
		}
	}
}

func TestIncludeDependenciesCall(t *testing.T) {
	tree, err := ParseAST("{{ part.crv }}")
	if err != nil {
		t.Fatal(err)
	}
	deps, err := IncludeDependencies(context.Background(), tree, "main.crv")
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 1 || deps[0].Path != "part.crv" || deps[0].Read {
		t.Fatalf("literal include dependencies: %#v", deps)
	}
}

func TestIncludeMountConfinesSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.crv")
	if err := os.WriteFile(secret, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "link.crv")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	opened, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	file, err := (confinedFS{root: opened}).Open("link.crv")
	if err == nil {
		file.Close()
		t.Fatal("include mount read outside root")
	}
}

func TestWriterGaps(t *testing.T) {
	if _, err := ToMarkdown("Hi"); err == nil || !strings.Contains(err.Error(), "writer") {
		t.Fatalf("Markdown writer gap: %v", err)
	}
	if _, err := ToPlainText("Hi"); err == nil || !strings.Contains(err.Error(), "writer") {
		t.Fatalf("plain writer gap: %v", err)
	}
	if _, err := ToANSI("Hi"); err == nil || !strings.Contains(err.Error(), "writer") {
		t.Fatalf("ANSI writer gap: %v", err)
	}
	if _, err := FromMarkdown("Hi"); err == nil {
		t.Fatal("Markdown importer unexpectedly available")
	}
	_, err := WriteTree(context.Background(), json.RawMessage(`{}`), "markdown", WriteOptions{})
	if err == nil || strings.Contains(err.Error(), "writer not available yet") {
		t.Fatalf("invalid tree was hidden as a missing writer: %v", err)
	}
}

// C2-W-035: the canonical writer writes a heading with `=`.
func TestCarveWriter(t *testing.T) {
	out, err := ToCarve("# Hi")
	if err != nil || out != "= Hi\n" {
		t.Fatalf("ToCarve: %q (%v)", out, err)
	}
	aligned, err := Format(context.Background(), "| a | longer |\n| b | c |\n", true)
	if err != nil || aligned != "| a | longer |\n| b | c      |\n" {
		t.Fatalf("Format aligned: %q (%v)", aligned, err)
	}
}

func TestHTMLImporter(t *testing.T) {
	raw, err := FromHTML("<p>Hello <strong>world</strong></p>")
	if err != nil {
		t.Fatal(err)
	}
	var imported struct {
		Source string `json:"source"`
		Tree   struct {
			Type string `json:"type"`
		} `json:"tree"`
	}
	if err := json.Unmarshal(raw, &imported); err != nil || imported.Source != "Hello *world*\n" || imported.Tree.Type != "document" {
		t.Fatalf("FromHTML: %s (%v)", raw, err)
	}
}

func TestMarkdownWriterPending(t *testing.T) {
	t.Skip("markdown writer not available yet in carve-rs 0.2 (carve-rs#4327, carve-rs#4349)")
}
func TestPlainWriterPending(t *testing.T) {
	t.Skip("plain writer not available yet in carve-rs 0.2 (carve-rs#4327, carve-rs#4349)")
}
func TestANSIWriterPending(t *testing.T) {
	t.Skip("ansi writer not available yet in carve-rs 0.2 (carve-rs#4327, carve-rs#4349)")
}

func TestPinnedArtifact(t *testing.T) {
	rev, err := os.ReadFile("internal/wasm/REV")
	if err != nil || len(strings.TrimSpace(string(rev))) != 40 {
		t.Fatalf("engine pin: %q (%v)", rev, err)
	}
	digest, err := os.ReadFile("internal/wasm/carve.wasm.sha256")
	if err != nil || !strings.HasSuffix(strings.TrimSpace(string(digest)), "carve.wasm") {
		t.Fatalf("engine digest: %q (%v)", digest, err)
	}
}
