package carve

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestConformance renders every `render` entry of spec/0.2/conformance
// (examples.json and clause-cases.json) that states expected.html and compares
// the HTML byte for byte. CARVE_CONFORMANCE names the conformance directory;
// unset, the test skips. CARVE_CORPUS_MAX_DIVERGENCE is the tolerance.
//
// An entry whose options have no Options field is counted and listed, not
// compared: rendering without the option would count a binding gap as an
// engine divergence.

type conformanceEntry struct {
	ID         string                     `json:"id"`
	Kind       string                     `json:"kind"`
	Input      *string                    `json:"input"`
	InputBytes string                     `json:"inputBytes"`
	Files      json.RawMessage            `json:"files"`
	Host       json.RawMessage            `json:"host"`
	Options    map[string]json.RawMessage `json:"options"`
	Expected   struct {
		HTML *string `json:"html"`
	} `json:"expected"`
}

func renderConformanceEntry(e conformanceEntry, opts Options) (string, error) {
	if len(e.Host) == 0 {
		return ToHTMLOptions(*e.Input, opts)
	}
	options, err := opts.data()
	if err != nil {
		return "", err
	}
	var result RenderResult
	err = Call(context.Background(), map[string]any{
		"call": "renderWithDiagnostics", "source": *e.Input,
		"options": options, "host": e.Host,
	}, &result)
	return result.HTML, err
}

func conformanceOptions(raw map[string]json.RawMessage) (Options, []string) {
	var out Options
	var unsupported []string
	for name, value := range raw {
		var err error
		switch name {
		case "rawHtml":
			err = json.Unmarshal(value, &out.RawHTML)
		case "mode":
			err = json.Unmarshal(value, &out.Mode)
		case "profile":
			err = json.Unmarshal(value, &out.Profile)
		case "headingIds":
			err = json.Unmarshal(value, &out.HeadingIDs)
		case "headingShift":
			err = json.Unmarshal(value, &out.HeadingShift)
		case "symbols":
			err = json.Unmarshal(value, &out.Symbols)
		case "strictLosses":
			err = json.Unmarshal(value, &out.StrictLosses)
		case "roundtrip":
			err = json.Unmarshal(value, &out.Roundtrip)
		case "sourceLines":
			err = json.Unmarshal(value, &out.SourceLines)
		case "extensions":
			err = json.Unmarshal(value, &out.Extensions)
		case "urlPolicy":
			out.URLPolicy = &URLPolicy{}
			err = json.Unmarshal(value, out.URLPolicy)
		case "renderers":
			err = json.Unmarshal(value, &out.Renderers)
		default:
			unsupported = append(unsupported, name)
			continue
		}
		if err != nil {
			unsupported = append(unsupported, name)
		}
	}
	sort.Strings(unsupported)
	return out, unsupported
}

func loadConformance(t *testing.T, path, list string) []conformanceEntry {
	t.Helper()
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("CARVE_CONFORMANCE: %v", err)
	}
	var file map[string]json.RawMessage
	if err := json.Unmarshal(blob, &file); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	var entries []conformanceEntry
	if err := json.Unmarshal(file[list], &entries); err != nil {
		t.Fatalf("%s: the %q list: %v", path, list, err)
	}
	if len(entries) == 0 {
		t.Fatalf("%s holds no %s", path, list)
	}
	return entries
}

func TestConformance(t *testing.T) {
	dir := os.Getenv("CARVE_CONFORMANCE")
	if dir == "" {
		t.Skip("CARVE_CONFORMANCE not set; the corpus jobs in ci.yml set it")
	}
	tolerance := 0
	if raw := os.Getenv("CARVE_CORPUS_MAX_DIVERGENCE"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("CARVE_CORPUS_MAX_DIVERGENCE=%q is not a number", raw)
		}
		tolerance = parsed
	}

	var entries []conformanceEntry
	for _, e := range loadConformance(t, filepath.Join(dir, "examples.json"), "examples") {
		if e.Kind == "render" {
			entries = append(entries, e)
		}
	}
	for _, e := range loadConformance(t, filepath.Join(dir, "clause-cases.json"), "cases") {
		// A clause case without kind is a render (conformance README).
		if e.Kind == "" || e.Kind == "render" {
			entries = append(entries, e)
		}
	}

	var mismatches []string
	details := map[string]string{}
	compared := 0
	unsupported := map[string][]string{}
	outOfScope := map[string]int{}
	for _, e := range entries {
		switch {
		case e.Expected.HTML == nil:
			outOfScope["no expected.html"]++
			continue
		case e.Input == nil:
			outOfScope["no Carve input"]++
			continue
		case len(e.Files) > 0:
			outOfScope["include files"]++
			continue
		}
		opts, missing := conformanceOptions(e.Options)
		if len(missing) > 0 {
			key := strings.Join(missing, "+")
			unsupported[key] = append(unsupported[key], e.ID)
			continue
		}
		compared++
		got, err := renderConformanceEntry(e, opts)
		if err != nil {
			mismatches = append(mismatches, e.ID)
			details[e.ID] = "error: " + err.Error()
			continue
		}
		if strings.TrimRight(got, "\n") != strings.TrimRight(*e.Expected.HTML, "\n") {
			mismatches = append(mismatches, e.ID)
		}
	}
	if compared == 0 {
		t.Fatalf("no conformance entry was compared out of %d render entries", len(entries))
	}

	keys := make([]string, 0, len(unsupported))
	skipped := 0
	for key, ids := range unsupported {
		keys = append(keys, key)
		skipped += len(ids)
	}
	sort.Strings(keys)
	for _, key := range keys {
		t.Logf("unsupported option %s: %d (%s)", key, len(unsupported[key]), strings.Join(unsupported[key], ","))
	}
	scopes := make([]string, 0, len(outOfScope))
	for scope, n := range outOfScope {
		scopes = append(scopes, fmt.Sprintf("%s=%d", scope, n))
	}
	sort.Strings(scopes)
	sort.Strings(mismatches)
	t.Logf("render-entries=%d", len(entries))
	t.Logf("unsupported-options=%d", skipped)
	t.Logf("out-of-scope=%s", strings.Join(scopes, ", "))
	t.Logf("divergent-list=%s", strings.Join(mismatches, ","))
	// The corpus-drift job reads one of these two lines back.
	if len(mismatches) > 0 {
		t.Logf("%d of %d conformance entries diverge", len(mismatches), compared)
	} else {
		t.Logf("%d conformance entries byte-identical", compared)
	}

	if len(mismatches) > tolerance {
		reported := make([]string, 0, len(mismatches))
		for _, id := range mismatches {
			if detail, ok := details[id]; ok {
				id += ": " + detail
			}
			reported = append(reported, id)
		}
		t.Fatalf("%d of %d conformance entries diverge, over the tolerance of %d:\n%s",
			len(mismatches), compared, tolerance, strings.Join(reported, "\n"))
	}
}
