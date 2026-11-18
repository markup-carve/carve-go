package carve2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func unavailable(err error) bool {
	var engineErr *EngineError
	return errors.As(err, &engineErr) && engineErr.Category == "usage" &&
		strings.Contains(engineErr.Message, "not available in this engine yet")
}

// Options uses the 0.2 option names. The zero value keeps raw HTML disabled.
type Options struct {
	RawHTML      bool
	URLPolicy    *URLPolicy
	Mode         string
	Profile      string
	HeadingIDs   string
	Symbols      map[string]string
	StrictLosses bool
	Roundtrip    bool
	SourceLines  bool
	// Extensions require host code and are not accepted by the WASI JSON boundary yet.
	Extensions []string
}

type URLPolicy struct {
	Allow []string
	Deny  []string
}

// OutputFormat names a target in the 0.2 writer registry.
type OutputFormat string

const (
	OutputHTML      OutputFormat = "html"
	OutputMarkdown  OutputFormat = "markdown"
	OutputPlainText OutputFormat = "plain"
	OutputANSI      OutputFormat = "ansi"
	OutputCarve     OutputFormat = "carve"
)

func Render(source string, format OutputFormat, options Options) (string, error) {
	return RenderContext(context.Background(), source, format, options)
}

func RenderContext(ctx context.Context, source string, format OutputFormat, options Options) (string, error) {
	if format == "" || format == OutputHTML {
		return renderHTML(ctx, source, options)
	}
	if format == OutputCarve {
		return Format(ctx, source, false)
	}
	return Write(ctx, source, string(format), options)
}

func (o Options) data() (map[string]any, error) {
	m := map[string]any{}
	if o.RawHTML {
		m["rawHtml"] = true
	}
	if o.URLPolicy != nil {
		if o.URLPolicy.Allow != nil && o.URLPolicy.Deny != nil {
			return nil, errors.New("carve2: URLPolicy must select allow or deny")
		}
		if o.URLPolicy.Allow != nil {
			m["urlPolicy"] = map[string]any{"allow": o.URLPolicy.Allow}
		}
		if o.URLPolicy.Deny != nil {
			m["urlPolicy"] = map[string]any{"deny": o.URLPolicy.Deny}
		}
	}
	if o.Mode != "" {
		m["mode"] = o.Mode
	}
	if o.Profile != "" {
		m["profile"] = o.Profile
	}
	if o.HeadingIDs != "" {
		m["headingIds"] = o.HeadingIDs
	}
	if o.Symbols != nil {
		m["symbols"] = o.Symbols
	}
	if o.StrictLosses {
		m["strictLosses"] = true
	}
	if o.Roundtrip {
		m["roundtrip"] = true
	}
	if o.SourceLines {
		m["sourceLines"] = true
	}
	if len(o.Extensions) > 0 {
		return nil, errors.New("carve2: extension registration is not available yet through the WASI JSON boundary")
	}
	return m, nil
}

// Diagnostic is one 0.2 parse, render or lint record. Ranges use UTF-8 bytes.
type Diagnostic struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Channel  string `json:"channel"`
	Message  string `json:"message"`
	Path     string `json:"path,omitempty"`
	Range    *Range `json:"range,omitempty"`
	File     string `json:"file,omitempty"`
}
type Range struct {
	StartOffset int `json:"startOffset"`
	EndOffset   int `json:"endOffset"`
}
type RenderResult struct {
	HTML                  string       `json:"html"`
	Diagnostics           []Diagnostic `json:"diagnostics"`
	SuppressedDiagnostics int          `json:"suppressedDiagnostics"`
}
type ParseResult struct {
	Tree                  json.RawMessage `json:"tree"`
	Diagnostics           []Diagnostic    `json:"diagnostics"`
	SuppressedDiagnostics int             `json:"suppressedDiagnostics"`
}

type WriteOptions struct {
	AlignTables bool
	Normalize   bool
}
type WriteResult struct {
	Output      string       `json:"output"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// RenderTree writes HTML from a resolved 0.2 tree.
func RenderTree(ctx context.Context, tree json.RawMessage, options Options) (RenderResult, error) {
	opts, err := options.data()
	if err != nil {
		return RenderResult{}, err
	}
	var result RenderResult
	err = Call(ctx, map[string]any{"call": "renderWithDiagnostics", "tree": tree, "options": opts}, &result)
	return result, err
}

// WriteTree invokes a registered writer on a resolved 0.2 tree.
func WriteTree(ctx context.Context, tree json.RawMessage, target string, options WriteOptions) (WriteResult, error) {
	opts := map[string]any{}
	if options.AlignTables {
		opts["alignTables"] = true
	}
	if options.Normalize {
		opts["normalize"] = true
	}
	var result WriteResult
	err := Call(ctx, map[string]any{"call": "writeWithDiagnostics", "tree": tree, "target": target, "options": opts}, &result)
	if err != nil && target != "html" && unavailable(err) {
		return WriteResult{}, fmt.Errorf("carve2: %s writer not available yet: %w", target, err)
	}
	return result, err
}

func EncodeTree(ctx context.Context, tree json.RawMessage) (string, error) {
	var result struct {
		JSON string `json:"json"`
	}
	err := Call(ctx, map[string]any{"call": "encodeTree", "tree": tree}, &result)
	return result.JSON, err
}
func DecodeTree(ctx context.Context, encoded string) (json.RawMessage, error) {
	var result struct {
		Tree json.RawMessage `json:"tree"`
	}
	err := Call(ctx, map[string]any{"call": "decodeTree", "json": encoded}, &result)
	return result.Tree, err
}
func ValidateTree(ctx context.Context, tree json.RawMessage) error {
	return Call(ctx, map[string]any{"call": "validateTree", "tree": tree}, nil)
}

func renderHTML(ctx context.Context, source string, options Options) (string, error) {
	result, err := RenderWithDiagnostics(ctx, source, options)
	return result.HTML, err
}
func RenderWithDiagnostics(ctx context.Context, source string, options Options) (RenderResult, error) {
	opts, err := options.data()
	if err != nil {
		return RenderResult{}, err
	}
	var result RenderResult
	err = Call(ctx, map[string]any{"call": "renderWithDiagnostics", "source": source, "options": opts}, &result)
	return result, err
}
func ToHTML(source string) (string, error) {
	return renderHTML(context.Background(), source, Options{})
}
func ToHTMLContext(ctx context.Context, source string) (string, error) {
	return renderHTML(ctx, source, Options{})
}
func ToHTMLOptions(source string, options Options) (string, error) {
	return renderHTML(context.Background(), source, options)
}
func ToHTMLOptionsContext(ctx context.Context, source string, options Options) (string, error) {
	return renderHTML(ctx, source, options)
}
func ToHTMLStatic(source string) (string, error) {
	return renderHTML(context.Background(), source, Options{Mode: "static"})
}

func Parse(ctx context.Context, source string, options Options) (ParseResult, error) {
	opts, err := options.data()
	if err != nil {
		return ParseResult{}, err
	}
	var result ParseResult
	err = Call(ctx, map[string]any{"call": "parseWithDiagnostics", "source": source, "options": opts}, &result)
	return result, err
}
func ParseAST(source string) (json.RawMessage, error) {
	result, err := Parse(context.Background(), source, Options{})
	return result.Tree, err
}
func ParseASTContext(ctx context.Context, source string) (json.RawMessage, error) {
	result, err := Parse(ctx, source, Options{})
	return result.Tree, err
}

// Write sends a resolved tree to the named 0.2 writer registry target.
// The current WASI engine has only the html writer; other target names remain stable.
func Write(ctx context.Context, source string, target string, options Options) (string, error) {
	opts, err := options.data()
	if err != nil {
		return "", err
	}
	for key := range opts {
		if key != "headingIds" {
			return "", errors.New("carve2: write does not read render options; render HTML to apply them")
		}
	}
	parsed, err := Parse(ctx, source, options)
	if err != nil {
		return "", err
	}
	result, err := WriteTree(ctx, parsed.Tree, target, WriteOptions{})
	return result.Output, err
}
func ToMarkdown(source string) (string, error) {
	return Write(context.Background(), source, "markdown", Options{})
}
func ToPlainText(source string) (string, error) {
	return Write(context.Background(), source, "plain", Options{})
}
func ToANSI(source string) (string, error) {
	return Write(context.Background(), source, "ansi", Options{})
}
func ToCarve(source string) (string, error) { return Format(context.Background(), source, false) }

func Format(ctx context.Context, source string, alignTables bool) (string, error) {
	opts := map[string]any{}
	if alignTables {
		opts["alignTables"] = true
	}
	var result struct {
		Source string `json:"source"`
	}
	err := Call(ctx, map[string]any{"call": "format", "source": source, "options": opts}, &result)
	if unavailable(err) {
		return "", fmt.Errorf("carve2: carve writer not available yet: %w", err)
	}
	return result.Source, err
}

// ImportDocument keeps the 0.2 importer entry point until the Rust engine provides it.
func ImportDocument(ctx context.Context, source, format string) (json.RawMessage, error) {
	var result json.RawMessage
	err := Call(ctx, map[string]any{"call": "importDocument", "source": source, "from": format}, &result)
	if unavailable(err) {
		return nil, fmt.Errorf("carve2: %s importer not available yet: %w", format, err)
	}
	return result, err
}
func FromHTML(source string) (json.RawMessage, error) {
	return ImportDocument(context.Background(), source, "html")
}
func FromMarkdown(source string) (json.RawMessage, error) {
	return nil, errors.New("carve2: Markdown importer not available in the 0.2 engine")
}
