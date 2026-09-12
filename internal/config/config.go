// Package config loads mux's optional user configuration from
// $XDG_CONFIG_HOME/mux/config.toml (default ~/.config/mux/config.toml).
//
// Schema:
//
//	[general]
//	discovery_roots = ["~/work", "~/oss"] # extra repo-scan roots, ~ expanded
//	native_status = true                  # master switch for native status probes
//
//	[providers.claude]          # known provider: overlay onto builtin spec
//	args = ["--model", "opus"]  # APPENDED to builtin Args
//
//	[providers.aider]           # unknown provider: defines a new entry
//	cmd = "aider"               # required for new providers
//	args = ["--watch-files"]
//	hint = "pip install aider-chat"
//	color = "39"
//	resume_flag = ["--restore-chat-history"]
//	session_arg = ""
//	pre_args = []               # root flags emitted immediately after cmd
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// ProviderConfig is one [providers.<name>] table. Pointer fields distinguish
// "not present" (nil → keep builtin value) from "explicitly set" (including
// set to empty). Args is a plain slice because it is always appended, never
// an override.
type ProviderConfig struct {
	Cmd        *string   `toml:"cmd"`
	Args       []string  `toml:"args"`
	PreArgs    *[]string `toml:"pre_args"`
	Hint       *string   `toml:"hint"`
	Color      *string   `toml:"color"`
	ResumeFlag *[]string `toml:"resume_flag"`
	SessionArg *string   `toml:"session_arg"`
}

// Config is the loaded, validated configuration.
type Config struct {
	// DiscoveryRoots are extra repo-scan roots, with ~ already expanded.
	DiscoveryRoots []string
	// NativeStatus is the master switch for native status probes.
	// Defaults to true when the file or key is absent.
	NativeStatus bool
	// QuickNav is the master switch for the prefixless alt-key tmux
	// bindings (the whole alt keymap: nav, actions, M-Space). With it off,
	// alt chords still work while mux's pane has keyboard focus — the
	// terminal delivers them directly. Defaults to true.
	QuickNav bool
	// WorktreeSetup is a shell command run inside every freshly created
	// agent worktree before the agent starts (deps install etc). "" = none.
	WorktreeSetup string
	// Providers maps provider name → overlay/new-provider spec.
	Providers map[string]ProviderConfig
	// Warnings collects non-fatal issues (e.g. unknown keys).
	Warnings []string
}

// fileConfig mirrors the on-disk TOML shape.
type fileConfig struct {
	General struct {
		DiscoveryRoots []string `toml:"discovery_roots"`
		NativeStatus   *bool    `toml:"native_status"`
		QuickNav       *bool    `toml:"quick_nav"`
	} `toml:"general"`
	Worktree struct {
		Setup string `toml:"setup"`
	} `toml:"worktree"`
	Providers map[string]ProviderConfig `toml:"providers"`
}

// knownProviders are the builtin provider names an overlay may omit `cmd`
// for. Must stay in sync with internal/session's builtin specs ("shell" is
// deliberately excluded: it is not overridable).
var knownProviders = map[string]bool{
	"claude": true,
	"codex":  true,
	"gemini": true,
	"cursor": true,
}

// Path returns the config file location, honoring $XDG_CONFIG_HOME.
func Path() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "mux", "config.toml")
}

// Load reads and validates the config file. A missing file is not an error:
// it returns the zero-value Config (NativeStatus true). Malformed TOML
// returns an error including the parse position. Unknown keys do not fail
// the load; they are collected into Config.Warnings.
func Load() (Config, error) {
	cfg := Config{NativeStatus: true, QuickNav: true}
	path := Path()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("config: read %s: %w", path, err)
	}

	var fc fileConfig
	md, err := toml.Decode(string(data), &fc)
	if err != nil {
		// toml.ParseError.Error() already includes line/column position.
		return cfg, fmt.Errorf("config: parse %s: %w", path, err)
	}

	for _, k := range md.Undecoded() {
		cfg.Warnings = append(cfg.Warnings,
			fmt.Sprintf("unknown key %q in %s", k.String(), path))
	}

	if fc.General.NativeStatus != nil {
		cfg.NativeStatus = *fc.General.NativeStatus
	}
	if fc.General.QuickNav != nil {
		cfg.QuickNav = *fc.General.QuickNav
	}
	cfg.WorktreeSetup = fc.Worktree.Setup
	for _, r := range fc.General.DiscoveryRoots {
		cfg.DiscoveryRoots = append(cfg.DiscoveryRoots, expandTilde(r))
	}
	cfg.Providers = fc.Providers

	if err := validate(cfg); err != nil {
		return cfg, fmt.Errorf("config: %s: %w", path, err)
	}
	return cfg, nil
}

func validate(cfg Config) error {
	names := make([]string, 0, len(cfg.Providers))
	for name := range cfg.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		pc := cfg.Providers[name]
		if name == "shell" {
			return fmt.Errorf("[providers.shell] may not be overridden")
		}
		if !knownProviders[name] && (pc.Cmd == nil || strings.TrimSpace(*pc.Cmd) == "") {
			return fmt.Errorf("[providers.%s]: new provider requires cmd", name)
		}
		if pc.Color != nil && *pc.Color != "" && !validColor(*pc.Color) {
			return fmt.Errorf("[providers.%s]: color %q must be an ANSI 256 code (0-255) or hex (#rgb / #rrggbb)", name, *pc.Color)
		}
	}
	return nil
}

// validColor accepts the forms lipgloss.Color understands: an ANSI 256
// palette index ("0".."255") or a hex color ("#rgb" / "#rrggbb").
func validColor(s string) bool {
	if n, err := strconv.Atoi(s); err == nil {
		return n >= 0 && n <= 255
	}
	if strings.HasPrefix(s, "#") && (len(s) == 4 || len(s) == 7) {
		for _, r := range s[1:] {
			switch {
			case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
			default:
				return false
			}
		}
		return true
	}
	return false
}

// expandTilde replaces a leading "~" or "~/" with the user's home directory.
func expandTilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return p
		}
		return filepath.Join(home, p[1:])
	}
	return p
}
