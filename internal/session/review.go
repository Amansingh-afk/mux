package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// review.go is the git side of the review overlay: read everything an agent
// changed in its worktree, land it on the base repo's branch, and report what
// would be lost by deleting the worktree.

const (
	// mergeTimeout is long because commits and merges run the user's hooks.
	mergeTimeout = 5 * time.Minute
	// maxReviewBytes caps the diff text kept for display.
	maxReviewBytes = 4 << 20
	// maxUntrackedBytes caps how much of one untracked file is read.
	maxUntrackedBytes = 512 << 10
	// maxUntrackedRead caps how many untracked files are read.
	maxUntrackedRead = 200
)

// ErrReviewStale means the worktree or the base moved after it was reviewed.
var ErrReviewStale = errors.New("changed since you opened the review; press r to reload")

// gitRun runs git in dir and returns stdout only, so warnings on stderr can't
// be parsed as data. stderr goes into the error. Never prompts.
func gitRun(dir string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir, "-c", "core.quotepath=off"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true", "GIT_OPTIONAL_LOCKS=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		err = fmt.Errorf("git %s: %w: %s", args[0], err, msg)
	}
	return stdout.String(), err
}

func gitLine(dir string, args ...string) (string, error) {
	out, err := gitRun(dir, gitTimeout, args...)
	return strings.TrimSpace(out), err
}

func countNUL(s string) int { return strings.Count(s, "\x00") }

// untracked lists wt's untracked, non-ignored files.
func untracked(wt string) ([]string, error) {
	out, err := gitRun(wt, gitTimeout, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(out, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}

// readUntracked reads a regular text file. ok is false for binaries, symlinks,
// oversized and unreadable files.
func readUntracked(path string) (text string, ok bool) {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxUntrackedBytes {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil || bytes.IndexByte(data, 0) >= 0 {
		return "", false
	}
	return string(data), true
}

func lineCount(s string) int {
	n := strings.Count(s, "\n")
	if s != "" && !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// Review is one snapshot of an agent's work.
type Review struct {
	Into     string // branch checked out in the base repo
	Head     string // worktree HEAD
	Commits  int    // commits not yet in the base repo's HEAD
	Loose    int    // uncommitted + untracked paths
	Ins, Del int
	Diff     string
	// Truncated is set when Diff doesn't show everything.
	Truncated bool
	// fingerprint covers the whole change and the base HEAD, so landing can
	// refuse anything that differs from what was shown.
	fingerprint string
}

// Empty reports whether there is nothing to land.
func (r Review) Empty() bool { return r.Commits == 0 && r.Loose == 0 }

// LoadReview snapshots everything in wt since it forked from repo's HEAD:
// commits, uncommitted edits and untracked files.
func LoadReview(repo, wt string) (Review, error) {
	var r Review
	var err error
	if r.Into, err = gitLine(repo, "symbolic-ref", "--short", "HEAD"); err != nil {
		return r, errors.New("the main repo is not on a branch")
	}
	base, err := gitLine(repo, "rev-parse", "HEAD")
	if err != nil {
		return r, err
	}
	if r.Head, err = gitLine(wt, "rev-parse", "HEAD"); err != nil {
		return r, err
	}
	mb, err := gitLine(wt, "merge-base", base, r.Head)
	if err != nil {
		return r, err
	}
	count, err := gitLine(wt, "rev-list", "--count", base+".."+r.Head)
	if err != nil {
		return r, err
	}
	if _, err := fmt.Sscan(count, &r.Commits); err != nil {
		return r, err
	}
	status, err := gitRun(wt, gitTimeout, "status", "--porcelain", "-z", "--no-renames", "--untracked-files=all")
	if err != nil {
		return r, err
	}
	r.Loose = countNUL(status)
	diff, err := gitRun(wt, gitTimeout, "diff", "--binary", "--no-color", "--no-ext-diff", "--no-textconv", mb)
	if err != nil {
		return r, err
	}
	files, err := untracked(wt)
	if err != nil {
		return r, err
	}
	if r.Ins, r.Del, err = DiffStat(repo, wt); err != nil {
		return r, err
	}

	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s", r.Into, base, r.Head, status, diff)
	var b strings.Builder
	b.WriteString(diff)
	for i, f := range files {
		path := filepath.Join(wt, f)
		if fi, serr := os.Lstat(path); serr == nil {
			fmt.Fprintf(h, "\x00%s\x00%d\x00%d\x00%s", f, fi.Size(), fi.ModTime().UnixNano(), fi.Mode())
		}
		fmt.Fprintf(&b, "diff --git a/%s b/%s\nnew file (untracked)\n", f, f)
		if i >= maxUntrackedRead || b.Len() > maxReviewBytes {
			r.Truncated = true
			b.WriteString("(content not shown)\n")
			continue
		}
		text, ok := readUntracked(path)
		if !ok {
			b.WriteString("(binary, link or large file: content not shown)\n")
			continue
		}
		if text != "" {
			for _, l := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
				b.WriteString("+" + l + "\n")
			}
		}
	}
	r.Diff = b.String()
	if len(r.Diff) > maxReviewBytes {
		r.Diff = r.Diff[:maxReviewBytes]
		r.Truncated = true
	}
	r.fingerprint = hex.EncodeToString(h.Sum(nil))
	return r, nil
}

// Land commits any loose work in wt and merges it into the branch checked out
// in repo. It refuses when repo has uncommitted tracked changes or a git
// operation in progress, when the worktree left its branch, or when anything
// changed since seen was loaded. A conflicting merge is aborted, leaving repo
// as it was.
func Land(repo, wt, branch, name string, seen Review) error {
	for _, ref := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "REBASE_HEAD"} {
		if _, err := gitLine(repo, "rev-parse", "--verify", "--quiet", ref); err == nil {
			return fmt.Errorf("the main repo has an operation in progress (%s); finish it first", ref)
		}
	}
	dirty, err := gitLine(repo, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return err
	}
	if dirty != "" {
		return errors.New("the main repo has uncommitted changes; commit or stash them first")
	}
	if cur, err := gitLine(wt, "symbolic-ref", "--short", "HEAD"); err != nil || cur != branch {
		return fmt.Errorf("the worktree is not on %s; merge it by hand", branch)
	}
	now, err := LoadReview(repo, wt)
	if err != nil {
		return err
	}
	if now.fingerprint != seen.fingerprint {
		return ErrReviewStale
	}
	if now.Empty() {
		return errors.New("nothing to merge")
	}
	if now.Loose > 0 {
		if _, err := gitRun(wt, mergeTimeout, "add", "-A"); err != nil {
			return err
		}
		if _, err := gitRun(wt, mergeTimeout, "commit", "-q", "-m", "work from "+name); err != nil {
			return fmt.Errorf("could not commit the agent's work (nothing merged): %w", err)
		}
	}
	head, err := gitLine(wt, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	_, merr := gitRun(repo, mergeTimeout, "merge", "--no-edit", "--commit", "--no-squash",
		"-m", fmt.Sprintf("Merge branch '%s' into %s", branch, now.Into), head)
	if merr == nil {
		return nil
	}
	conflicts, _ := gitLine(repo, "diff", "--name-only", "--diff-filter=U")
	if _, err := gitLine(repo, "rev-parse", "--verify", "--quiet", "MERGE_HEAD"); err == nil {
		if _, aerr := gitRun(repo, mergeTimeout, "merge", "--abort"); aerr != nil {
			return fmt.Errorf("merge failed and could not be backed out, check %s: %w", repo, aerr)
		}
	}
	if conflicts != "" {
		return fmt.Errorf("conflicts, nothing merged: %s", strings.ReplaceAll(conflicts, "\n", ", "))
	}
	return merr
}

// Risk is what deleting a worktree would destroy.
type Risk struct {
	Loose    int  // uncommitted + untracked paths
	Ignored  int  // ignored paths (builds, renders, .env)
	Detached int  // commits reachable from the worktree HEAD but no branch
	Unmerged int  // commits on the branch not in the base; the branch is kept
	Unknown  bool // git couldn't tell
}

// Any reports whether deleting the worktree loses files or commits.
func (r Risk) Any() bool { return r.Loose > 0 || r.Ignored > 0 || r.Detached > 0 || r.Unknown }

func (r Risk) String() string {
	var parts []string
	add := func(n int, one, many string) {
		if n == 1 {
			parts = append(parts, "1 "+one)
		} else if n > 1 {
			parts = append(parts, fmt.Sprintf("%d %s", n, many))
		}
	}
	add(r.Loose, "uncommitted file", "uncommitted files")
	add(r.Ignored, "ignored file or folder", "ignored files or folders")
	add(r.Detached, "commit on no branch", "commits on no branch")
	if r.Unknown {
		parts = append(parts, "contents git could not check")
	}
	return strings.Join(parts, ", ")
}

// WorktreeRisk inspects wt before deletion. A worktree directory that no
// longer exists has nothing to lose; any git failure counts as risk.
func WorktreeRisk(repo, wt, branch string) Risk {
	var r Risk
	if _, err := os.Stat(wt); errors.Is(err, os.ErrNotExist) {
		return r
	}
	// a broken worktree link must not resolve to some enclosing repo.
	top, err := gitLine(wt, "rev-parse", "--show-toplevel")
	want, werr := filepath.EvalSymlinks(wt)
	if err != nil || werr != nil || top != want {
		r.Unknown = true
		return r
	}
	out, err := gitRun(wt, gitTimeout, "status", "--porcelain", "-z", "--no-renames", "--untracked-files=all", "--ignored")
	if err != nil {
		r.Unknown = true
		return r
	}
	for _, e := range strings.Split(out, "\x00") {
		switch {
		case e == "":
		case strings.HasPrefix(e, "!! "):
			r.Ignored++
		default:
			r.Loose++
		}
	}
	scan := func(n *int, args ...string) {
		s, err := gitLine(wt, append([]string{"rev-list", "--count"}, args...)...)
		if err != nil {
			r.Unknown = true
			return
		}
		_, _ = fmt.Sscan(s, n)
	}
	scan(&r.Detached, "HEAD", "--not", "--branches")
	if branch != "" && branchExists(repo, branch) {
		if base, err := gitLine(repo, "rev-parse", "HEAD"); err == nil {
			scan(&r.Unmerged, base+"..refs/heads/"+branch)
		}
	}
	return r
}
