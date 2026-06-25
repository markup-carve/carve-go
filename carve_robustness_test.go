package carve

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// quadraticInput is a Carve source that the engine processes in O(n^2): each
// "[a](" opens a link the parser must keep scanning for. At this size it takes
// several seconds (often 10s+) to run to completion, which makes it a reliable
// stand-in for any CPU-bound input that out-runs a per-call deadline.
func quadraticInput() string { return strings.Repeat("[a](", 10000) }

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
	// render, never the cold-start compilation (see warmEngine).
	warmEngine(t)

	src := quadraticInput()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
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
	// not because it ran to completion. The uninterrupted parse of this input
	// takes ~10s natively and ~40s+ under -race; wazero's context-done check is
	// periodic, so interrupt latency varies with machine load (and is much
	// larger under -race). A 20s ceiling unambiguously proves the run was cut
	// short rather than completed, while tolerating that periodic-check jitter.
	if elapsed > 20*time.Second {
		t.Fatalf("deadline did not interrupt: took %v (uninterrupted run is far longer)", elapsed)
	}
}

// TestToHTMLContext_CanceledInterrupts asserts an already-canceled context is
// honored: the render must not run to completion.
func TestToHTMLContext_CanceledInterrupts(t *testing.T) {
	warmEngine(t)

	src := quadraticInput()

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
	// See the latency note in TestToHTMLContext_DeadlineInterrupts: the ceiling
	// proves interruption, not completion, with headroom for -race jitter.
	if elapsed > 20*time.Second {
		t.Fatalf("cancellation did not interrupt: took %v", elapsed)
	}
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
// shared-runtime model: a canceled or deadline-exceeded call closes only that
// call's fresh module instance, not the shared runtime. A subsequent call on a
// healthy context must still succeed. (wazero builds the runtime once via
// engineOnce; per-call cancellation must not leave it unusable.)
func TestToHTMLContext_CancellationDoesNotPoisonRuntime(t *testing.T) {
	// Canceled context first.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ToHTMLContext(ctx, quadraticInput()); err == nil {
		t.Fatalf("expected canceled context to error")
	}
	// Healthy call must still work afterward.
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
