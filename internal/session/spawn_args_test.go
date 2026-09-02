package session

import (
	"reflect"
	"testing"
)

func TestBuildSpawnArgv(t *testing.T) {
	claude := Provider{
		Name: "claude", Cmd: "claude",
		ResumeFlag: []string{"--resume"},
		SessionArg: "--resume",
	}
	codex := Provider{
		Name: "codex", Cmd: "codex",
		ResumeFlag: []string{"resume", "--last"},
		SessionArg: "resume",
	}
	cursor := Provider{
		Name: "cursor", Cmd: "cursor-agent",
		ResumeFlag: []string{"resume"},
		SessionArg: "--chat-id",
	}
	gemini := Provider{
		Name: "gemini", Cmd: "gemini",
		ResumeFlag: []string{"--continue"},
	}
	withExtras := func(p Provider) Provider {
		p.PreArgs = []string{"-c", "notify=hook"}
		p.Args = []string{"--model", "opus"}
		return p
	}

	tests := []struct {
		name string
		p    Provider
		r    ResumeOpts
		want []string
	}{
		{
			name: "claude fresh no extras",
			p:    claude,
			want: []string{"claude"},
		},
		{
			name: "claude fresh with args and preargs",
			p:    withExtras(claude),
			want: []string{"claude", "-c", "notify=hook", "--model", "opus"},
		},
		{
			name: "claude resume with uuid no extras",
			p:    claude,
			r:    ResumeOpts{Enabled: true, UUID: "abc-123"},
			// SessionArg overlaps ResumeFlag: uuid appended as value, flag not re-emitted.
			want: []string{"claude", "--resume", "abc-123"},
		},
		{
			name: "claude resume with uuid with extras keeps Args",
			p:    withExtras(claude),
			r:    ResumeOpts{Enabled: true, UUID: "abc-123"},
			want: []string{"claude", "-c", "notify=hook", "--resume", "abc-123", "--model", "opus"},
		},
		{
			name: "claude resume without uuid no extras",
			p:    claude,
			r:    ResumeOpts{Enabled: true},
			want: []string{"claude", "--resume"},
		},
		{
			name: "claude resume without uuid with extras keeps Args",
			p:    withExtras(claude),
			r:    ResumeOpts{Enabled: true},
			want: []string{"claude", "-c", "notify=hook", "--resume", "--model", "opus"},
		},
		{
			name: "codex fresh",
			p:    codex,
			want: []string{"codex"},
		},
		{
			name: "codex resume subcommand ordering no uuid",
			p:    codex,
			r:    ResumeOpts{Enabled: true},
			want: []string{"codex", "resume", "--last"},
		},
		{
			name: "codex resume with uuid",
			p:    codex,
			r:    ResumeOpts{Enabled: true, UUID: "u-1"},
			// "resume" is both ResumeFlag element and SessionArg: uuid appended as value.
			want: []string{"codex", "resume", "--last", "u-1"},
		},
		{
			name: "codex resume with preargs before subcommand",
			p:    Provider{Name: "codex", Cmd: "codex", PreArgs: []string{"-c", "notify=x"}, ResumeFlag: []string{"resume", "--last"}, SessionArg: "resume"},
			r:    ResumeOpts{Enabled: true},
			want: []string{"codex", "-c", "notify=x", "resume", "--last"},
		},
		{
			name: "cursor resume distinct session arg re-emits flag",
			p:    cursor,
			r:    ResumeOpts{Enabled: true, UUID: "chat-9"},
			want: []string{"cursor-agent", "resume", "--chat-id", "chat-9"},
		},
		{
			name: "gemini resume no session arg ignores uuid",
			p:    gemini,
			r:    ResumeOpts{Enabled: true, UUID: "ignored"},
			want: []string{"gemini", "--continue"},
		},
		{
			name: "resume enabled but no resume flag falls through to args",
			p:    Provider{Name: "aider", Cmd: "aider", Args: []string{"--watch-files"}},
			r:    ResumeOpts{Enabled: true},
			want: []string{"aider", "--watch-files"},
		},
		{
			name: "shell provider empty cmd yields nil",
			p:    Provider{Name: "shell", Cmd: ""},
			want: nil,
		},
		{
			name: "shell provider resume still nil",
			p:    Provider{Name: "shell", Cmd: "", ResumeFlag: []string{"--x"}},
			r:    ResumeOpts{Enabled: true, UUID: "u"},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSpawnArgv(tt.p, tt.r)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildSpawnArgv() = %v, want %v", got, tt.want)
			}
		})
	}
}
