package carve

import (
	"context"
	"fmt"
)

// DecisionTrace returns the engine's block decision trace for source, as
// `carve trace` prints it: JSON lines, a header line naming the trace format
// version and then one record per source line from the ownership walk.
//
// The trace explains how the engine assigned each line to a block. It is a
// debugging aid; its record fields follow the engine's trace format version,
// not this package's API.
//
// Uses context.Background(); prefer DecisionTraceContext for untrusted input.
func DecisionTrace(source string) (string, error) {
	return DecisionTraceContext(context.Background(), source)
}

// DecisionTraceContext is DecisionTrace with a caller-supplied context.
func DecisionTraceContext(ctx context.Context, source string) (string, error) {
	eng, err := loadEngine()
	if err != nil {
		return "", err
	}
	out, code, err := runEngine(ctx, eng, []string{"carve", "trace", "-"}, source)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("carve: engine exited with code %d: %s", code, out.stderr)
	}
	return out.stdout, nil
}
