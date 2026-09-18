package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// gitTimeout bounds every git call so a hung object store / network remote
// can't wedge the caller.
const gitTimeout = 30 * time.Second

// gitOut runs `git -C dir args...` with a timeout and returns combined output.
func gitOut(dir string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	full := append([]string{"-C", dir}, args...)
	out, err := exec.CommandContext(ctx, "git", full...).CombinedOutput()
	return string(out), err
}

// WorktreeRoot returns the base directory under which mux places all agent
// worktrees: $XDG_DATA_HOME/mux/worktrees, falling back to
// ~/.local/share/mux/worktrees when XDG_DATA_HOME is unset.
func WorktreeRoot() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "mux", "worktrees")
	}
	return filepath.Join(home(), ".local", "share", "mux", "worktrees")
}

// IsGitRepo reports whether dir is inside a git work tree.
func IsGitRepo(dir string) bool {
	out, err := gitOut(dir, gitTimeout, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// repoSlug produces a stable, collision-resistant directory name for a repo:
// sanitized basename plus a short hash of the absolute path, so two checkouts
// named "api" in different parents don't share a worktree bucket.
func repoSlug(repo string) string {
	abs, err := filepath.Abs(repo)
	if err != nil {
		abs = repo
	}
	sum := sha256.Sum256([]byte(abs))
	return sanitize(filepath.Base(abs)) + "-" + hex.EncodeToString(sum[:])[:8]
}

// branchExists reports whether refs/heads/<branch> exists in repo.
func branchExists(repo, branch string) bool {
	_, err := gitOut(repo, gitTimeout, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// CreateWorktree creates a new git worktree for an agent at
// <WorktreeRoot()>/<repo slug>/<codename> on a NEW branch mux/<codename>
// forked from the repo's current HEAD. If that branch already exists (stale
// from a forgotten agent), suffixed names mux/<codename>-2 .. -9 are tried;
// the worktree directory carries the same suffix. Returns the worktree path
// and the branch name actually used.
func CreateWorktree(repo, codename string) (string, string, error) {
	bucket := filepath.Join(WorktreeRoot(), repoSlug(repo))
	if err := os.MkdirAll(bucket, 0o755); err != nil {
		return "", "", fmt.Errorf("worktree root: %w", err)
	}
	for n := 1; n <= 9; n++ {
		name := codename
		if n > 1 {
			name = fmt.Sprintf("%s-%d", codename, n)
		}
		branch := "mux/" + name
		wt := filepath.Join(bucket, name)
		if branchExists(repo, branch) {
			continue
		}
		if _, err := os.Stat(wt); err == nil {
			continue
		}
		out, err := gitOut(repo, gitTimeout, "worktree", "add", wt, "-b", branch)
		if err != nil {
			return "", "", fmt.Errorf("git worktree add: %w: %s", err, strings.TrimSpace(out))
		}
		return wt, branch, nil
	}
	return "", "", fmt.Errorf("worktree for %q: all branch names mux/%s..mux/%s-9 taken", codename, codename, codename)
}

// RemoveWorktree removes an agent worktree: `git worktree remove --force`;
// if that fails (directory already gone), `git worktree prune` clears the
// stale registration. When deleteBranch is set, the branch is force-deleted —
// only pass deleteBranch when the branch is merged or the user confirmed.
// Best-effort throughout; the first real error is returned.
func RemoveWorktree(repo, path, branch string, deleteBranch bool) error {
	var firstErr error
	if _, err := gitOut(repo, gitTimeout, "worktree", "remove", "--force", path); err != nil {
		// directory may already be gone; prune clears the stale registration.
		if pout, perr := gitOut(repo, gitTimeout, "worktree", "prune"); perr != nil {
			firstErr = fmt.Errorf("git worktree prune: %w: %s", perr, strings.TrimSpace(pout))
		}
	}
	if deleteBranch && branch != "" {
		if out, err := gitOut(repo, gitTimeout, "branch", "-D", branch); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("git branch -D %s: %w: %s", branch, err, strings.TrimSpace(out))
		}
	}
	return firstErr
}

// BranchMerged reports whether branch's tip is an ancestor of the repo's
// current HEAD (i.e. fully merged).
func BranchMerged(repo, branch string) bool {
	_, err := gitOut(repo, gitTimeout, "merge-base", "--is-ancestor", branch, "HEAD")
	return err == nil
}

// CopyWorktreeInclude copies gitignored files the agent still needs (.env,
// local config, secrets) from the base repo into a fresh worktree, driven by
// <repo>/.worktreeinclude. A missing file is a no-op.
//
// Each non-empty, non-comment (#) line is a pattern, matched against the
// repo's GITIGNORED entries only (git ls-files --others --ignored
// --exclude-standard --directory), so tracked files are never copied even
// when a pattern matches them. Matching is gitignore-style-lite:
//
//   - a pattern without "/" matches the entry's basename at any depth
//     (path.Match), like gitignore — ".env" matches ".env" and "sub/.env"
//   - a pattern containing "/" is root-anchored and path.Match'ed against
//     the full repo-relative path ("/only-root.env" ≡ "only-root.env")
//   - a trailing "/" marks a directory pattern: it matches only ignored
//     directories, whose whole tree is copied
//   - negation ("!") lines are not implemented; each produces a warning
//     and is skipped
//
// Matched files and directory trees are copied to wt preserving relative
// path and file mode. Returns the repo-relative paths copied plus warnings.
func CopyWorktreeInclude(repo, wt string) (copied []string, warnings []string, err error) {
	data, rerr := os.ReadFile(filepath.Join(repo, ".worktreeinclude"))
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("read .worktreeinclude: %w", rerr)
	}
	var patterns []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "!") {
			warnings = append(warnings, fmt.Sprintf(".worktreeinclude: negation pattern %q not supported, skipped", line))
			continue
		}
		patterns = append(patterns, line)
	}
	if len(patterns) == 0 {
		return nil, warnings, nil
	}

	out, gerr := gitOut(repo, gitTimeout, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory")
	if gerr != nil {
		return nil, warnings, fmt.Errorf("git ls-files: %w: %s", gerr, strings.TrimSpace(out))
	}

	seen := map[string]bool{}
	for _, entry := range strings.Split(out, "\x00") {
		if entry == "" {
			continue
		}
		isDir := strings.HasSuffix(entry, "/")
		rel := strings.TrimSuffix(entry, "/")
		if !matchInclude(patterns, rel, isDir) {
			continue
		}
		if isDir {
			cerr := copyTree(repo, wt, rel, seen, &copied)
			if cerr != nil {
				return copied, warnings, cerr
			}
			continue
		}
		if cerr := copyRel(repo, wt, rel, seen, &copied); cerr != nil {
			return copied, warnings, cerr
		}
	}
	return copied, warnings, nil
}

// matchInclude reports whether the repo-relative entry (isDir when it names
// an ignored directory) matches any .worktreeinclude pattern.
func matchInclude(patterns []string, rel string, isDir bool) bool {
	for _, p := range patterns {
		dirPat := strings.HasSuffix(p, "/")
		p = strings.TrimSuffix(p, "/")
		if dirPat && !isDir {
			continue
		}
		anchored := strings.Contains(p, "/")
		p = strings.TrimPrefix(p, "/")
		var target string
		if anchored {
			target = rel
		} else {
			target = path.Base(rel)
		}
		if ok, merr := path.Match(p, target); merr == nil && ok {
			return true
		}
	}
	return false
}

// copyTree copies the whole ignored directory `rel` from repo into wt.
func copyTree(repo, wt, rel string, seen map[string]bool, copied *[]string) error {
	src := filepath.Join(repo, filepath.FromSlash(rel))
	return filepath.WalkDir(src, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		sub, rerr := filepath.Rel(repo, p)
		if rerr != nil {
			return rerr
		}
		return copyRel(repo, wt, filepath.ToSlash(sub), seen, copied)
	})
}

// copyRel copies one regular file at repo-relative path rel into wt,
// preserving mode. Non-regular files (sockets, symlinks) are skipped.
func copyRel(repo, wt, rel string, seen map[string]bool, copied *[]string) error {
	if seen[rel] {
		return nil
	}
	src := filepath.Join(repo, filepath.FromSlash(rel))
	info, serr := os.Lstat(src)
	if serr != nil {
		return serr
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	dst := filepath.Join(wt, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, ierr := os.Open(src)
	if ierr != nil {
		return ierr
	}
	defer in.Close()
	out, oerr := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if oerr != nil {
		return oerr
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	// re-chmod: umask may have narrowed the OpenFile perm.
	if err := os.Chmod(dst, info.Mode().Perm()); err != nil {
		return err
	}
	seen[rel] = true
	*copied = append(*copied, rel)
	return nil
}
