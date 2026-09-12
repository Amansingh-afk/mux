package diffview

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const gitTimeout = 30 * time.Second

// data is everything the pager needs, gathered up front.
type data struct {
	branch string
	files  []FileDiff // ordered: committed, uncommitted, untracked
	ahead  int
	merge  MergeInfo
}

// gitOut runs git -C dir args... and returns (stdout, exitCode, err).
// err is non-nil only for real failures (git missing, timeout); a nonzero
// exit with output is returned as (out, code, nil) so callers can interpret.
func gitOut(dir string, args ...string) (string, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return "", -1, fmt.Errorf("git %s timed out after %s", strings.Join(args, " "), gitTimeout)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return stdout.String(), ee.ExitCode(), nil
	}
	if err != nil {
		return "", -1, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return stdout.String(), 0, nil
}

// gather collects committed, uncommitted, and untracked changes plus the
// ahead count and merge prediction. Individual failures degrade gracefully;
// an error is returned only when the committed diff itself cannot be read.
func gather(base, wt, branch string) (*data, error) {
	d := &data{branch: branch, merge: MergeInfo{Supported: false}}

	// Committed: base HEAD...branch
	out, code, err := gitOut(base, "diff", "--no-color", "HEAD..."+branch)
	if err != nil {
		return nil, err
	}
	if code != 0 && code != 1 { // diff exits 1 with --exit-code only; 0 normally
		return nil, fmt.Errorf("git diff HEAD...%s failed (exit %d)", branch, code)
	}
	committed := parseUnified(out, SectionCommitted)
	if ns, _, err := gitOut(base, "diff", "--numstat", "HEAD..."+branch); err == nil {
		applyNumstat(committed, parseNumstat(ns))
	}
	d.files = append(d.files, committed...)

	// Uncommitted: worktree vs its HEAD
	if out, code, err := gitOut(wt, "diff", "--no-color", "HEAD"); err == nil && (code == 0 || code == 1) {
		uncommitted := parseUnified(out, SectionUncommitted)
		if ns, _, err := gitOut(wt, "diff", "--numstat", "HEAD"); err == nil {
			applyNumstat(uncommitted, parseNumstat(ns))
		}
		d.files = append(d.files, uncommitted...)
	}

	// Untracked: each rendered as a full new-file diff via --no-index.
	if out, code, err := gitOut(wt, "ls-files", "--others", "--exclude-standard"); err == nil && code == 0 {
		for _, f := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			if f == "" {
				continue
			}
			// exit 1 is normal for --no-index when files differ
			diff, code, err := gitOut(wt, "diff", "--no-color", "--no-index", "/dev/null", f)
			if err != nil || (code != 0 && code != 1) {
				continue
			}
			for _, fd := range parseUnified(diff, SectionUntracked) {
				fd.Status = 'A'
				if fd.Path == "" {
					fd.Path = f
				}
				d.files = append(d.files, fd)
			}
		}
	}

	// Ahead count: commits on branch not on base HEAD.
	if out, code, err := gitOut(base, "rev-list", "--count", branch, "--not", "HEAD"); err == nil && code == 0 {
		if n, err := strconv.Atoi(strings.TrimSpace(out)); err == nil {
			d.ahead = n
		}
	}

	d.merge = predictMerge(base, branch)
	return d, nil
}

// predictMerge asks git whether merging branch into HEAD would be clean,
// without touching the index or worktree. Requires git >= 2.38 for
// merge-tree --write-tree; older git degrades to Supported=false.
func predictMerge(base, branch string) MergeInfo {
	info := predictMergeTree(base, branch)
	// merge-tree ignores the working tree: an untracked file in base that
	// the branch adds makes git abort the real merge ("would be overwritten
	// by merge"). Cross-check so the header warns about it up front.
	if info.Supported {
		info.Blocked = untrackedCollisions(base, branch)
		if len(info.Blocked) > 0 {
			info.Clean = false
		}
	}
	return info
}

// untrackedCollisions returns base-tree untracked files that the branch also
// touches (i.e. files git would refuse to overwrite on merge).
func untrackedCollisions(base, branch string) []string {
	changed, _, err := gitOut(base, "diff", "--name-only", "HEAD..."+branch)
	if err != nil {
		return nil
	}
	untracked, _, err := gitOut(base, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil
	}
	have := map[string]bool{}
	for _, f := range strings.Split(strings.TrimSpace(untracked), "\n") {
		if f != "" {
			have[f] = true
		}
	}
	var out []string
	for _, f := range strings.Split(strings.TrimSpace(changed), "\n") {
		if f != "" && have[f] {
			out = append(out, f)
		}
	}
	return out
}

func predictMergeTree(base, branch string) MergeInfo {
	out, code, err := gitOut(base, "merge-tree", "--write-tree", "--name-only", "HEAD", branch)
	if err == nil && (code == 0 || code == 1) {
		return parseMergeTree(out, code)
	}
	// --name-only may be missing on some versions; try without it. The
	// conflict detail lines differ but exit code semantics are the same.
	out, code, err = gitOut(base, "merge-tree", "--write-tree", "HEAD", branch)
	if err == nil && (code == 0 || code == 1) {
		info := parseMergeTreePlain(out, code)
		return info
	}
	return MergeInfo{Supported: false}
}

// parseMergeTreePlain handles merge-tree --write-tree output without
// --name-only: conflicted paths appear in stage lines
// "<mode> <oid> <stage>\t<path>" after the tree OID.
func parseMergeTreePlain(out string, exit int) MergeInfo {
	if exit == 0 {
		return MergeInfo{Supported: true, Clean: true}
	}
	info := MergeInfo{Supported: true}
	seen := make(map[string]bool)
	for i, line := range strings.Split(out, "\n") {
		if i == 0 {
			continue
		}
		if strings.TrimSpace(line) == "" {
			break
		}
		if tab := strings.IndexByte(line, '\t'); tab >= 0 {
			p := line[tab+1:]
			if !seen[p] {
				seen[p] = true
				info.Conflicts = append(info.Conflicts, p)
			}
		}
	}
	return info
}

// applyNumstat overlays authoritative counts from --numstat onto parsed
// files (binary files in particular have no countable hunks).
func applyNumstat(files []FileDiff, ns map[string]numstatEntry) {
	for i := range files {
		if e, ok := ns[files[i].Path]; ok && !e.Binary {
			files[i].Adds, files[i].Dels = e.Adds, e.Dels
		}
	}
}
