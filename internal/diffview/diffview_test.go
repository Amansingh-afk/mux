package diffview

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const multiFileDiff = `diff --git a/cmd/root.go b/cmd/root.go
index 1234567..89abcde 100644
--- a/cmd/root.go
+++ b/cmd/root.go
@@ -10,7 +10,8 @@ func main() {
 	a := 1
-	b := 2
+	b := 3
+	c := 4
 	fmt.Println(a, b)
@@ -30,3 +31,3 @@ func other() {
 	x := 9
-	y := old()
+	y := new()
 	use(x, y)
diff --git a/api.go b/api.go
index aaa..bbb 100644
--- a/api.go
+++ b/api.go
@@ -1,2 +1,1 @@
-package apiold
-// gone
+package api
`

func TestParseUnifiedMultiFile(t *testing.T) {
	files := parseUnified(multiFileDiff, SectionCommitted)
	if len(files) != 2 {
		t.Fatalf("want 2 files, got %d", len(files))
	}

	f := files[0]
	if f.Path != "cmd/root.go" || f.Status != 'M' {
		t.Errorf("file0: path=%q status=%c", f.Path, f.Status)
	}
	if len(f.Hunks) != 2 {
		t.Fatalf("file0: want 2 hunks, got %d", len(f.Hunks))
	}
	if f.Adds != 3 || f.Dels != 2 {
		t.Errorf("file0: adds=%d dels=%d, want 3/2", f.Adds, f.Dels)
	}
	if !strings.HasPrefix(f.Hunks[0].Header, "@@ -10,7 +10,8 @@") {
		t.Errorf("hunk header: %q", f.Hunks[0].Header)
	}

	// Line numbers: hunk 1 starts old=10 new=10.
	h := f.Hunks[0]
	if len(h.Lines) != 5 {
		t.Fatalf("hunk0: want 5 lines, got %d", len(h.Lines))
	}
	checks := []struct {
		kind         LineKind
		oldNo, newNo int
	}{
		{LineContext, 10, 10},
		{LineDel, 11, 0},
		{LineAdd, 0, 11},
		{LineAdd, 0, 12},
		{LineContext, 12, 13},
	}
	for i, c := range checks {
		got := h.Lines[i]
		if got.Kind != c.kind || got.OldNo != c.oldNo || got.NewNo != c.newNo {
			t.Errorf("line %d: kind=%d old=%d new=%d, want %d/%d/%d",
				i, got.Kind, got.OldNo, got.NewNo, c.kind, c.oldNo, c.newNo)
		}
	}

	g := files[1]
	if g.Path != "api.go" || g.Adds != 1 || g.Dels != 2 {
		t.Errorf("file1: path=%q adds=%d dels=%d", g.Path, g.Adds, g.Dels)
	}
	if g.Section != SectionCommitted {
		t.Errorf("file1 section = %v", g.Section)
	}
}

const newFileDiff = `diff --git a/notes.txt b/notes.txt
new file mode 100644
index 0000000..3b18e51
--- /dev/null
+++ b/notes.txt
@@ -0,0 +1,2 @@
+hello
+world
\ No newline at end of file
`

func TestParseUnifiedNewFile(t *testing.T) {
	files := parseUnified(newFileDiff, SectionUntracked)
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	f := files[0]
	if f.Status != 'A' {
		t.Errorf("status = %c, want A", f.Status)
	}
	if f.Path != "notes.txt" {
		t.Errorf("path = %q", f.Path)
	}
	if f.Adds != 2 || f.Dels != 0 {
		t.Errorf("adds=%d dels=%d, want 2/0", f.Adds, f.Dels)
	}
	// "\ No newline" must not become a content line.
	if n := len(f.Hunks[0].Lines); n != 2 {
		t.Errorf("hunk lines = %d, want 2", n)
	}
	if f.Hunks[0].Lines[0].NewNo != 1 || f.Hunks[0].Lines[1].NewNo != 2 {
		t.Errorf("new line numbers wrong: %+v", f.Hunks[0].Lines)
	}
}

const binaryDiff = `diff --git a/img.png b/img.png
new file mode 100644
index 0000000..9f1a2b3
Binary files /dev/null and b/img.png differ
diff --git a/readme.md b/readme.md
index aaa..bbb 100644
--- a/readme.md
+++ b/readme.md
@@ -1 +1 @@
-old
+new
`

func TestParseUnifiedBinary(t *testing.T) {
	files := parseUnified(binaryDiff, SectionCommitted)
	if len(files) != 2 {
		t.Fatalf("want 2 files, got %d", len(files))
	}
	if !files[0].Binary {
		t.Errorf("img.png not marked binary")
	}
	if files[0].Status != 'A' {
		t.Errorf("img.png status = %c, want A", files[0].Status)
	}
	if files[1].Binary {
		t.Errorf("readme.md wrongly marked binary")
	}
	if files[1].Adds != 1 || files[1].Dels != 1 {
		t.Errorf("readme.md counts: +%d −%d", files[1].Adds, files[1].Dels)
	}
}

const deletedFileDiff = `diff --git a/gone.go b/gone.go
deleted file mode 100644
index abc..000
--- a/gone.go
+++ /dev/null
@@ -1,2 +0,0 @@
-package gone
-// bye
`

func TestParseUnifiedDeletedFile(t *testing.T) {
	files := parseUnified(deletedFileDiff, SectionCommitted)
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	f := files[0]
	if f.Status != 'D' || f.Path != "gone.go" || f.Dels != 2 || f.Adds != 0 {
		t.Errorf("got status=%c path=%q adds=%d dels=%d", f.Status, f.Path, f.Adds, f.Dels)
	}
}

func TestParseNumstat(t *testing.T) {
	out := "12\t3\tcmd/root.go\n0\t7\tapi.go\n-\t-\timg.png\n5\t0\tdir with space/f.go\n"
	m := parseNumstat(out)
	if len(m) != 4 {
		t.Fatalf("want 4 entries, got %d: %v", len(m), m)
	}
	if e := m["cmd/root.go"]; e.Adds != 12 || e.Dels != 3 || e.Binary {
		t.Errorf("cmd/root.go: %+v", e)
	}
	if e := m["api.go"]; e.Adds != 0 || e.Dels != 7 {
		t.Errorf("api.go: %+v", e)
	}
	if e := m["img.png"]; !e.Binary {
		t.Errorf("img.png not binary: %+v", e)
	}
	if e := m["dir with space/f.go"]; e.Adds != 5 {
		t.Errorf("spaced path: %+v", e)
	}
}

func TestParseMergeTree(t *testing.T) {
	clean := parseMergeTree("ee75a3bf1d03740ec05bdb6a771a407e3aaa10ee\n", 0)
	if !clean.Supported || !clean.Clean || len(clean.Conflicts) != 0 {
		t.Errorf("clean: %+v", clean)
	}

	out := "ee75a3bf1d03740ec05bdb6a771a407e3aaa10ee\na.txt\nb/c.txt\n\nAuto-merging a.txt\nCONFLICT (content): Merge conflict in a.txt\n"
	conf := parseMergeTree(out, 1)
	if !conf.Supported || conf.Clean {
		t.Errorf("conflict flags: %+v", conf)
	}
	want := []string{"a.txt", "b/c.txt"}
	if len(conf.Conflicts) != len(want) {
		t.Fatalf("conflicts = %v, want %v", conf.Conflicts, want)
	}
	for i := range want {
		if conf.Conflicts[i] != want[i] {
			t.Errorf("conflict %d = %q, want %q", i, conf.Conflicts[i], want[i])
		}
	}
}

// gitT runs git in dir, failing the test on error.
func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t.t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.t",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPredictMergeRealRepo engineers a real conflict (and a clean branch)
// in a temp repo and checks predictMerge against live git.
func TestPredictMergeRealRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	gitT(t, dir, "init", "-q", "-b", "main")
	writeT(t, filepath.Join(dir, "a.txt"), "base\n")
	writeT(t, filepath.Join(dir, "b.txt"), "keep\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-qm", "base")

	// conflicting branch: both sides edit a.txt
	gitT(t, dir, "checkout", "-qb", "feature")
	writeT(t, filepath.Join(dir, "a.txt"), "feature side\n")
	gitT(t, dir, "commit", "-aqm", "feature edit")
	gitT(t, dir, "checkout", "-q", "main")

	// clean branch: only adds a new file
	gitT(t, dir, "checkout", "-qb", "clean")
	writeT(t, filepath.Join(dir, "c.txt"), "new\n")
	gitT(t, dir, "add", "c.txt")
	gitT(t, dir, "commit", "-qm", "clean add")
	gitT(t, dir, "checkout", "-q", "main")

	writeT(t, filepath.Join(dir, "a.txt"), "main side\n")
	gitT(t, dir, "commit", "-aqm", "main edit")

	conf := predictMerge(dir, "feature")
	if !conf.Supported {
		t.Skip("git too old for merge-tree --write-tree")
	}
	if conf.Clean {
		t.Errorf("feature should conflict: %+v", conf)
	}
	found := false
	for _, c := range conf.Conflicts {
		if c == "a.txt" {
			found = true
		}
	}
	if !found {
		t.Errorf("a.txt missing from conflicts: %v", conf.Conflicts)
	}

	cln := predictMerge(dir, "clean")
	if !cln.Supported || !cln.Clean || len(cln.Conflicts) != 0 {
		t.Errorf("clean branch prediction: %+v", cln)
	}
}

// TestGatherRealRepo exercises the full gather pipeline against a temp
// base repo + worktree with committed, uncommitted, and untracked changes.
func TestGatherRealRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	base := t.TempDir()
	gitT(t, base, "init", "-q", "-b", "main")
	writeT(t, filepath.Join(base, "a.txt"), "one\ntwo\n")
	gitT(t, base, "add", ".")
	gitT(t, base, "commit", "-qm", "base")

	wt := filepath.Join(t.TempDir(), "wt")
	gitT(t, base, "worktree", "add", "-q", "-b", "mux/test", wt)
	writeT(t, filepath.Join(wt, "a.txt"), "one\nTWO\n")
	gitT(t, wt, "commit", "-aqm", "edit a")
	// uncommitted edit + untracked file
	writeT(t, filepath.Join(wt, "a.txt"), "one\nTWO\nthree\n")
	writeT(t, filepath.Join(wt, "fresh.txt"), "brand new\n")

	d, err := gather(base, wt, "mux/test")
	if err != nil {
		t.Fatal(err)
	}
	if d.ahead != 1 {
		t.Errorf("ahead = %d, want 1", d.ahead)
	}
	var sections []Section
	for _, f := range d.files {
		sections = append(sections, f.Section)
	}
	if len(d.files) != 3 {
		t.Fatalf("want 3 files (committed, uncommitted, untracked), got %d: %v", len(d.files), sections)
	}
	if d.files[0].Section != SectionCommitted || d.files[1].Section != SectionUncommitted || d.files[2].Section != SectionUntracked {
		t.Errorf("section order wrong: %v", sections)
	}
	if d.files[2].Path != "fresh.txt" || d.files[2].Status != 'A' || d.files[2].Adds != 1 {
		t.Errorf("untracked file: %+v", d.files[2])
	}
	if d.merge.Supported && !d.merge.Clean {
		t.Errorf("merge should predict clean: %+v", d.merge)
	}
}

func TestSummaryText(t *testing.T) {
	cases := []struct {
		files, adds, dels, ahead, unc int
		want                          string
	}{
		{3, 142, 18, 1, 2, "3 files · +142 −18 · 1 commit ahead · 2 uncommitted"},
		{1, 5, 0, 0, 0, "1 file · +5 −0"},
		{2, 10, 4, 3, 0, "2 files · +10 −4 · 3 commits ahead"},
		{0, 0, 0, 0, 1, "0 files · +0 −0 · 1 uncommitted"},
	}
	for _, c := range cases {
		got := summaryText(c.files, c.adds, c.dels, c.ahead, c.unc)
		if got != c.want {
			t.Errorf("summaryText(%d,%d,%d,%d,%d) = %q, want %q",
				c.files, c.adds, c.dels, c.ahead, c.unc, got, c.want)
		}
	}
}
