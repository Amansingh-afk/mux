package diffview

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Section identifies where a change lives relative to the agent branch.
type Section int

const (
	SectionCommitted Section = iota
	SectionUncommitted
	SectionUntracked
)

func (s Section) String() string {
	switch s {
	case SectionCommitted:
		return "committed"
	case SectionUncommitted:
		return "uncommitted"
	default:
		return "untracked"
	}
}

// LineKind classifies a single diff line.
type LineKind int

const (
	LineContext LineKind = iota
	LineAdd
	LineDel
)

// Line is one line of a hunk with resolved old/new line numbers
// (0 means "not present on that side").
type Line struct {
	Kind  LineKind
	Text  string // content without the leading +/-/space marker
	OldNo int
	NewNo int
}

// Hunk is one @@ block.
type Hunk struct {
	Header string // full "@@ -a,b +c,d @@ ctx" line
	Lines  []Line
}

// FileDiff is one file's worth of changes.
type FileDiff struct {
	Path    string // display path (new path; old path for deletions)
	OldPath string
	Status  byte // 'A' added, 'M' modified, 'D' deleted, 'R' renamed
	Binary  bool
	Section Section
	Hunks   []Hunk
	Adds    int
	Dels    int
}

var hunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// parseUnified parses `git diff` unified output into per-file diffs.
// It handles new-file (/dev/null) form, deletions, renames, and binary files.
func parseUnified(out string, section Section) []FileDiff {
	var files []FileDiff
	var cur *FileDiff
	var oldNo, newNo int
	var oldLeft, newLeft int // lines remaining per the @@ header counts
	inHunk := false

	flush := func() {
		if cur != nil {
			files = append(files, *cur)
			cur = nil
		}
	}

	for _, raw := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(raw, "diff --git "):
			flush()
			inHunk = false
			cur = &FileDiff{Status: 'M', Section: section}
			if a, b, ok := parseDiffGitPaths(raw); ok {
				cur.OldPath, cur.Path = a, b
			}
		case cur == nil:
			// preamble noise (shouldn't happen in git output)
		case !inHunk && strings.HasPrefix(raw, "new file mode"):
			cur.Status = 'A'
		case !inHunk && strings.HasPrefix(raw, "deleted file mode"):
			cur.Status = 'D'
		case !inHunk && strings.HasPrefix(raw, "rename from "):
			cur.Status = 'R'
			cur.OldPath = strings.TrimPrefix(raw, "rename from ")
		case !inHunk && strings.HasPrefix(raw, "rename to "):
			cur.Path = strings.TrimPrefix(raw, "rename to ")
		case !inHunk && (strings.HasPrefix(raw, "Binary files ") || raw == "GIT binary patch"):
			cur.Binary = true
		case !inHunk && strings.HasPrefix(raw, "--- "):
			if p := stripDiffPath(raw[4:]); p != "" {
				cur.OldPath = p
			} else {
				cur.Status = 'A' // --- /dev/null
			}
		case !inHunk && strings.HasPrefix(raw, "+++ "):
			if p := stripDiffPath(raw[4:]); p != "" {
				cur.Path = p
			} else {
				cur.Status = 'D' // +++ /dev/null
				cur.Path = cur.OldPath
			}
		case hunkRe.MatchString(raw):
			m := hunkRe.FindStringSubmatch(raw)
			oldNo, _ = strconv.Atoi(m[1])
			newNo, _ = strconv.Atoi(m[3])
			oldLeft, newLeft = 1, 1
			if m[2] != "" {
				oldLeft, _ = strconv.Atoi(m[2])
			}
			if m[4] != "" {
				newLeft, _ = strconv.Atoi(m[4])
			}
			cur.Hunks = append(cur.Hunks, Hunk{Header: raw})
			inHunk = true
		case inHunk && len(raw) > 0:
			h := &cur.Hunks[len(cur.Hunks)-1]
			switch raw[0] {
			case '+':
				h.Lines = append(h.Lines, Line{Kind: LineAdd, Text: raw[1:], NewNo: newNo})
				newNo++
				newLeft--
				cur.Adds++
			case '-':
				h.Lines = append(h.Lines, Line{Kind: LineDel, Text: raw[1:], OldNo: oldNo})
				oldNo++
				oldLeft--
				cur.Dels++
			case ' ':
				h.Lines = append(h.Lines, Line{Kind: LineContext, Text: raw[1:], OldNo: oldNo, NewNo: newNo})
				oldNo++
				newNo++
				oldLeft--
				newLeft--
			case '\\':
				// "\ No newline at end of file" — not a content line
			default:
				inHunk = false
			}
			if oldLeft <= 0 && newLeft <= 0 {
				inHunk = false
			}
		case inHunk && raw == "" && oldLeft > 0 && newLeft > 0:
			// blank context line whose trailing space was stripped in transit
			h := &cur.Hunks[len(cur.Hunks)-1]
			h.Lines = append(h.Lines, Line{Kind: LineContext, OldNo: oldNo, NewNo: newNo})
			oldNo++
			newNo++
			oldLeft--
			newLeft--
		}
	}
	flush()
	return files
}

// parseDiffGitPaths pulls old/new paths out of a "diff --git a/x b/y" line.
// Best-effort: paths with spaces are resolved later by the ---/+++ lines.
func parseDiffGitPaths(line string) (string, string, bool) {
	rest := strings.TrimPrefix(line, "diff --git ")
	parts := strings.Fields(rest)
	if len(parts) != 2 {
		return "", "", false
	}
	return stripDiffPath(parts[0]), stripDiffPath(parts[1]), true
}

// stripDiffPath strips the a/ or b/ prefix and quotes; returns "" for /dev/null.
func stripDiffPath(p string) string {
	p = strings.TrimSuffix(strings.TrimSpace(p), "\t")
	p = strings.Trim(p, `"`)
	if p == "/dev/null" {
		return ""
	}
	if strings.HasPrefix(p, "a/") || strings.HasPrefix(p, "b/") {
		return p[2:]
	}
	return p
}

// numstatEntry holds per-file counts from --numstat; Binary when git prints "-".
type numstatEntry struct {
	Adds, Dels int
	Binary     bool
}

// parseNumstat parses `git diff --numstat` output into path → counts.
func parseNumstat(out string) map[string]numstatEntry {
	m := make(map[string]numstatEntry)
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		path := strings.Trim(parts[2], `"`)
		// rename form: "old => new" or "{a => b}/rest"
		if i := strings.Index(path, " => "); i >= 0 && !strings.Contains(path, "{") {
			path = path[i+4:]
		}
		if parts[0] == "-" || parts[1] == "-" {
			m[path] = numstatEntry{Binary: true}
			continue
		}
		a, err1 := strconv.Atoi(parts[0])
		d, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil {
			continue
		}
		m[path] = numstatEntry{Adds: a, Dels: d}
	}
	return m
}

// MergeInfo is the merge prediction for base ← branch.
type MergeInfo struct {
	Supported bool // false when git is too old for merge-tree --write-tree
	Clean     bool
	Conflicts []string
	// Blocked lists base-tree untracked files the branch would overwrite —
	// git aborts such merges outright, and merge-tree does not predict it.
	Blocked []string
}

// parseMergeTree interprets `git merge-tree --write-tree --name-only` output.
// exit 0 → clean. exit 1 → line 1 is the tree OID, then one conflicted path
// per line until a blank line, then informational messages.
func parseMergeTree(out string, exit int) MergeInfo {
	if exit == 0 {
		return MergeInfo{Supported: true, Clean: true}
	}
	info := MergeInfo{Supported: true}
	seen := make(map[string]bool)
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if i == 0 {
			continue // tree OID
		}
		if strings.TrimSpace(line) == "" {
			break
		}
		if !seen[line] {
			seen[line] = true
			info.Conflicts = append(info.Conflicts, line)
		}
	}
	return info
}

// summaryText builds the plain-text header summary, e.g.
// "3 files · +142 −18 · 1 commit ahead · 2 uncommitted".
func summaryText(files, adds, dels, ahead, uncommitted int) string {
	parts := []string{
		fmt.Sprintf("%d %s", files, plural(files, "file", "files")),
		fmt.Sprintf("+%d −%d", adds, dels),
	}
	if ahead > 0 {
		parts = append(parts, fmt.Sprintf("%d %s ahead", ahead, plural(ahead, "commit", "commits")))
	}
	if uncommitted > 0 {
		parts = append(parts, fmt.Sprintf("%d uncommitted", uncommitted))
	}
	return strings.Join(parts, " · ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
