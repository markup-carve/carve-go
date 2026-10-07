package carve

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
)

// What the embedded artifact SAYS it is, against what this repository RECORDS
// it as.
//
// internal/wasm/REV names a carve-rs commit and carve.wasm.sha256 names the
// bytes, and between them they still leave one thing unasserted: which engine
// those bytes are. The digest proves only that the artifact is intact, so it
// matches whatever it was computed from, including an artifact built from an
// entirely different engine; and REV is a line of text that nothing ties to the
// binary, so editing it alone passes every other check here. build-wasm.sh says
// as much in the comment beside the digest: closing that would need CI to
// rebuild from REV and compare, and the build is not byte-reproducible across
// checkout paths anyway.
//
// carve-rs 0.1.8 made a cheaper route available. `carve --version` reports the
// engine version from INSIDE the binary (carve-rs#2270, #2271), so the artifact
// can be asked what it is rather than having to be rebuilt to find out. That is
// what this compares against internal/wasm/ENGINE_VERSION, which build-wasm.sh
// writes in the same step as REV and the digest.
//
// It catches an artifact swapped for a different engine version, a half-finished
// rebuild that updated the bytes and not the record, and a REV moved across a
// version boundary. It does NOT distinguish two commits of the same version;
// that hole stays open, and it is narrower than the one this closes.

// carve-rs main reads X.Y.Z-dev between releases, so a REV on main reports that.
var engineVersionLine = regexp.MustCompile(`^carve-rs ([0-9]+\.[0-9]+\.[0-9]+(?:-dev)?)$`)

// recordedEngineVersion is the version this repository commits to, read from
// the file build-wasm.sh writes beside REV.
func recordedEngineVersion(t *testing.T) string {
	t.Helper()
	blob, err := os.ReadFile("internal/wasm/ENGINE_VERSION")
	if err != nil {
		// Not a skip. A missing record is the state this test exists to
		// forbid: it is what an artifact with no stated version looks like,
		// and skipping would report success over exactly that.
		t.Fatalf("no internal/wasm/ENGINE_VERSION: %v. build-wasm.sh writes it beside REV "+
			"and carve.wasm.sha256; an artifact with no recorded version cannot be checked "+
			"against the engine it reports itself to be", err)
	}
	version := strings.TrimSpace(string(blob))
	if version == "" {
		t.Fatal("internal/wasm/ENGINE_VERSION is empty, so there is nothing to hold the artifact to")
	}
	return version
}

// reportedEngineVersion asks the embedded artifact.
func reportedEngineVersion(t *testing.T) string {
	t.Helper()
	eng, err := loadEngine()
	if err != nil {
		t.Fatalf("loading the embedded engine: %v", err)
	}
	out, status, err := runEngine(context.Background(), eng, []string{"carve", "--version"}, "")
	if err != nil {
		t.Fatalf("running the embedded engine with --version: %v", err)
	}
	if status != 0 {
		t.Fatalf("the embedded engine exited %d for --version (stderr: %q). The flag arrived in "+
			"carve-rs 0.1.8; an artifact that refuses it predates the version it is recorded as, "+
			"or internal/wasm/ENGINE_VERSION describes an engine these bytes are not",
			status, strings.TrimSpace(out.stderr))
	}
	line := strings.TrimSpace(out.stdout)
	m := engineVersionLine.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("the embedded engine answered --version with %q, which is not `carve-rs X.Y.Z`. "+
			"That spelling is the contract carve-rs#2270 set; a change to it is a change this "+
			"package reads", line)
	}
	return m[1]
}

func TestTheEmbeddedEngineReportsTheRecordedVersion(t *testing.T) {
	recorded := recordedEngineVersion(t)
	reported := reportedEngineVersion(t)

	if recorded != reported {
		t.Fatalf("internal/wasm/ENGINE_VERSION records %s but the embedded artifact reports %s. "+
			"The bytes and the record describe different engines, which is a stale, swapped or "+
			"half-committed rebuild; re-run ./build-wasm.sh so all four files are written together",
			recorded, reported)
	}
}

// The ablation. Without it the comparison above passes identically whether the
// pattern matched a real version or the helpers silently agreed on nothing.
func TestTheEngineVersionCheckCanFail(t *testing.T) {
	if engineVersionLine.MatchString("carve-rs") {
		t.Fatal("the version pattern matches a line carrying no version, so it cannot tell a " +
			"well-formed report from a truncated one")
	}
	if engineVersionLine.MatchString("carve 0.1.8") {
		t.Fatal("the version pattern matches the wrong program name, so it would accept a " +
			"different binary's report")
	}
	m := engineVersionLine.FindStringSubmatch("carve-rs 1.2.3")
	if m == nil || m[1] != "1.2.3" {
		t.Fatalf("the version pattern does not capture the version out of a well-formed line: %v", m)
	}
	m = engineVersionLine.FindStringSubmatch("carve-rs 1.2.4-dev")
	if m == nil || m[1] != "1.2.4-dev" {
		t.Fatalf("the version pattern does not capture a between-releases -dev version: %v", m)
	}
	if engineVersionLine.MatchString("carve-rs 1.2.4-rc1") {
		t.Fatal("the version pattern accepts a prerelease carve-rs never publishes")
	}
}
