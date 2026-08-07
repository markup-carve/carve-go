package carve

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// slowInput is a Carve source whose full parse takes long enough to be cut in
// half, so it exercises deadline interruption of CPU-bound guest code. Each
// "[a](" opens a link destination the parser keeps scanning for; the engine
// handles this in linear time (an earlier O(n^2) was fixed upstream), so the
// parse cost is linear in this repeat count.
//
// The count no longer has to clear an absolute deadline. It used to: the
// deadline was a fixed 50ms and the comment here claimed the parse took ~0.5s,
// which had stopped being true (measured 0.09s at the previous count of
// 100000, i.e. under twice the deadline). Both the deadline and the ceiling are
// now fractions of a MEASURED parse - see uninterruptedParse - so the only
// thing this count controls is how much absolute room the fractions have on a
// loaded machine.
func slowInput() string { return strings.Repeat("[a](", 300000) }

// uninterruptedParse is how long slowInput takes to render with no deadline, on
// THIS machine, measured once per test binary.
//
// The interruption tests below are about a proportion - did the call return
// because it was cut short, or because it ran to completion - and that question
// has no wall-clock answer. It used to be asked as `elapsed > 20*time.Second`,
// against work that takes a tenth of a second: a ceiling 200 times the length
// of the run it bounds cannot fail, so it never once distinguished the two
// cases it was written to distinguish. Measuring the completion time and
// comparing against a fraction of it asks the real question, and asks it the
// same way on a fast laptop, a contended CI runner, and under -race (where this
// input measures roughly three times slower and every fraction moves with it).
var (
	uninterruptedOnce sync.Once
	uninterruptedFull time.Duration
	uninterruptedErr  error
)

func uninterruptedParse(t *testing.T) time.Duration {
	t.Helper()
	uninterruptedOnce.Do(func() {
		// Warm first: a cold start would charge the one-time wasm compile to
		// the baseline and inflate every fraction derived from it.
		if _, err := ToHTMLContext(context.Background(), "# warm"); err != nil {
			uninterruptedErr = err
			return
		}
		start := time.Now()
		_, uninterruptedErr = ToHTMLContext(context.Background(), slowInput())
		uninterruptedFull = time.Since(start)
	})
	if uninterruptedErr != nil {
		t.Fatalf("measuring the uninterrupted parse failed: %v", uninterruptedErr)
	}
	return uninterruptedFull
}

// requireCutShort is the assertion both interruption tests share: the call came
// back well before the same work would have finished on its own.
//
// Half is a deliberately loose fraction. Interruption lands at about an eighth
// of the parse (the deadline requireCutShort's callers use), so a run has to be
// four times slower than expected before this reports, which is the margin that
// keeps a proportion assertion from becoming a flakiness source on a shared
// runner.
func requireCutShort(t *testing.T, elapsed time.Duration, what string) {
	t.Helper()
	full := uninterruptedParse(t)
	// Logged rather than only asserted: the two numbers are what someone
	// debugging a failure here needs, and a reader of a -v run can see the
	// margin the fraction is actually running with on that machine.
	t.Logf("returned in %v; the same render with no deadline takes %v", elapsed, full)
	if elapsed >= full/2 {
		t.Fatalf("%s: the call took %v, and the same render with no deadline takes %v on this "+
			"machine. It was not cut short, it very nearly ran to completion.", what, elapsed, full)
	}
}

// warmEngine forces the one-time, relatively expensive wasm compilation to
// happen now, on a context with no deadline. Tests that assert render
// cancellation must call this first: otherwise, on a cold start, a short
// deadline could be tripped by the compile step itself rather than by
// interrupting the running parse, and the test would pass even with
// WithCloseOnContextDone removed (the exact false-positive this avoids).
func warmEngine(t *testing.T) {
	t.Helper()
	if _, err := ToHTMLContext(context.Background(), "# warm"); err != nil {
		t.Fatalf("warm-up render failed: %v", err)
	}
}

// TestToHTMLContext_DeadlineInterrupts is the Finding 1 regression guard: an
// expired/short context deadline must actually interrupt CPU-bound guest code,
// returning promptly with a context error instead of running the parse loop to
// completion. This only holds because the runtime is built with
// WithCloseOnContextDone(true); without it the deadline is a no-op and this
// test would block for the full run time before failing.
func TestToHTMLContext_DeadlineInterrupts(t *testing.T) {
	// Pay the wasm compile cost up front so the deadline below bounds only the
	// render, never the cold-start compilation (see warmEngine). Measuring the
	// baseline warms it too; both are here because the order matters and only
	// one of them says so.
	warmEngine(t)
	full := uninterruptedParse(t)

	src := slowInput()

	// An eighth of the measured parse, so the deadline is short relative to the
	// work on any machine rather than short in milliseconds on one of them.
	ctx, cancel := context.WithTimeout(context.Background(), full/8)
	defer cancel()

	start := time.Now()
	_, err := ToHTMLContext(ctx, src)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected a context error from an expired deadline, got nil (ran to completion in %v)", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected errors.Is(err, context.DeadlineExceeded), got %v", err)
	}
	// The whole point of the fix: the call returns because it was interrupted,
	// not because it ran to completion.
	requireCutShort(t, elapsed, "the deadline did not interrupt")
}

// TestToHTMLContext_CanceledInterrupts asserts an already-canceled context is
// honored: the render must not run to completion.
func TestToHTMLContext_CanceledInterrupts(t *testing.T) {
	warmEngine(t)

	src := slowInput()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // canceled before the call

	start := time.Now()
	_, err := ToHTMLContext(ctx, src)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected a context error from a canceled context, got nil (ran to completion in %v)", elapsed)
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected a context error, got %v", err)
	}
	requireCutShort(t, elapsed, "cancellation did not interrupt")
}

// TestToHTML_NoDeadlineStillCompletes guards that the hardened runtime does not
// break the no-deadline path: a normal render with a background context still
// succeeds (WithCloseOnContextDone must not spuriously interrupt work).
func TestToHTML_NoDeadlineStillCompletes(t *testing.T) {
	out, err := ToHTMLContext(context.Background(), "# Hi\n\n*bold*")
	if err != nil {
		t.Fatalf("background-context render failed: %v", err)
	}
	if !strings.Contains(out, "<h1") || !strings.Contains(out, "<strong>bold</strong>") {
		t.Fatalf("unexpected output: %q", out)
	}
}

// TestToHTMLContext_CancellationDoesNotPoisonRuntime guards the compile-once,
// shared-runtime model against a subtle hazard: the one-time engine build is
// guarded by sync.Once, and wazero's compiler honors ctx.Err() during
// compilation. If that build ran under a caller's context, a first call with an
// already-canceled context could abort compilation and cache the error
// permanently, poisoning the engine for every later caller. loadEngine builds
// under a background context to prevent that.
//
// This test deliberately does NOT warm the engine first, so when run in
// isolation (e.g. -run TestToHTMLContext_CancellationDoesNotPoisonRuntime) the
// canceled call is the one that triggers initialization. A subsequent call on a
// healthy context must still succeed.
func TestToHTMLContext_CancellationDoesNotPoisonRuntime(t *testing.T) {
	// Canceled context first (possibly the very first call into the package).
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ToHTMLContext(ctx, slowInput()); err == nil {
		t.Fatalf("expected canceled context to error")
	}
	// Healthy call must still work afterward: the shared engine is not poisoned.
	out, err := ToHTML("# ok")
	if err != nil {
		t.Fatalf("runtime poisoned after canceled call: %v", err)
	}
	if !strings.Contains(out, "<h1") {
		t.Fatalf("unexpected output after recovery: %q", out)
	}
}

// TestMemoryCapEnforced is the Finding 3 regression guard: an input whose
// processing would grow guest memory past the configured cap must fail
// gracefully (a returned error) rather than OOM-killing the host. The 200 MiB
// input drives the guest past the 512 MiB (8192-page) limit while reading
// stdin, so memory.grow fails inside the guest and the engine exits non-zero.
//
// The assertion is on the *contract* (an error is returned, the host survives,
// the call returns), not on an exact error string, since the precise failure
// point can shift with engine changes. With the default 4 GiB ceiling this
// input would instead try to allocate hundreds of MiB on the host unchecked.
func TestMemoryCapEnforced(t *testing.T) {
	// Low-CPU but memory-hungry: a large run of plain bytes the engine must
	// buffer. Sized above the 512 MiB cap once in-guest copies are accounted
	// for, but far below the 4 GiB default ceiling.
	src := strings.Repeat("a", 200<<20)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	out, err := ToHTMLContext(ctx, src)
	if err == nil {
		t.Fatalf("expected over-cap input to fail gracefully, got nil error (out len %d)", len(out))
	}
	// Must not be a context timeout: that would mean the run was CPU-bound and
	// never actually hit the memory cap, making this a false guard.
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("expected a memory-cap failure, got a context error (cap not exercised): %v", err)
	}
	if out != "" {
		t.Fatalf("expected no output on a failed render, got %d bytes", len(out))
	}
	// Sanity: the host is still alive and serving requests after the rejection.
	if _, err := ToHTML("# ok"); err != nil {
		t.Fatalf("host unusable after over-cap rejection: %v", err)
	}
}
