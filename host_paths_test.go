package carve

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// A developer's own filesystem has reached this repository's tracked files
// before: build-wasm.sh defaulted CARVE_RS to one absolute path under /media,
// which published a directory layout and, because it resolved on exactly one
// machine, meant the fallback everybody else depends on was never exercised.
// The same shape shipped a broken 0.1.0 in markup-carve/carve-pdf.
//
// Grep is not the guard - a sweep greps for the pattern someone already knows.
// This one fails the suite on any host-rooted absolute path in any tracked
// file, so the next one cannot be committed quietly. Text files are read line
// by line below; binaries are scanned whole by the arm after it, because a
// text-only guard is what let the embedded wasm keep 96 of them.
//
// A line that genuinely needs such a path (a fixture proving a path does not
// leak, a documented CI location) carries the marker below on the same line.
const hostPathAllowMarker = "host-path-guard: intentional"

// Go's regexp engine has no negative lookahead, so the allowlist is applied
// to the captured user component rather than written into the pattern.
var hostPathAllowedUsers = map[string]bool{
	// Machines nobody owns. GitHub's Linux runners work under /home/runner,
	// its macOS runners under /Users/runner.
	"runner":  true,
	"vsts":    true,
	"vagrant": true,
}

func hostPathPattern() *regexp.Regexp {
	// Assembled from fragments so this file's own source carries no literal
	// host path. The guard therefore still sees a leak committed HERE, which
	// an exemption for the guard's own file would have blinded it to.
	sep := "/"
	return regexp.MustCompile(sep + `(home|Users|media|mnt)` + sep + `([A-Za-z0-9._-]+)` + sep)
}

// findHostPath returns the offending path prefix in line, or "".
func findHostPath(pattern *regexp.Regexp, line string) string {
	for _, m := range pattern.FindAllStringSubmatch(line, -1) {
		if !hostPathAllowedUsers[m[2]] {
			return m[0]
		}
	}
	return ""
}

func trackedFiles(t *testing.T) []string {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-z")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("git ls-files unavailable (%v): %s", err, stderr.String())
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		t.Fatal("git ls-files listed no tracked files; the guard would pass by scanning nothing")
	}
	return files
}

func isBinary(data []byte) bool { return bytes.IndexByte(data, 0) >= 0 }

// scanBinaryForHostPaths returns the distinct offending path prefixes in data,
// in the order they first appear.
//
// The text arm reads tracked files line by line, which is why a host path
// survived in internal/wasm/carve.wasm for three releases: Rust embeds the
// build machine's cargo registry in panic locations, and no grep over text
// files can see them. The remedy is a rebuild under --remap-path-prefix, so
// what this arm guards is that the rebuilt bytes stay remapped.
func scanBinaryForHostPaths(pattern *regexp.Regexp, data []byte) []string {
	var found []string
	seen := map[string]bool{}
	for _, m := range pattern.FindAllSubmatch(data, -1) {
		if hostPathAllowedUsers[string(m[2])] {
			continue
		}
		prefix := string(m[0])
		if seen[prefix] {
			continue
		}
		seen[prefix] = true
		found = append(found, prefix)
	}
	return found
}

// binaryFindingCap bounds the report. Distinct prefixes are already
// deduplicated - the pre-fix carve.wasm held 96 matches of a single one - but a
// differently built artifact could carry many, and a hundred-line failure
// buries the point.
const binaryFindingCap = 8

func TestNoHostPathsInTrackedBinaries(t *testing.T) {
	pattern := hostPathPattern()
	var findings []string

	for _, file := range trackedFiles(t) {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading tracked file %s: %v", file, err)
		}
		if !isBinary(data) {
			continue
		}
		prefixes := scanBinaryForHostPaths(pattern, data)
		for i, prefix := range prefixes {
			if i == binaryFindingCap {
				findings = append(findings, fmt.Sprintf("%s: ...and %d more distinct prefixes", file, len(prefixes)-i))
				break
			}
			findings = append(findings, fmt.Sprintf("%s: %s", file, prefix))
		}
	}

	if len(findings) > 0 {
		t.Errorf("tracked binaries contain host-rooted absolute paths:\n  %s\n\n"+
			"A committed artifact carries whichever machine built it. Rebuild it with the "+
			"source prefixes remapped out - for internal/wasm/carve.wasm that is "+
			"./build-wasm.sh, which passes --remap-path-prefix for both the carve-rs "+
			"checkout and CARGO_HOME.",
			strings.Join(findings, "\n  "))
	}
}

func TestNoHostPathsInTrackedFiles(t *testing.T) {
	pattern := hostPathPattern()
	var findings []string

	for _, file := range trackedFiles(t) {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading tracked file %s: %v", file, err)
		}
		// Binaries are scanned by TestNoHostPathsInTrackedBinaries instead:
		// they have no lines to report and no place to put an allow marker.
		if isBinary(data) {
			continue
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, hostPathAllowMarker) {
				continue
			}
			if m := findHostPath(pattern, line); m != "" {
				findings = append(findings, fmt.Sprintf("%s:%d: %s", file, i+1, m))
			}
		}
	}

	if len(findings) > 0 {
		t.Errorf("tracked files contain host-rooted absolute paths:\n  %s\n\n"+
			"A path under a home or mount directory is one machine's layout. Resolve it "+
			"relatively or require it from the environment with an actionable error. If a "+
			"line genuinely needs one, mark it with %q.",
			strings.Join(findings, "\n  "), hostPathAllowMarker)
	}
}

// The guard above is only worth its line count if its patterns actually match
// the shapes it claims to catch, and only pass the ones it claims to allow.
func TestHostPathPatternsMatchWhatTheyClaim(t *testing.T) {
	pattern := hostPathPattern()
	match := func(line string) bool { return findHostPath(pattern, line) != "" }

	// Built from fragments for the same reason hostPathPattern is.
	s := "/"
	caught := []string{
		"CARVE_RS=" + s + "media" + s + "someone" + s + "work" + s + "git" + s + "carve-rs",
		s + "home" + s + "someone" + s + ".cargo" + s + "bin" + s + "cargo",
		s + "Users" + s + "someone" + s + "src" + s + "carve-go",
		s + "mnt" + s + "someone" + s + "data",
	}
	for _, line := range caught {
		if !match(line) {
			t.Errorf("guard does not catch %q", line)
		}
	}

	allowed := []string{
		s + "home" + s + "runner" + s + "work" + s + "carve-go",
		s + "Users" + s + "runner" + s + "work",
		s + "usr" + s + "local" + s + "bin" + s + "cargo",
		s + "tmp" + s + "carve-rs-build",
		"Include{Root: \"content\", SourcePath: \"" + s + "abs" + s + "content" + s + "a.crv\"}",
	}
	for _, line := range allowed {
		if match(line) {
			t.Errorf("guard wrongly flags %q", line)
		}
	}
}

// Same reasoning for the binary arm: a guard whose scanner nobody has watched
// fire is not evidence. These bytes are shaped like what a wasm artifact
// actually carries - a path with no newline near it, wedged between NUL bytes
// and other binary noise, which is precisely what the line-based arm misses.
func TestBinaryScanMatchesWhatItClaims(t *testing.T) {
	pattern := hostPathPattern()
	s := "/"

	leak := []byte("\x00\x07carve-rs" + s + "src" + s + "lib.rs\x00" +
		s + "home" + s + "someone" + s + ".cargo" + s + "registry" + s + "src\x00\xff")
	got := scanBinaryForHostPaths(pattern, leak)
	want := s + "home" + s + "someone" + s
	if len(got) != 1 || got[0] != want {
		t.Errorf("binary scan of a leaking artifact = %q, want exactly [%q]", got, want)
	}
	if !isBinary(leak) {
		t.Error("a NUL-bearing artifact must classify as binary, or the text arm claims it")
	}

	// A remapped artifact keeps the remap targets, which name no machine.
	clean := []byte("\x00\x07" + s + "carve-rs" + s + "src" + s + "lib.rs\x00" +
		s + "cargo" + s + "registry" + s + "src" + s + "index.crates.io-1949cf8c6b5b557f\x00\xff")
	if got := scanBinaryForHostPaths(pattern, clean); got != nil {
		t.Errorf("binary scan of a remapped artifact = %q, want none", got)
	}

	// The runner allowance carries over from the text arm.
	runner := []byte("\x00" + s + "home" + s + "runner" + s + "work" + s + "carve-go\x00")
	if got := scanBinaryForHostPaths(pattern, runner); got != nil {
		t.Errorf("binary scan wrongly flags a CI runner path: %q", got)
	}

	// Distinct prefixes are deduplicated, so 96 matches do not become 96 lines.
	repeated := bytes.Repeat([]byte("\x00"+s+"home"+s+"someone"+s+".cargo"+s), 30)
	if got := scanBinaryForHostPaths(pattern, repeated); len(got) != 1 {
		t.Errorf("binary scan of 30 copies of one prefix = %d findings, want 1", len(got))
	}
}
