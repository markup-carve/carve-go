package carve

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The spec's shared table-alignment rows (markup-carve/carve#3333), read from
// the spec checkout the corpus job makes.
func TestAlignTablesMatchesTheSpecFixtures(t *testing.T) {
	dir := os.Getenv("CARVE_SPEC_CORPUS")
	if dir == "" {
		t.Skip("CARVE_SPEC_CORPUS not set; see .github/workflows/ci.yml for the corpus job")
	}
	data, err := os.ReadFile(filepath.Join(dir, "..", "..", "resources", "spec", "table-alignment-fixtures.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Name, Canonical, Aligned string }
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 13 {
		t.Fatalf("%d alignment fixtures, want at least 13", len(cases))
	}
	aligned := Options{AlignTables: true}
	for _, c := range cases {
		check := func(label, source string, opts Options, want string) {
			t.Helper()
			got, err := ToCarveOptions(source, opts)
			if err != nil {
				t.Fatalf("%s %s: %v", c.Name, label, err)
			}
			if got != want {
				t.Errorf("%s %s:\n got %q\nwant %q", c.Name, label, got, want)
			}
		}
		check("aligned", c.Canonical, aligned, c.Aligned)
		check("fixed point", c.Aligned, aligned, c.Aligned)
		check("default", c.Aligned, Options{}, c.Canonical)
		viaRender, err := Render(c.Canonical, OutputCarve, aligned)
		if err != nil || viaRender != c.Aligned {
			t.Errorf("%s Render(OutputCarve): %q, %v", c.Name, viaRender, err)
		}
		a, errA := ToHTML(c.Aligned)
		b, errB := ToHTML(c.Canonical)
		if errA != nil || errB != nil || a != b {
			t.Errorf("%s HTML differs between aligned and canonical", c.Name)
		}
	}
	t.Logf("table alignment: %d fixtures byte-identical", len(cases))
}

func TestAlignTablesRefusesWhatItCannotHonor(t *testing.T) {
	if _, err := Render("|a|", OutputHTML, Options{AlignTables: true}); err == nil {
		t.Error("AlignTables on HTML was accepted")
	}
	if _, err := ToCarveOptions("|a|", Options{AlignTables: true, Profile: "comment"}); err == nil {
		t.Error("AlignTables with Profile was accepted")
	}
	root := t.TempDir()
	doc := filepath.Join(root, "main.crv")
	write(t, doc, "|a|\n")
	if _, err := RenderWithIncludes("|a|\n", OutputCarve, Options{AlignTables: true}, Include{Root: root, SourcePath: doc}); err == nil {
		t.Error("AlignTables with includes was accepted")
	}
}

const regionFile = "intro\n" +
	"%% #region setup\n" +
	"a\n" +
	"b\n" +
	"%% #endregion\n" +
	"%% #region empty\n" +
	"%% #endregion\n" +
	"// #region snippet\n" +
	"x\n" +
	"// #endregion\n" +
	"/* #region block */\n" +
	"y\n" +
	"/* #endregion */\n"

// The same table carve-wasm's tests/align-tables-regions.mjs runs through
// carve-rs compiled to wasm; nil means include-region-invalid.
var regionCases = []struct {
	source, name string
	mode         RegionMode
	want         *IncludeRegion
}{
	{regionFile, "setup", RegionCarve, &IncludeRegion{3, 4}},
	{regionFile, "setup", RegionCode, &IncludeRegion{3, 4}},
	{regionFile, "empty", RegionCarve, &IncludeRegion{7, 6}},
	{regionFile, "snippet", RegionCode, &IncludeRegion{9, 9}},
	{regionFile, "block", RegionCode, &IncludeRegion{12, 12}},
	{regionFile, "snippet", RegionCarve, nil},
	{regionFile, "missing", RegionCode, nil},
	{"%% #region a\nx\n", "a", RegionCarve, nil},
	{"%% #region a\n%% #endregion b\n", "a", RegionCarve, nil},
	{"%% #region a\n%% #endregion\n%% #region a\n%% #endregion\n", "a", RegionCarve, nil},
	{"%% #region a\r\nx\r\n%% #endregion a\r\n", "a", RegionCarve, &IncludeRegion{2, 2}},
	{"/* #region a\nx\n/* #endregion */\n", "a", RegionCode, nil},
	{"#region a\nx\n#endregion\n", "a", RegionCode, &IncludeRegion{2, 2}},
	{"%% #regional a\n%% #region a\nx\n%% #endregion\n", "a", RegionCarve, &IncludeRegion{3, 3}},
	{"%% #region 1a\n%% #endregion\n%% #region a\n%% #endregion\n", "a", RegionCarve, nil},
	{"-- #region q\nSELECT 1;\n-- #endregion q\n", "q", RegionCode, &IncludeRegion{2, 2}},
	{"<!-- #region h -->\n<p>\n<!-- #endregion -->\n", "h", RegionCode, &IncludeRegion{2, 2}},
	{"%%% #region a\nx\n%%% #endregion\n", "a", RegionCarve, nil},
	{"  %%\t#region a\nx\n%% #endregion a  \n", "a", RegionCarve, &IncludeRegion{2, 2}},
}

func TestSelectIncludeRegion(t *testing.T) {
	for _, c := range regionCases {
		got, err := SelectIncludeRegion(c.source, c.name, c.mode)
		if c.want == nil {
			var regionErr *IncludeRegionError
			if !errors.As(err, &regionErr) || regionErr.Rule() != "include-region-invalid" {
				t.Errorf("%q %q: got %+v, %v; want include-region-invalid", c.source, c.name, got, err)
			}
			continue
		}
		if err != nil || got != *c.want {
			t.Errorf("%q %q: got %+v, %v; want %+v", c.source, c.name, got, err, *c.want)
		}
	}
}

// The port against the embedded engine: `@region:name` must cut exactly the
// lines `@lines:start-end` cuts, and fail exactly when the engine warns.
func TestSelectIncludeRegionAgreesWithTheEngine(t *testing.T) {
	root := t.TempDir()
	doc := filepath.Join(root, "main.crv")
	write(t, doc, "\n")
	for i, c := range regionCases {
		file := fmt.Sprintf("f%d.txt", i)
		write(t, filepath.Join(root, file), c.source)
		code := ""
		if c.mode == RegionCode {
			code = " @code"
		}
		expand := func(selector string) IncludeResult {
			t.Helper()
			res, err := RenderWithIncludes("{{ "+file+" "+selector+code+" }}\n", OutputHTML, Options{}, Include{Root: root, SourcePath: doc})
			if err != nil {
				t.Fatalf("case %d: %v", i, err)
			}
			return res
		}
		byRegion := expand("@region:" + c.name)
		engineInvalid := strings.Contains(strings.Join(rules(byRegion), ","), "include-region-invalid")
		got, err := SelectIncludeRegion(c.source, c.name, c.mode)
		if engineInvalid != (err != nil) {
			t.Errorf("case %d: engine invalid=%v, port %+v, %v", i, engineInvalid, got, err)
			continue
		}
		if err != nil || got.End < got.Start {
			continue
		}
		byLines := expand(fmt.Sprintf("@lines:%d-%d", got.Start, got.End))
		if byRegion.Output != byLines.Output {
			t.Errorf("case %d: region cut %q, lines %d-%d cut %q", i, byRegion.Output, got.Start, got.End, byLines.Output)
		}
	}
}
