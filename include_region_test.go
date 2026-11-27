package carve

import (
	"context"
	"strings"
	"testing"
)

func TestSelectIncludeRegion(t *testing.T) {
	cases := []struct {
		name   string
		source string
		region string
		code   bool
		start  int
		end    int
		bad    bool
	}{
		{"carve markers", "intro\n%% #region setup\none\ntwo\n%% #endregion\n", "setup", false, 3, 4, false},
		{"empty region", "%% #region e\n%% #endregion\n", "e", false, 2, 1, false},
		{"code markers", "// #region setup\none\n// #endregion\n", "setup", true, 2, 2, false},
		{"code markers refused in Carve mode", "// #region setup\none\n// #endregion\n", "setup", false, 0, 0, true},
		{"missing region", "%% #region other\none\n%% #endregion\n", "setup", false, 0, 0, true},
		{"unclosed region", "%% #region setup\none\n", "setup", false, 0, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, end, err := SelectIncludeRegion(context.Background(), tc.source, tc.region, tc.code)
			if tc.bad {
				if err == nil || !strings.Contains(err.Error(), "include-region-invalid") {
					t.Fatalf("got %d, %d, %v; want include-region-invalid", start, end, err)
				}
				return
			}
			if err != nil || start != tc.start || end != tc.end {
				t.Fatalf("got %d, %d, %v; want %d, %d", start, end, err, tc.start, tc.end)
			}
		})
	}
}
