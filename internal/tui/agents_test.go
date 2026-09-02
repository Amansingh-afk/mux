package tui

import (
	"path/filepath"
	"strings"
	"testing"
)

// Two projects with the same basename but different paths must derive
// distinct session-id fragments — deriving from the display name (basename)
// alone made mux_api_claude_1 collide across ~/work/api and ~/personal/api.
func TestProjectFragmentDistinctForSameBasename(t *testing.T) {
	a := projectFragment("/home/u/work/api")
	b := projectFragment("/home/u/personal/api")
	if a == b {
		t.Fatalf("fragments collide for equal basenames: %q == %q", a, b)
	}
	for _, f := range []string{a, b} {
		if !strings.HasPrefix(f, "api_") {
			t.Errorf("fragment %q should start with basename %q + underscore", f, "api")
		}
	}
}

func TestProjectFragmentStable(t *testing.T) {
	p := filepath.Join("/home", "u", "work", "api")
	if projectFragment(p) != projectFragment(p) {
		t.Fatal("fragment derivation is not deterministic for the same path")
	}
}
