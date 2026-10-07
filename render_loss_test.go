package carve

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// A link to a denied scheme renders as href="" either way, so the blanking is
// not what these tests observe - what they observe is whether a caller can ask
// what the render dropped (#81).
const deniedLink = "[a](javascript:x)\n\n[b](javascript:y)\n"

func TestRenderCheckedReportsLossesBesideTheOutput(t *testing.T) {
	html, report, err := RenderChecked(deniedLink, OutputHTML, CheckedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `<a href="">a</a>`) {
		t.Errorf("the output still comes back: %q", html)
	}
	if report.TotalLosses != 2 || report.Truncated || len(report.Losses) != 2 {
		t.Fatalf("unexpected report: %#v", report)
	}
	first := report.Losses[0]
	if first.Code != "destination-denied" || first.Target != "html" ||
		first.NodeType != "inline" || first.Message != "Blanked a denied destination scheme" {
		t.Errorf("unexpected loss: %#v", first)
	}
	if first.Pos == nil || first.Pos.StartLine != 1 || first.Pos.StartColumn != 1 ||
		first.Pos.EndColumn != 18 || first.Pos.EndOffset != 17 {
		t.Errorf("unexpected position: %#v", first.Pos)
	}
	// An image names its own sink, so a caller can tell the two apart.
	_, imageReport, err := RenderChecked("![a](javascript:x)\n", OutputHTML, CheckedOptions{})
	if err != nil || len(imageReport.Losses) != 1 ||
		imageReport.Losses[0].Message != "Blanked a denied image source" {
		t.Errorf("an image's loss names the image sink: %#v, %v", imageReport.Losses, err)
	}
}

// A clean render reports zero rather than nothing, so "no loss" is a reading
// and not an absent one.
func TestRenderCheckedReportsZeroOnACleanRender(t *testing.T) {
	out, report, err := RenderChecked("# Title\n\nBody.\n", OutputHTML, CheckedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<h1>Title</h1>") {
		t.Errorf("unexpected output: %q", out)
	}
	if report.TotalLosses != 0 || report.Truncated || report.Losses == nil || len(report.Losses) != 0 {
		t.Errorf("a clean render must report an empty, non-nil loss list: %#v", report)
	}
}

// The default for every CheckedOptions field, stated because carve-go#78 was
// exactly this class: an empty selection that silently meant "everything".
func TestCheckedOptionsZeroValueIsNonStrictAndUnbounded(t *testing.T) {
	// Strict defaults to false: a lossy render returns its output.
	out, report, err := RenderChecked(deniedLink, OutputHTML, CheckedOptions{})
	if err != nil || out == "" || report.TotalLosses != 2 {
		t.Fatalf("the zero value must not refuse: %q, %#v, %v", out, report, err)
	}
	// MaxLosses nil means the engine's own bound, which is above two, so
	// nothing is truncated.
	if report.Truncated || len(report.Losses) != 2 {
		t.Errorf("a nil MaxLosses must not bound below the engine default: %#v", report)
	}
	// The embedded Options behaves as it does for Render: the zero value is
	// interactive HTML with no extensions.
	plain, err := Render(deniedLink, OutputHTML, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if plain != out {
		t.Errorf("a non-strict checked render must match Render:\n %q\n %q", plain, out)
	}
}

func TestRenderCheckedStrictRefusesAndStillCarriesTheReport(t *testing.T) {
	out, report, err := RenderChecked(deniedLink, OutputHTML, CheckedOptions{Strict: true})
	if err == nil {
		t.Fatal("a strict render of a lossy document must refuse")
	}
	if out != "" {
		t.Errorf("a refused render must not hand back output: %q", out)
	}
	// The report comes back on the refusal path too, both as the return value
	// and off the error, so a caller need not choose one.
	if report.TotalLosses != 2 || len(report.Losses) != 2 {
		t.Errorf("the refusal must carry its report: %#v", report)
	}
	var lossErr *RenderLossError
	if !errors.As(err, &lossErr) {
		t.Fatalf("a refusal must be a *RenderLossError, got %T: %v", err, err)
	}
	if lossErr.Report.TotalLosses != 2 {
		t.Errorf("the error's report: %#v", lossErr.Report)
	}
	if !strings.Contains(err.Error(), "destination-denied") {
		t.Errorf("the message should name what was lost: %q", err.Error())
	}

	// Strict on a clean document is not a refusal.
	if _, _, err := RenderChecked("# ok\n", OutputHTML, CheckedOptions{Strict: true}); err != nil {
		t.Errorf("strict must pass a clean render: %v", err)
	}
}

// MaxLosses bounds the DETAILED list only. This is the distinction carve-wasm#158
// lost: the visible array shrinks while the total does not, so a consumer
// reading len(Losses) as the count is reading a bounded number.
func TestMaxLossesBoundsTheDetailNotTheTotal(t *testing.T) {
	one := 1
	_, report, err := RenderChecked(deniedLink, OutputHTML, CheckedOptions{MaxLosses: &one})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Losses) != 1 || report.TotalLosses != 2 || !report.Truncated {
		t.Fatalf("bounded detail with an unbounded total: %#v", report)
	}

	// Zero is the engine's own "totals only" and has to stay expressible, which
	// is why the field is a pointer: a plain int could not tell it from unset.
	zero := 0
	_, totals, err := RenderChecked(deniedLink, OutputHTML, CheckedOptions{MaxLosses: &zero})
	if err != nil {
		t.Fatal(err)
	}
	if len(totals.Losses) != 0 || totals.TotalLosses != 2 || !totals.Truncated {
		t.Fatalf("zero must mean totals only: %#v", totals)
	}
}

// The allow-list is not duplicated here: the engine accepts two codes and says
// so itself, and destination-denied is deliberately not one of them.
func TestAllowLossDefersToTheEngine(t *testing.T) {
	_, _, err := RenderChecked(deniedLink, OutputHTML,
		CheckedOptions{Strict: true, AllowLoss: []string{"destination-denied"}})
	if err == nil || !strings.Contains(err.Error(), "--allow-loss expects") {
		t.Errorf("an unaccepted code must surface the engine's own list, got %v", err)
	}
	// An accepted code reaches the engine and is not a usage error.
	if _, _, err := RenderChecked("# ok\n", OutputHTML,
		CheckedOptions{Strict: true, AllowLoss: []string{"raw-format-dropped", "ruby-flattened"}}); err != nil {
		t.Errorf("the accepted codes must be accepted: %v", err)
	}
}

// Every field the engine emits must be represented, and Raw keeps the bytes so
// a field this package has not modeled yet is still reachable.
//
// carve-wasm#158 is the failure this holds against: that binding enumerates the
// report's fields by hand and silently dropped one, so its visible array
// disagreed with the truth and nothing noticed.
func TestEveryReportFieldTheEngineEmitsIsRepresented(t *testing.T) {
	_, report, err := RenderChecked(deniedLink, OutputHTML, CheckedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Raw) == 0 {
		t.Fatal("Raw must hold the engine's report verbatim")
	}
	var emitted map[string]any
	if err := json.Unmarshal(report.Raw, &emitted); err != nil {
		t.Fatalf("Raw must be the report JSON: %v", err)
	}

	missing := unmodeledKeys(reflect.TypeOf(RenderReport{}), emitted, "")
	sort.Strings(missing)
	if len(missing) != 0 {
		t.Errorf("the engine emits fields this package does not model: %v", missing)
	}

	// The check must be able to see a field go unmodeled, or it proves nothing.
	probe := map[string]any{"totalLosses": 2.0, "somethingNew": true,
		"losses": []any{map[string]any{"code": "x", "unmodeledInner": 1.0}}}
	if got := unmodeledKeys(reflect.TypeOf(RenderReport{}), probe, ""); len(got) != 2 {
		t.Errorf("the coverage check cannot see an unmodeled field: %v", got)
	}
}

// unmodeledKeys reports the JSON key paths in `value` that `typ` has no field
// for, following nested objects and the first element of each array.
func unmodeledKeys(typ reflect.Type, value any, prefix string) []string {
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice {
		typ = typ.Elem()
	}
	object, ok := value.(map[string]any)
	if !ok || typ.Kind() != reflect.Struct {
		return nil
	}
	fields := map[string]reflect.Type{}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" {
			name = field.Name
		}
		if name == "-" {
			continue
		}
		fields[name] = field.Type
	}
	var missing []string
	for key, inner := range object {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		fieldType, ok := fields[key]
		if !ok {
			missing = append(missing, path)
			continue
		}
		if list, ok := inner.([]any); ok {
			if len(list) > 0 {
				missing = append(missing, unmodeledKeys(fieldType, list[0], path+"[]")...)
			}
			continue
		}
		missing = append(missing, unmodeledKeys(fieldType, inner, path)...)
	}
	return missing
}

// The engine emits `"losses":[]` rather than null today, so the normalization
// in parseRenderReport is unreachable through RenderChecked. Exercised directly
// instead, so it is a check that can fail rather than defensive decoration.
func TestParseRenderReportNormalizesANullLossList(t *testing.T) {
	report, err := parseRenderReport(`{"losses":null,"totalLosses":0,"truncated":false}`)
	if err != nil {
		t.Fatal(err)
	}
	if report.Losses == nil {
		t.Error("a null loss list must come back as an empty slice")
	}

	// A stream with no report at all is an error rather than a zero report,
	// so an engine that stops writing one says so.
	if _, err := parseRenderReport("<stdin>: 1 render loss\n"); err == nil {
		t.Error("a missing report must be an error")
	}
	// The JSON is the LAST line: the human-readable loss lines come first.
	multi := "<stdin>:1:1 destination-denied - Blanked a denied destination scheme\n" +
		"<stdin>: 1 render loss\n" +
		`{"losses":[{"code":"destination-denied"}],"totalLosses":1,"truncated":false}`
	got, err := parseRenderReport(multi)
	if err != nil || got.TotalLosses != 1 || len(got.Losses) != 1 {
		t.Errorf("the report must be read past the human lines: %#v, %v", got, err)
	}
	// The LAST brace-leading line wins, not the first, so a message that
	// happens to open with a brace does not become the report.
	noisy := "{not the report}\n" + `{"losses":[],"totalLosses":7,"truncated":false}`
	if got, err := parseRenderReport(noisy); err != nil || got.TotalLosses != 7 {
		t.Errorf("the last brace-leading line is the report: %#v, %v", got, err)
	}
}

// Positions count CODEPOINTS, not bytes. Held because the doc comment first
// said bytes, which would have shipped a public contract that silently cuts a
// UTF-8 sequence in half for any source with non-ASCII text ahead of the loss.
func TestLossPositionsCountCodepointsNotBytes(t *testing.T) {
	// One 2-byte rune and one 4-byte rune in the same column, so a byte
	// reading would differ between them and a codepoint reading cannot.
	for _, lead := range []string{"é", "\U0001F600"} {
		source := lead + " [a](javascript:x)\n"
		_, report, err := RenderChecked(source, OutputHTML, CheckedOptions{})
		if err != nil || len(report.Losses) != 1 {
			t.Fatalf("%q: %#v, %v", lead, report, err)
		}
		pos := report.Losses[0].Pos
		if pos.StartOffset != 2 || pos.StartColumn != 3 || pos.EndOffset != 19 {
			t.Errorf("%q: positions must not move with the byte length: %#v", lead, pos)
		}
		// The byte index of the link differs from the reported offset, which
		// is the whole reason the unit has to be stated.
		if strings.Index(source, "[a]") == pos.StartOffset && lead != "" {
			t.Errorf("%q: byte index and codepoint offset agree, so this case proves nothing", lead)
		}
		// Converting first is what a caller must do.
		if string([]rune(source)[pos.StartOffset:pos.StartOffset+3]) != "[a]" {
			t.Errorf("%q: the offset must index runes: %q", lead,
				string([]rune(source)[pos.StartOffset:]))
		}
	}
}
