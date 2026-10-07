package carve

import (
	"strings"
	"testing"
)

// The spoiler construct renders differently depending on whether the spoiler
// extension is enabled, which is what makes it a usable probe for whether the
// caller's selection reached the engine.
const spoilerSource = "Plot: :spoiler[the butler did it].\n"

func TestExtensionSelectionReachesEngine(t *testing.T) {
	cases := []struct {
		name  string
		opts  Options
		want  string
		avoid string
	}{
		{
			name:  "no selection leaves extensions off",
			opts:  Options{},
			want:  `class="ext-spoiler"`,
			avoid: `class="spoiler"`,
		},
		{
			name:  "explicit subset enables only what was asked for",
			opts:  Options{Extensions: []string{"math-block"}},
			want:  `class="ext-spoiler"`,
			avoid: `class="spoiler"`,
		},
		{
			name: "the named extension is enabled",
			opts: Options{Extensions: []string{"spoiler"}},
			want: `class="spoiler"`,
		},
		{
			name: "several named extensions compose",
			opts: Options{Extensions: []string{"math-block", "spoiler"}},
			want: `class="spoiler"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Render(spoilerSource, OutputHTML, tc.opts)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("output does not contain %s:\n%s", tc.want, got)
			}
			if tc.avoid != "" && strings.Contains(got, tc.avoid) {
				t.Errorf("output unexpectedly contains %s:\n%s", tc.avoid, got)
			}
		})
	}
}

func TestStaticWithoutSelectionEnablesTheBundle(t *testing.T) {
	got, err := Render(spoilerSource, OutputHTML, Options{Static: true})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(got, "spoiler-revealed") {
		t.Errorf("Static alone should flatten the spoiler:\n%s", got)
	}
}

func TestStaticHonorsAnExplicitSelection(t *testing.T) {
	got, err := Render(spoilerSource, OutputHTML, Options{Static: true, Extensions: []string{"spoiler"}})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(got, "spoiler-revealed") {
		t.Errorf("Static with spoiler selected should flatten it:\n%s", got)
	}
}

func TestUnknownExtensionIsReported(t *testing.T) {
	_, err := Render("x\n", OutputHTML, Options{Extensions: []string{"nope-bogus"}})
	if err == nil {
		t.Fatal("expected an error for an unknown extension key")
	}
	if !strings.Contains(err.Error(), "nope-bogus") {
		t.Errorf("error should name the rejected key, got: %v", err)
	}
}

func TestRenderArgsPassesEachSelectedExtension(t *testing.T) {
	args, err := renderArgs(OutputHTML, Options{Extensions: []string{"spoiler", "tabs"}})
	if err != nil {
		t.Fatalf("renderArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	if want := "--extension spoiler --extension tabs"; !strings.Contains(joined, want) {
		t.Errorf("args %q do not contain %q", joined, want)
	}
	if strings.Contains(joined, "--extensions") {
		t.Errorf("an explicit selection must not send the whole bundle: %q", joined)
	}
}
