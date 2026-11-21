package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	colorful "github.com/lucasb-eyer/go-colorful"
)

// fx.go holds mux's decorative rendering: gradients and compact meters.
// Everything here is drawn in the mux pane only — never the agent pane — so
// it can't add flicker to agent output.

// Gradient endpoints: the pink hero fading into violet.
var (
	gradFrom, _ = colorful.Hex("#ff5fd7")
	gradTo, _   = colorful.Hex("#875fff")
)

// gradient colors s left to right from gradFrom to gradTo, per rune. Lines
// in a multi-line s share the same horizontal ramp so columns line up.
func gradient(s string, bold bool) string {
	lines := strings.Split(s, "\n")
	maxW := 0
	for _, l := range lines {
		if n := len([]rune(l)); n > maxW {
			maxW = n
		}
	}
	out := make([]string, len(lines))
	for li, l := range lines {
		var b strings.Builder
		for i, r := range []rune(l) {
			if r == ' ' {
				b.WriteRune(r)
				continue
			}
			t := 0.0
			if maxW > 1 {
				t = float64(i) / float64(maxW-1)
			}
			c := gradFrom.BlendLuv(gradTo, t).Clamped().Hex()
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Bold(bold).Render(string(r)))
		}
		out[li] = b.String()
	}
	return strings.Join(out, "\n")
}

// ruleCache memoizes gradientRule by width: the sidebar redraws on every
// spinner tick and the rule never changes.
var ruleCache = map[int]string{}

// gradientRule is a w-wide horizontal rule in the gradient.
func gradientRule(w int) string {
	if r, ok := ruleCache[w]; ok {
		return r
	}
	r := gradient(strings.Repeat("─", w), false)
	ruleCache[w] = r
	return r
}

// diffStatLine renders "+12 −3" in add/remove colors; "clean" when the
// agent hasn't changed anything yet.
func diffStatLine(ins, del int) string {
	if ins == 0 && del == 0 {
		return lipgloss.NewStyle().Foreground(colorDim).Render("clean")
	}
	return lipgloss.NewStyle().Foreground(colorOK).Render(fmt.Sprintf("+%d", ins)) + " " +
		lipgloss.NewStyle().Foreground(colorErr).Render(fmt.Sprintf("−%d", del))
}

// shortAge formats how long ago unix second ts was: 45s, 12m, 3h12m, 2d.
func shortAge(ts int64) string {
	if ts <= 0 {
		return ""
	}
	d := time.Since(time.Unix(ts, 0))
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
