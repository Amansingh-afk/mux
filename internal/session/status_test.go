package session

import "testing"

// TestClassify locks the current Classify state machine ahead of the
// status-v2 refactor. Note: prevStatus is currently ignored by Classify.
func TestClassify(t *testing.T) {
	spinnerContent := "building...\n⠋ working"
	promptContent := "done.\n│ > │\n? for shortcuts"
	idleContent := "compiled 3 packages\nall tests passed"

	tests := []struct {
		name        string
		content     string
		prevHash    uint64
		prevStatus  Status
		stableTicks int
		wantStatus  Status
		wantTicks   int
	}{
		{
			name:        "hash changed -> active, ticks reset to 0",
			content:     "new output",
			prevHash:    Hash("old output"),
			prevStatus:  StatusIdle,
			stableTicks: 7,
			wantStatus:  StatusActive,
			wantTicks:   0,
		},
		{
			name:        "stable + spinner char + ticks<3 -> active (tick 1)",
			content:     spinnerContent,
			prevHash:    Hash(spinnerContent),
			prevStatus:  StatusActive,
			stableTicks: 0,
			wantStatus:  StatusActive,
			wantTicks:   1,
		},
		{
			name:        "stable + spinner char + ticks<3 -> active (tick 2)",
			content:     spinnerContent,
			prevHash:    Hash(spinnerContent),
			prevStatus:  StatusActive,
			stableTicks: 1,
			wantStatus:  StatusActive,
			wantTicks:   2,
		},
		{
			name: "stable + spinner but ticks reach 3 -> no longer active (falls to idle)",
			// spinner grace period ends at 3 stable ticks; this content has no
			// prompt so it classifies idle.
			content:     spinnerContent,
			prevHash:    Hash(spinnerContent),
			prevStatus:  StatusActive,
			stableTicks: 2,
			wantStatus:  StatusIdle,
			wantTicks:   3,
		},
		{
			name:        "stable + prompt-shaped tail -> waiting",
			content:     promptContent,
			prevHash:    Hash(promptContent),
			prevStatus:  StatusActive,
			stableTicks: 3,
			wantStatus:  StatusWaiting,
			wantTicks:   4,
		},
		{
			name:        "stable + no prompt -> idle",
			content:     idleContent,
			prevHash:    Hash(idleContent),
			prevStatus:  StatusActive,
			stableTicks: 5,
			wantStatus:  StatusIdle,
			wantTicks:   6,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotStatus, gotHash, gotTicks := Classify(tc.content, tc.prevHash, tc.prevStatus, tc.stableTicks)
			if gotStatus != tc.wantStatus {
				t.Errorf("status = %q, want %q", gotStatus, tc.wantStatus)
			}
			if gotHash != Hash(tc.content) {
				t.Errorf("hash = %d, want Hash(content) = %d", gotHash, Hash(tc.content))
			}
			if gotTicks != tc.wantTicks {
				t.Errorf("ticks = %d, want %d", gotTicks, tc.wantTicks)
			}
		})
	}
}

func TestHashStability(t *testing.T) {
	if Hash("abc") != Hash("abc") {
		t.Error("Hash is not deterministic")
	}
	if Hash("abc") == Hash("abd") {
		t.Error("Hash collision on trivially different inputs")
	}
}

func TestLooksWaiting(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name: "claude-code box prompt with chrome rendered below",
			content: "I finished the refactor.\n" +
				"╭──────────────────────────────╮\n" +
				"│ > Try \"fix the failing test\" │\n" +
				"╰──────────────────────────────╯\n" +
				"? for shortcuts",
			want: true,
		},
		{
			name: "box prompt followed by interrupt chrome",
			content: "some earlier output\n" +
				"│ > │\n" +
				"[Request interrupted]",
			want: true,
		},
		{
			name:    "simple > prompt with text after marker on last line",
			content: "output\n> run the tests",
			want:    true,
		},
		{
			name: "simple bare ❯ prompt followed by a blank-ish chrome line",
			// the prompt line keeps its trailing space because it is not the
			// final line of the capture.
			content: "output\n❯ \n? for shortcuts",
			want:    true,
		},
		{
			name:    "shell $ prompt with cursor cell after it",
			content: "make: done\n$ █",
			want:    true,
		},
		{
			name: "bare trailing '> ' prompt at very end of capture",
			// TrimRight eats the space after the marker; a bare marker alone
			// on the line must still count as a prompt.
			content: "output\n> ",
			want:    true,
		},
		{
			name:    "bare trailing '$ ' prompt at very end of capture",
			content: "build ok\n$ ",
			want:    true,
		},
		{
			name:    "plain streaming prose",
			content: "Reading files...\nAnalyzing the codebase structure\nThis may take a moment",
			want:    false,
		},
		{
			name:    "empty content",
			content: "",
			want:    false,
		},
		{
			name: "markdown blockquote deep in scrollback, active tail below",
			// blockquote is more than tailScan(8) non-trimmed lines above the
			// end, so the scan window never reaches it.
			content: "> this is a markdown quote\n" +
				"line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\n" +
				"still streaming output",
			want: false,
		},
		{
			name: "markdown blockquote within the last 8 lines",
			// NOTE(arguably wrong): a blockquote inside the tailScan window is
			// indistinguishable from a prompt to the current prefix matcher, so
			// this false-positives as waiting. Locking current behavior.
			content: "some output\n> quoted wisdom from the docs\nmore streaming text",
			want:    true,
		},
		{
			name:    "ANSI-colored prompt line still detected after stripping",
			content: "output\n\x1b[36m❯ \x1b[0mtype a message",
			want:    true,
		},
		{
			name:    "indented box prompt (leading whitespace trimmed)",
			content: "output\n   │ > │\n? for shortcuts",
			want:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksWaiting(tc.content); got != tc.want {
				t.Errorf("looksWaiting(%q) = %v, want %v", tc.content, got, tc.want)
			}
		})
	}
}

func TestStripANSI(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain text passthrough", "hello world", "hello world"},
		{"empty", "", ""},
		{"CSI SGR color", "\x1b[31mred\x1b[0m", "red"},
		{"CSI with multiple params", "\x1b[1;32;44mbold\x1b[0m done", "bold done"},
		{"CSI cursor movement", "\x1b[2Ahi\x1b[10;20H", "hi"},
		{"OSC terminated by BEL", "\x1b]0;window title\x07text", "text"},
		{"OSC terminated by ST", "\x1b]8;;https://x.test\x1b\\link", "link"},
		{"simple two-char escape", "\x1bM up-line", " up-line"},
		{"mixed sequences interleaved", "\x1b[36m❯ \x1b[0mrun", "❯ run"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripANSI(tc.in); got != tc.want {
				t.Errorf("stripANSI(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
