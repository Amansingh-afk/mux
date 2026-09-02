package session

import (
	"os/exec"
	"sort"

	"github.com/Amansingh-afk/mux/internal/config"
)

// ApplyConfig merges user config into the builtin provider table. Call once
// at startup, after package init has populated Providers.
//
// Known provider names (already in Providers): cfg.Args are APPENDED to the
// builtin Args; Cmd/Hint/Color/ResumeFlag/SessionArg/PreArgs replace the
// builtin value only when present in the file (nil pointer = keep builtin).
// A changed Cmd re-runs exec.LookPath to refresh Available.
//
// Unknown names: a new Provider is built from the config entry (Cmd is
// validated as required by config.Load), Available gated by exec.LookPath,
// and the name inserted into providerOrder just before "shell".
func ApplyConfig(cfg config.Config) {
	names := make([]string, 0, len(cfg.Providers))
	for name := range cfg.Providers {
		names = append(names, name)
	}
	sort.Strings(names) // deterministic order for new-provider insertion

	for _, name := range names {
		pc := cfg.Providers[name]
		if name == "shell" {
			continue // not overridable; config.Load rejects it anyway
		}
		if p, ok := Providers[name]; ok {
			if pc.Cmd != nil && *pc.Cmd != "" && *pc.Cmd != p.Cmd {
				p.Cmd = *pc.Cmd
				_, err := exec.LookPath(p.Cmd)
				p.Available = err == nil
			}
			if pc.Hint != nil {
				p.Hint = *pc.Hint
			}
			if pc.Color != nil {
				p.Color = *pc.Color
			}
			if pc.ResumeFlag != nil {
				p.ResumeFlag = *pc.ResumeFlag
			}
			if pc.SessionArg != nil {
				p.SessionArg = *pc.SessionArg
			}
			if pc.PreArgs != nil {
				p.PreArgs = *pc.PreArgs
			}
			if len(pc.Args) > 0 {
				p.Args = append(append([]string{}, p.Args...), pc.Args...)
			}
			Providers[name] = p
			continue
		}
		// New provider.
		p := Provider{Name: name, Args: pc.Args}
		if pc.Cmd != nil {
			p.Cmd = *pc.Cmd
		}
		if pc.Hint != nil {
			p.Hint = *pc.Hint
		}
		if pc.Color != nil {
			p.Color = *pc.Color
		}
		if pc.ResumeFlag != nil {
			p.ResumeFlag = *pc.ResumeFlag
		}
		if pc.SessionArg != nil {
			p.SessionArg = *pc.SessionArg
		}
		if pc.PreArgs != nil {
			p.PreArgs = *pc.PreArgs
		}
		if p.Cmd == "" {
			continue // defensive; config.Load requires cmd for new providers
		}
		_, err := exec.LookPath(p.Cmd)
		p.Available = err == nil
		Providers[name] = p
		insertBeforeShell(name)
	}
}

// insertBeforeShell adds name to providerOrder just before "shell"
// (appending if "shell" is absent). No-op if name is already listed.
func insertBeforeShell(name string) {
	if containsString(providerOrder, name) {
		return
	}
	for i, n := range providerOrder {
		if n == "shell" {
			providerOrder = append(providerOrder[:i],
				append([]string{name}, providerOrder[i:]...)...)
			return
		}
	}
	providerOrder = append(providerOrder, name)
}
