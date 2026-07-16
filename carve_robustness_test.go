package carve

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// slowInput is a Carve source whose full parse reliably out-runs the short
// per-call deadline used below, so it exercises deadline interruption of
// CPU-bound guest code. Each "[a](" opens a link destination the parser keeps
// scanning for; the engine handles this in linear time (an earlier O(n^2) was
// fixed upstream), so the size is chosen to keep the uninterrupted parse well
// above the 50ms deadline (~0.5s here) rather than relying on quadratic blowup.
func slowInput() string { return strings.Repeat("[a](", 100000) }

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

	src := slowInput()

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
	// not because it ran to completion. The uninterrupted parse of this input is
	// ~0.5s natively (more under -race), well above the 50ms deadline; wazero's
	// context-done check is periodic, so interrupt latency varies with machine
	// load (and is larger under -race). A 20s ceiling unambiguously proves the
	// run was cut short rather than completed, while tolerating that jitter.
	if elapsed > 20*time.Second {
		t.Fatalf("deadline did not interrupt: took %v (uninterrupted run is far longer)", elapsed)
	}
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
