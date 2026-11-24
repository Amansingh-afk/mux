package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newReviewWorktree(t *testing.T) (repo, wt, branch string) {
	t.Helper()
	repo = newTestRepo(t)
	wt, branch, err := CreateWorktree(repo, "nova")
	if err != nil {
		t.Fatal(err)
	}
	return repo, wt, branch
}

func mustReview(t *testing.T, repo, wt string) Review {
	t.Helper()
	r, err := LoadReview(repo, wt)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestReviewAndLandIncludesCommitsEditsAndNewFiles(t *testing.T) {
	repo, wt, branch := newReviewWorktree(t)
	mustWrite(t, filepath.Join(wt, "committed.txt"), "c\n")
	mustGit(t, wt, "add", "-A")
	mustGit(t, wt, "commit", "-q", "-m", "c")
	mustWrite(t, filepath.Join(wt, "main.txt"), "edited\n")
	mustWrite(t, filepath.Join(wt, "sub", "new file.txt"), "a\nb")

	r := mustReview(t, repo, wt)
	if r.Commits != 1 || r.Loose != 2 || r.Ins != 4 || r.Del != 1 {
		t.Fatalf("review = commits %d loose %d +%d -%d", r.Commits, r.Loose, r.Ins, r.Del)
	}
	for _, want := range []string{"+c", "+edited", "sub/new file.txt", "+b"} {
		if !strings.Contains(r.Diff, want) {
			t.Errorf("diff lacks %q", want)
		}
	}
	if out := mustGit(t, wt, "diff", "--cached", "--name-only"); out != "" {
		t.Errorf("review touched the agent's index: %s", out)
	}
	if err := Land(repo, wt, branch, "nova", r); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"committed.txt", "sub/new file.txt"} {
		if _, err := os.Stat(filepath.Join(repo, f)); err != nil {
			t.Errorf("%s not landed", f)
		}
	}
	if !mustReview(t, repo, wt).Empty() {
		t.Error("review not empty after landing")
	}
}

func TestLandRefusesStaleReview(t *testing.T) {
	repo, wt, branch := newReviewWorktree(t)
	mustWrite(t, filepath.Join(wt, "new.txt"), "a\n")
	r := mustReview(t, repo, wt)
	mustWrite(t, filepath.Join(wt, "unseen.txt"), "x\n")
	if err := Land(repo, wt, branch, "nova", r); !errors.Is(err, ErrReviewStale) {
		t.Fatalf("err = %v, want stale", err)
	}
}

func TestLandRefusesDirtyBase(t *testing.T) {
	repo, wt, branch := newReviewWorktree(t)
	mustWrite(t, filepath.Join(wt, "new.txt"), "a\n")
	mustWrite(t, filepath.Join(repo, "main.txt"), "mine\n")
	if err := Land(repo, wt, branch, "nova", mustReview(t, repo, wt)); err == nil {
		t.Fatal("landed on a dirty base")
	}
	if out := mustGit(t, wt, "status", "--porcelain"); !strings.Contains(out, "?? new.txt") {
		t.Errorf("refusal changed the worktree: %s", out)
	}
}

func TestLandBacksOutOfConflicts(t *testing.T) {
	repo, wt, branch := newReviewWorktree(t)
	mustWrite(t, filepath.Join(wt, "main.txt"), "theirs\n")
	mustWrite(t, filepath.Join(repo, "main.txt"), "ours\n")
	mustGit(t, repo, "commit", "-qam", "ours")
	before := mustGit(t, repo, "rev-parse", "HEAD")
	err := Land(repo, wt, branch, "nova", mustReview(t, repo, wt))
	if err == nil || !strings.Contains(err.Error(), "main.txt") {
		t.Fatalf("err = %v, want conflict naming main.txt", err)
	}
	if mustGit(t, repo, "rev-parse", "HEAD") != before || mustGit(t, repo, "status", "--porcelain") != "" {
		t.Error("base not restored after conflict")
	}
}

func TestLandKeepsUserMergeInProgress(t *testing.T) {
	repo, wt, branch := newReviewWorktree(t)
	mustGit(t, repo, "checkout", "-qb", "side")
	mustGit(t, repo, "commit", "-q", "--allow-empty", "-m", "side")
	mustGit(t, repo, "checkout", "-q", "-")
	mustGit(t, repo, "commit", "-q", "--allow-empty", "-m", "base")
	mustGit(t, repo, "merge", "--no-commit", "--no-ff", "side")
	mustWrite(t, filepath.Join(wt, "new.txt"), "a\n")
	if err := Land(repo, wt, branch, "nova", mustReview(t, repo, wt)); err == nil {
		t.Fatal("landed during a merge")
	}
	mustGit(t, repo, "rev-parse", "--verify", "MERGE_HEAD")
}

func TestWorktreeRisk(t *testing.T) {
	repo, wt, branch := newReviewWorktree(t)
	if r := WorktreeRisk(repo, wt, branch); r.Any() {
		t.Fatalf("clean worktree at risk: %+v", r)
	}
	mustWrite(t, filepath.Join(wt, ".gitignore"), "out/\n")
	mustGit(t, wt, "add", "-A")
	mustGit(t, wt, "commit", "-q", "-m", "ignore")
	if r := WorktreeRisk(repo, wt, branch); r.Any() || r.Unmerged != 1 {
		t.Fatalf("branch commits survive deletion, want only Unmerged: %+v", r)
	}
	mustWrite(t, filepath.Join(wt, "out", "render.png"), "x")
	mustWrite(t, filepath.Join(wt, "new.txt"), "x")
	if r := WorktreeRisk(repo, wt, branch); r.Loose != 1 || r.Ignored != 1 {
		t.Fatalf("risk = %+v", r)
	}
	mustGit(t, wt, "checkout", "-q", "--detach")
	mustGit(t, wt, "commit", "-q", "--allow-empty", "-m", "floating")
	if r := WorktreeRisk(repo, wt, branch); r.Detached != 1 {
		t.Fatalf("detached = %+v", r)
	}
	if r := WorktreeRisk(repo, wt+"-gone", branch); r.Any() {
		t.Fatalf("missing worktree at risk: %+v", r)
	}
	if err := os.Remove(filepath.Join(wt, ".git")); err != nil {
		t.Fatal(err)
	}
	if r := WorktreeRisk(repo, wt, branch); !r.Unknown {
		t.Fatalf("broken worktree must count as risk: %+v", r)
	}
}
