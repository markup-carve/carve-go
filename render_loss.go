package carve

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// LossPosition is a render loss's span in the source. All four line and column
// numbers are 1-based; offsets are 0-based byte offsets.
type LossPosition struct {
	StartLine   int `json:"startLine"`
	EndLine     int `json:"endLine"`
	StartColumn int `json:"startColumn"`
	EndColumn   int `json:"endColumn"`
	StartOffset int `json:"startOffset"`
	EndOffset   int `json:"endOffset"`
}

// RenderLoss is one thing a render dropped or flattened rather than emitted.
type RenderLoss struct {
	// Code is the engine's loss code, such as destination-denied.
	Code string `json:"code"`
	// Format is the raw-block format a raw-format-dropped loss names. It is
	// optional upstream and absent for losses that have no format.
	Format string `json:"format,omitempty"`
	// Target is the render target the loss happened on, such as html.
	Target string `json:"target"`
	// NodeType is the kind of node that lost something, such as inline.
	NodeType string `json:"nodeType"`
	// Message is the engine's prose. It names the sink: a denied link reads
	// "Blanked a denied destination scheme" and a denied image "Blanked a
	// denied image source".
	Message string `json:"message"`
	// Pos is the span, absent when the engine attributed no position.
	Pos *LossPosition `json:"pos,omitempty"`
}

// RenderReport is what a checked render lost.
//
// Losses is the DETAILED list and TotalLosses is the count; CheckedOptions.MaxLosses
// bounds the first and never the second, so len(Losses) is not the number of
// losses whenever Truncated is true. Reading the slice length as the count is
// how markup-carve/carve-wasm#158 reported 20 where the truth was 40.
type RenderReport struct {
	Losses      []RenderLoss `json:"losses"`
	TotalLosses int          `json:"totalLosses"`
	Truncated   bool         `json:"truncated"`
	// Raw is the engine's report verbatim, so a field this package has not
	// modeled yet is still reachable without a release here.
	Raw json.RawMessage `json:"-"`
}

// RenderLossError is the error a strict checked render refuses with. The report
// is carried on it as well as returned, so a caller need not type-assert to
// find out what was lost.
type RenderLossError struct {
	Report RenderReport
}

func (e *RenderLossError) Error() string {
	codes := make([]string, 0, len(e.Report.Losses))
	seen := map[string]bool{}
	for _, loss := range e.Report.Losses {
		if !seen[loss.Code] {
			seen[loss.Code] = true
			codes = append(codes, loss.Code)
		}
	}
	detail := strings.Join(codes, ", ")
	if detail == "" {
		detail = "no loss code reported"
	}
	return fmt.Sprintf("carve: render lost %d item(s) (%s)", e.Report.TotalLosses, detail)
}

// CheckedOptions is Options plus the engine's loss-reporting controls. The zero
// value equals Options{} and reports without refusing anything.
type CheckedOptions struct {
	Options

	// Strict refuses the output when the render lost anything not allowed
	// (--strict-losses). False, the default, returns the output and the
	// report together.
	Strict bool
	// AllowLoss lists loss codes Strict tolerates (--allow-loss). The
	// accepted codes are not duplicated here, so an unaccepted one comes
	// back as the engine's own error naming what it takes.
	AllowLoss []string
	// MaxLosses bounds the DETAILED losses (--max-render-losses), never
	// TotalLosses. Nil selects the engine's own default, which is 100.
	//
	// A pointer rather than an int because the engine accepts 0, meaning
	// totals with no detail, and a plain int could not tell that from unset.
	MaxLosses *int
}

// RenderChecked is Render with the engine's loss report returned beside the
// output.
//
// It is a second entry point rather than a change to Render, whose
// (string, error) has no slot for a report. A non-strict checked render returns
// exactly what Render returns, plus the report.
//
// Uses context.Background(); prefer RenderCheckedContext for untrusted input.
func RenderChecked(source string, format OutputFormat, opts CheckedOptions) (string, RenderReport, error) {
	return RenderCheckedContext(context.Background(), source, format, opts)
}

// RenderCheckedContext is RenderChecked with a caller-supplied context.
func RenderCheckedContext(
	ctx context.Context,
	source string,
	format OutputFormat,
	opts CheckedOptions,
) (string, RenderReport, error) {
	if opts.Static && format != OutputHTML {
		return "", RenderReport{}, fmt.Errorf("carve: Options.Static applies to HTML only, not %q", format.flag())
	}

	eng, err := loadEngine()
	if err != nil {
		return "", RenderReport{}, err
	}

	args, err := renderArgs(format, opts.Options)
	if err != nil {
		return "", RenderReport{}, err
	}
	// `-` sends the JSON report to stderr, where the engine also writes its
	// human-readable loss lines and a summary, so the JSON is the last line
	// rather than the whole stream.
	args = append(args, "--report-losses", "-")
	if opts.Strict {
		args = append(args, "--strict-losses")
	}
	for _, code := range opts.AllowLoss {
		if code == "" {
			return "", RenderReport{}, fmt.Errorf("carve: CheckedOptions.AllowLoss contains an empty loss code")
		}
		args = append(args, "--allow-loss", code)
	}
	if opts.MaxLosses != nil {
		if *opts.MaxLosses < 0 {
			return "", RenderReport{}, fmt.Errorf("carve: CheckedOptions.MaxLosses is negative: %d", *opts.MaxLosses)
		}
		args = append(args, "--max-render-losses", strconv.Itoa(*opts.MaxLosses))
	}

	out, code, err := runEngine(ctx, eng, args, source)
	if err != nil {
		return "", RenderReport{}, err
	}
	// Exit 1 is a strict refusal, which still carries a report; 2 and above are
	// a usage error or a failure to render, which do not.
	if code != 0 && code != 1 {
		return "", RenderReport{}, fmt.Errorf("carve: engine exited with code %d: %s", code, out.stderr)
	}

	report, err := parseRenderReport(out.stderr)
	if err != nil {
		return "", RenderReport{}, err
	}
	if code == 1 {
		return "", report, &RenderLossError{Report: report}
	}
	return out.stdout, report, nil
}

// parseRenderReport reads the JSON report out of the engine's stderr, which
// also carries the human-readable loss lines and a summary line ahead of it.
func parseRenderReport(stderr string) (RenderReport, error) {
	raw := ""
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "{") {
			raw = line
		}
	}
	if raw == "" {
		return RenderReport{}, fmt.Errorf("carve: the engine wrote no loss report: %s", stderr)
	}
	var report RenderReport
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		return RenderReport{}, fmt.Errorf("carve: invalid loss report: %w", err)
	}
	report.Raw = json.RawMessage(raw)
	if report.Losses == nil {
		report.Losses = []RenderLoss{}
	}
	return report, nil
}
