package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig points XDG_CONFIG_HOME at a temp dir and writes the given
// content as mux/config.toml inside it.
func writeConfig(t *testing.T, content string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "mux"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mux", "config.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestConfigLoadValid(t *testing.T) {
	writeConfig(t, `
[general]
discovery_roots = ["/abs/root"]
native_status = false
quick_nav = false

[worktree]
setup = "pnpm i"

[providers.claude]
args = ["--model", "opus"]

[providers.aider]
cmd = "aider"
args = ["--watch-files"]
hint = "pip install aider-chat"
color = "39"
resume_flag = ["--restore-chat-history"]
session_arg = ""
`)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", cfg.Warnings)
	}
	if cfg.NativeStatus {
		t.Error("native_status=false not honored")
	}
	if cfg.QuickNav {
		t.Error("quick_nav=false not honored")
	}
	if cfg.WorktreeSetup != "pnpm i" {
		t.Errorf("worktree setup = %q", cfg.WorktreeSetup)
	}
	if len(cfg.DiscoveryRoots) != 1 || cfg.DiscoveryRoots[0] != "/abs/root" {
		t.Errorf("discovery_roots = %v", cfg.DiscoveryRoots)
	}
	cl, ok := cfg.Providers["claude"]
	if !ok {
		t.Fatal("missing providers.claude")
	}
	if len(cl.Args) != 2 || cl.Args[0] != "--model" || cl.Args[1] != "opus" {
		t.Errorf("claude args = %v", cl.Args)
	}
	if cl.Cmd != nil {
		t.Errorf("claude cmd should be unset, got %q", *cl.Cmd)
	}
	ai, ok := cfg.Providers["aider"]
	if !ok {
		t.Fatal("missing providers.aider")
	}
	if ai.Cmd == nil || *ai.Cmd != "aider" {
		t.Errorf("aider cmd = %v", ai.Cmd)
	}
	if ai.Hint == nil || *ai.Hint != "pip install aider-chat" {
		t.Errorf("aider hint = %v", ai.Hint)
	}
	if ai.Color == nil || *ai.Color != "39" {
		t.Errorf("aider color = %v", ai.Color)
	}
	if ai.ResumeFlag == nil || len(*ai.ResumeFlag) != 1 || (*ai.ResumeFlag)[0] != "--restore-chat-history" {
		t.Errorf("aider resume_flag = %v", ai.ResumeFlag)
	}
	// session_arg = "" is explicitly set (non-nil) but empty.
	if ai.SessionArg == nil || *ai.SessionArg != "" {
		t.Errorf("aider session_arg = %v, want explicit empty", ai.SessionArg)
	}
}

func TestConfigLoadMissingFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // no mux/config.toml inside
	cfg, err := Load()
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if !cfg.NativeStatus {
		t.Error("NativeStatus should default true")
	}
	if !cfg.QuickNav {
		t.Error("QuickNav should default true")
	}
	if len(cfg.DiscoveryRoots) != 0 || len(cfg.Providers) != 0 || len(cfg.Warnings) != 0 {
		t.Errorf("expected zero-value config, got %+v", cfg)
	}
}

func TestConfigLoadMalformed(t *testing.T) {
	writeConfig(t, "[general]\ndiscovery_roots = [\"unclosed\n")
	_, err := Load()
	if err == nil {
		t.Fatal("malformed TOML should error")
	}
	// BurntSushi parse errors carry "line N" position info.
	if !strings.Contains(err.Error(), "line") {
		t.Errorf("error should include position, got: %v", err)
	}
}

func TestConfigLoadUnknownKeyWarns(t *testing.T) {
	writeConfig(t, `
[general]
native_status = true
mystery_knob = 3

[providers.claude]
args = ["--model", "opus"]
typo_key = "x"
`)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unknown keys must not fail load: %v", err)
	}
	if len(cfg.Warnings) != 2 {
		t.Fatalf("want 2 warnings, got %d: %v", len(cfg.Warnings), cfg.Warnings)
	}
	joined := strings.Join(cfg.Warnings, "\n")
	if !strings.Contains(joined, "mystery_knob") || !strings.Contains(joined, "typo_key") {
		t.Errorf("warnings should name unknown keys: %v", cfg.Warnings)
	}
}

func TestConfigTildeExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeConfig(t, `
[general]
discovery_roots = ["~/work", "~", "/abs", "relative"]
`)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{filepath.Join(home, "work"), home, "/abs", "relative"}
	if len(cfg.DiscoveryRoots) != len(want) {
		t.Fatalf("roots = %v, want %v", cfg.DiscoveryRoots, want)
	}
	for i := range want {
		if cfg.DiscoveryRoots[i] != want[i] {
			t.Errorf("roots[%d] = %q, want %q", i, cfg.DiscoveryRoots[i], want[i])
		}
	}
}

func TestConfigRejectsShellOverride(t *testing.T) {
	writeConfig(t, `
[providers.shell]
cmd = "fish"
`)
	_, err := Load()
	if err == nil {
		t.Fatal("[providers.shell] override should be rejected")
	}
	if !strings.Contains(err.Error(), "shell") {
		t.Errorf("error should mention shell, got: %v", err)
	}
}

func TestConfigNewProviderRequiresCmd(t *testing.T) {
	writeConfig(t, `
[providers.aider]
args = ["--watch-files"]
`)
	_, err := Load()
	if err == nil {
		t.Fatal("new provider without cmd should be rejected")
	}
	if !strings.Contains(err.Error(), "cmd") {
		t.Errorf("error should mention cmd, got: %v", err)
	}
}

func TestConfigColorValidation(t *testing.T) {
	for _, bad := range []string{"256", "-1", "banana", "#12345", "#gggggg"} {
		writeConfig(t, "[providers.claude]\ncolor = \""+bad+"\"\n")
		if _, err := Load(); err == nil {
			t.Errorf("color %q should be rejected", bad)
		}
	}
	for _, good := range []string{"0", "255", "39", "#fff", "#FF00aa"} {
		writeConfig(t, "[providers.claude]\ncolor = \""+good+"\"\n")
		if _, err := Load(); err != nil {
			t.Errorf("color %q should be accepted: %v", good, err)
		}
	}
}
