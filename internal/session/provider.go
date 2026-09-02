package session

import "os/exec"

type Provider struct {
	Name       string
	Cmd        string
	PreArgs    []string // root flags emitted immediately after Cmd in BOTH normal and resume spawns (e.g. codex -c notify=...)
	Args       []string
	Available  bool
	Hint       string   // install hint when missing
	ResumeFlag []string // cli flags to resume latest session; if UUID known, SessionArg is appended
	SessionArg string   // flag name that accepts a specific session UUID (e.g. --session-id)
	Color      string   // 256-color code for brand-tinted text
}

var Providers = map[string]Provider{}

var providerOrder = []string{"claude", "codex", "gemini", "cursor", "shell"}

func init() {
	specs := []Provider{
		{
			Name: "claude", Cmd: "claude",
			Hint:       "npm i -g @anthropic-ai/claude-code",
			ResumeFlag: []string{"--resume"},
			SessionArg: "--resume",
			Color:      "214", // amber
		},
		{
			Name: "codex", Cmd: "codex",
			Hint:       "npm i -g @openai/codex",
			ResumeFlag: []string{"resume", "--last"},
			SessionArg: "resume",
			Color:      "42", // green
		},
		{
			Name: "gemini", Cmd: "gemini",
			Hint:       "npm i -g @google/gemini-cli",
			ResumeFlag: []string{"--continue"},
			Color:      "75", // blue
		},
		{
			Name: "cursor", Cmd: "cursor-agent",
			Hint:       "install cursor-agent",
			ResumeFlag: []string{"resume"},
			SessionArg: "--chat-id",
			Color:      "141", // purple
		},
		{Name: "shell", Cmd: "", Color: "244"},
	}
	for _, p := range specs {
		if p.Cmd == "" {
			p.Available = true
		} else {
			_, err := exec.LookPath(p.Cmd)
			p.Available = err == nil
		}
		Providers[p.Name] = p
	}
}

func ProviderNames() []string {
	return providerOrder
}
