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
// text file, so the next one cannot be committed quietly.
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

func TestNoHostPathsInTrackedFiles(t *testing.T) {
	pattern := hostPathPattern()
	var findings []string

	for _, file := range trackedFiles(t) {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading tracked file %s: %v", file, err)
		}
		// Binary artifacts are out of scope here: internal/wasm/carve.wasm
		// carries the build host's cargo paths in its panic locations, which
		// no edit to a tracked text file can fix. build-wasm.sh passes
		// --remap-path-prefix so a rebuild stops embedding them.
		if bytes.IndexByte(data, 0) >= 0 {
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
