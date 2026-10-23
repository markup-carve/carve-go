package carve

import (
	"fmt"
	"strings"
)

// RegionMode selects which marker set SelectIncludeRegion reads (spec I1b).
type RegionMode int

const (
	// RegionCarve reads only `%% #region` / `%% #endregion`, as a Carve
	// include does.
	RegionCarve RegionMode = iota
	// RegionCode also reads the source-comment forms (`// #region`,
	// `-- #region`, `/* #region */`, `<!-- #region -->` and a bare
	// `#region`), as a `@code` include does.
	RegionCode
)

// IncludeRegion is the 1-based, inclusive range of a region's content lines.
// The opening marker sits on Start-1; an empty region has End == Start-1.
type IncludeRegion struct {
	Start int
	End   int
}

// IncludeRegionError reports a missing region, or a malformed or unpaired
// marker anywhere in the file.
type IncludeRegionError struct {
	Name string
}

func (e *IncludeRegionError) Error() string {
	return fmt.Sprintf("carve: include region %q is invalid", e.Name)
}

// Rule is the warning rule expansion reports for the same failure.
func (e *IncludeRegionError) Rule() string { return "include-region-invalid" }

// SelectIncludeRegion resolves `@region:name` in a file's text with the
// engine's marker rules, so a host (an editor jumping to a region) agrees with
// what expansion cuts.
//
// The engine CLI has no entry point for this, so it is a line-for-line port of
// carve-rs `select_include_region`; align_region_test.go holds it to the engine's
// answers.
func SelectIncludeRegion(source, name string, mode RegionMode) (IncludeRegion, error) {
	start, end, ok := regionRange(source, name, mode == RegionCode)
	if !ok {
		return IncludeRegion{}, &IncludeRegionError{Name: name}
	}
	return IncludeRegion{Start: start, End: end}, nil
}

func regionRange(source, selected string, code bool) (int, int, bool) {
	type open struct {
		name  string
		index int
	}
	seen := map[string]bool{}
	var stack []open
	found := false
	var start, end int
	for index, raw := range regionSourceLines(source) {
		line := strings.TrimLeft(raw, " \t")
		var body, closer string
		switch {
		case strings.HasPrefix(line, "%%") && !strings.HasPrefix(line, "%%%"):
			body = line[2:]
		case !code:
			continue
		case strings.HasPrefix(line, "//") || strings.HasPrefix(line, "--"):
			body = line[2:]
		case strings.HasPrefix(line, "/*"):
			body, closer = line[2:], "*/"
		case strings.HasPrefix(line, "<!--"):
			body, closer = line[4:], "-->"
		default:
			body = line
		}
		body = strings.TrimLeft(body, " \t")
		var closing bool
		var rest string
		if r, ok := strings.CutPrefix(body, "#endregion"); ok {
			closing, rest = true, r
		} else if r, ok := strings.CutPrefix(body, "#region"); ok {
			rest = r
		} else {
			continue
		}
		if rest != "" && isRegionWordByte(rest[0]) {
			continue
		}
		rest = strings.TrimRight(rest, " \t")
		if closer != "" {
			r, ok := strings.CutSuffix(rest, closer)
			if !ok {
				return 0, 0, false
			}
			rest = strings.TrimRight(r, " \t")
		}
		label := ""
		if rest != "" {
			if rest[0] != ' ' && rest[0] != '\t' {
				return 0, 0, false
			}
			label = strings.TrimLeft(rest[1:], " \t")
			if !isRegionIdent(label) {
				return 0, 0, false
			}
		}
		if closing {
			if len(stack) == 0 {
				return 0, 0, false
			}
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if label != "" && label != top.name {
				return 0, 0, false
			}
			if top.name == selected {
				found, start, end = true, top.index+2, index
			}
			continue
		}
		if label == "" || seen[label] {
			return 0, 0, false
		}
		seen[label] = true
		stack = append(stack, open{label, index})
	}
	if len(stack) != 0 || !found {
		return 0, 0, false
	}
	return start, end, true
}

// regionSourceLines splits on LF, CR and CRLF, dropping an empty final line.
func regionSourceLines(source string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(source); i++ {
		if source[i] != '\r' && source[i] != '\n' {
			continue
		}
		lines = append(lines, source[start:i])
		if source[i] == '\r' && i+1 < len(source) && source[i+1] == '\n' {
			i++
		}
		start = i + 1
	}
	if start < len(source) {
		lines = append(lines, source[start:])
	}
	return lines
}

func isRegionWordByte(c byte) bool {
	return c == '_' || ('0' <= c && c <= '9') || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

// isRegionIdent: an ASCII letter or `_`, then letters, digits, `_` or `-`.
func isRegionIdent(name string) bool {
	if name == "" || name[0] == '-' || ('0' <= name[0] && name[0] <= '9') {
		return false
	}
	for i := 0; i < len(name); i++ {
		if !isRegionWordByte(name[i]) && name[i] != '-' {
			return false
		}
	}
	return true
}
