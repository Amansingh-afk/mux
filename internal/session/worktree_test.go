package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mustGit runs `git -C dir args...`, failing the test on error.
func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newTestRepo builds a real git repo in a temp dir with one commit, and
// points XDG_DATA_HOME at a temp dir so WorktreeRoot lands there too.
// Global/system git config is neutralized for hermetic behavior.
func newTestRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	repo := t.TempDir()
	mustGit(t, repo, "init", "-q")
	mustGit(t, repo, "config", "user.email", "mux-test@example.com")
	mustGit(t, repo, "config", "user.name", "mux test")
	mustGit(t, repo, "config", "commit.gpgsign", "false")
	mustWrite(t, filepath.Join(repo, "main.txt"), "line1\n")
	mustGit(t, repo, "add", "-A")
	mustGit(t, repo, "commit", "-q", "-m", "init")
	return repo
}

func TestCreateWorktree(t *testing.T) {
	repo := newTestRepo(t)

	wt, branch, err := CreateWorktree(repo, "nova")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	if branch != "mux/nova" {
		t.Errorf("branch = %q, want mux/nova", branch)
	}
	if fi, serr := os.Stat(wt); serr != nil || !fi.IsDir() {
		t.Errorf("worktree path %q not a directory: %v", wt, serr)
	}
	if !strings.HasPrefix(wt, WorktreeRoot()) {
		t.Errorf("worktree %q not under WorktreeRoot %q", wt, WorktreeRoot())
	}
	if out := mustGit(t, repo, "branch", "--list", "mux/nova"); !strings.Contains(out, "mux/nova") {
		t.Errorf("branch mux/nova not listed: %s", out)
	}
	// tracked file materialized in the worktree
	if _, serr := os.Stat(filepath.Join(wt, "main.txt")); serr != nil {
		t.Errorf("main.txt missing in worktree: %v", serr)
	}

	// second create with the same codename → -2 suffix
	wt2, branch2, err := CreateWorktree(repo, "nova")
	if err != nil {
		t.Fatalf("CreateWorktree (second): %v", err)
	}
	if branch2 != "mux/nova-2" {
		t.Errorf("second branch = %q, want mux/nova-2", branch2)
	}
	if filepath.Base(wt2) != "nova-2" {
		t.Errorf("second worktree dir = %q, want nova-2", filepath.Base(wt2))
	}
}

func TestWorktreeDirty(t *testing.T) {
	repo := newTestRepo(t)
	wt, _, err := CreateWorktree(repo, "vega")
	if err != nil {
		t.Fatal(err)
	}
	if WorktreeDirty(wt) {
		t.Error("fresh worktree reported dirty")
	}
	mustWrite(t, filepath.Join(wt, "scratch.txt"), "hi\n")
	if !WorktreeDirty(wt) {
		t.Error("worktree with new file reported clean")
	}
}

func TestMergeAgentClean(t *testing.T) {
	repo := newTestRepo(t)
	wt, branch, err := CreateWorktree(repo, "lyra")
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(wt, "agent.txt"), "agent work\n")

	res := MergeAgent(repo, wt, branch)
	if res.Err != nil {
		t.Fatalf("MergeAgent: %v (out: %s)", res.Err, res.Out)
	}
	if res.Conflict {
		t.Fatalf("unexpected conflict (out: %s)", res.Out)
	}
	if _, serr := os.Stat(filepath.Join(repo, "agent.txt")); serr != nil {
		t.Errorf("agent.txt not present in base repo after merge: %v", serr)
	}
	log := mustGit(t, repo, "log", "--format=%s")
	if !strings.Contains(log, "mux: lyra wip") {
		t.Errorf("wip commit missing from base log:\n%s", log)
	}
}

func TestMergeAgentConflict(t *testing.T) {
	repo := newTestRepo(t)
	wt, branch, err := CreateWorktree(repo, "orion")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.Command("git", "-C", repo, "merge", "--abort").Run()
	})

	// same line edited + committed in base...
	mustWrite(t, filepath.Join(repo, "main.txt"), "base edit\n")
	mustGit(t, repo, "commit", "-aqm", "base change")
	// ...and edited (uncommitted; MergeAgent commits it) in the worktree
	mustWrite(t, filepath.Join(wt, "main.txt"), "agent edit\n")

	res := MergeAgent(repo, wt, branch)
	if res.Err == nil {
		t.Fatal("expected merge error, got nil")
	}
	if !res.Conflict {
		t.Errorf("Conflict = false, want true (out: %s)", res.Out)
	}
	if res.Out == "" {
		t.Error("Out empty, want merge output tail")
	}
}

func TestBranchMerged(t *testing.T) {
	repo := newTestRepo(t)
	wt, branch, err := CreateWorktree(repo, "atlas")
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(wt, "feature.txt"), "x\n")
	mustGit(t, wt, "add", "-A")
	mustGit(t, wt, "commit", "-qm", "feature")

	if BranchMerged(repo, branch) {
		t.Error("BranchMerged true before merge")
	}
	mustGit(t, repo, "merge", "--no-ff", "--no-edit", branch)
	if !BranchMerged(repo, branch) {
		t.Error("BranchMerged false after merge")
	}
}

func TestRemoveWorktree(t *testing.T) {
	repo := newTestRepo(t)
	wt, branch, err := CreateWorktree(repo, "frost")
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveWorktree(repo, wt, branch, true); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, serr := os.Stat(wt); !os.IsNotExist(serr) {
		t.Errorf("worktree dir still present: %v", serr)
	}
	if out := mustGit(t, repo, "branch", "--list", branch); strings.TrimSpace(out) != "" {
		t.Errorf("branch %s still listed: %s", branch, out)
	}
}

func TestCopyWorktreeInclude(t *testing.T) {
	repo := newTestRepo(t)

	mustWrite(t, filepath.Join(repo, ".gitignore"),
		".env\nconfig/local/\n*.secret\nonly-root.env\n")
	mustWrite(t, filepath.Join(repo, ".worktreeinclude"),
		"# agent needs these\n\n.env\nconfig/local/\n*.secret\n/only-root.env\n!negated.txt\n")
	// tracked files so parent dirs don't collapse in ls-files --directory,
	// plus a tracked file that matches a pattern (must NOT be copied).
	mustWrite(t, filepath.Join(repo, "sub", "tracked.txt"), "t\n")
	mustWrite(t, filepath.Join(repo, "config", "app.json"), "{}\n")
	mustWrite(t, filepath.Join(repo, "tracked.secret"), "committed\n")
	mustGit(t, repo, "add", "-A")
	mustGit(t, repo, "add", "-f", "tracked.secret")
	mustGit(t, repo, "commit", "-qm", "add ignore rules")

	// gitignored content
	mustWrite(t, filepath.Join(repo, ".env"), "ROOT=1\n")
	mustWrite(t, filepath.Join(repo, "sub", ".env"), "NESTED=1\n")
	mustWrite(t, filepath.Join(repo, "a.secret"), "s1\n")
	mustWrite(t, filepath.Join(repo, "sub", "b.secret"), "s2\n")
	mustWrite(t, filepath.Join(repo, "only-root.env"), "root-only\n")
	mustWrite(t, filepath.Join(repo, "sub", "only-root.env"), "nested, anchored pattern must skip\n")
	mustWrite(t, filepath.Join(repo, "config", "local", "settings.json"), "{\"local\":true}\n")
	mustWrite(t, filepath.Join(repo, "config", "local", "deep", "extra.json"), "{}\n")
	if err := os.Chmod(filepath.Join(repo, ".env"), 0o600); err != nil {
		t.Fatal(err)
	}

	wt, _, err := CreateWorktree(repo, "cipher")
	if err != nil {
		t.Fatal(err)
	}

	copied, warnings, err := CopyWorktreeInclude(repo, wt)
	if err != nil {
		t.Fatalf("CopyWorktreeInclude: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "!negated.txt") {
		t.Errorf("warnings = %v, want one negation warning", warnings)
	}

	got := map[string]bool{}
	for _, c := range copied {
		got[c] = true
	}
	want := []string{
		".env", "sub/.env",
		"a.secret", "sub/b.secret",
		"only-root.env",
		"config/local/settings.json", "config/local/deep/extra.json",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("expected %q in copied list, got %v", w, copied)
		}
		if _, serr := os.Stat(filepath.Join(wt, filepath.FromSlash(w))); serr != nil {
			t.Errorf("copied file %q missing in worktree: %v", w, serr)
		}
	}
	for _, skip := range []string{"sub/only-root.env", "tracked.secret"} {
		if got[skip] {
			t.Errorf("%q must not be in copied list", skip)
		}
	}
	// root-anchored pattern must not have copied the nested variant
	if _, serr := os.Stat(filepath.Join(wt, "sub", "only-root.env")); !os.IsNotExist(serr) {
		t.Errorf("nested only-root.env copied despite root-anchored pattern")
	}
	// tracked.secret exists in wt (checked out by git) but its content must
	// be the committed version, untouched by the copy.
	b, rerr := os.ReadFile(filepath.Join(wt, "tracked.secret"))
	if rerr != nil || string(b) != "committed\n" {
		t.Errorf("tracked.secret in wt = %q, %v; want committed content untouched", b, rerr)
	}
	// mode preserved
	if fi, serr := os.Stat(filepath.Join(wt, ".env")); serr != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf(".env mode = %v (%v), want 0600", fi.Mode(), serr)
	}
	// content preserved
	if b, rerr := os.ReadFile(filepath.Join(wt, "sub", ".env")); rerr != nil || string(b) != "NESTED=1\n" {
		t.Errorf("sub/.env content = %q, %v", b, rerr)
	}
}

func TestCopyWorktreeIncludeMissingFile(t *testing.T) {
	repo := newTestRepo(t)
	wt := t.TempDir()
	copied, warnings, err := CopyWorktreeInclude(repo, wt)
	if err != nil || copied != nil || warnings != nil {
		t.Errorf("missing .worktreeinclude: got (%v, %v, %v), want all nil", copied, warnings, err)
	}
}
