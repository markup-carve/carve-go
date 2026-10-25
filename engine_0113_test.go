package carve

import (
	"strings"
	"testing"
)

func TestRust0113TaskStates(t *testing.T) {
	for _, marker := range []string{"/", "!", "_"} {
		source := "- [" + marker + "] task\n"
		html, err := ToHTML(source)
		if err != nil {
			t.Fatalf("ToHTML(%q): %v", source, err)
		}
		if !strings.Contains(html, `data-task-state="`+marker+`"`) {
			t.Errorf("task state %q missing from %q", marker, html)
		}
	}
}
