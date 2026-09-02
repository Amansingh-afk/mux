package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const testUUID = "550e8400-e29b-41d4-a716-446655440000"

func TestUUIDFromFilename(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		file     string
		want     string
	}{
		{
			name:     "claude style <uuid>.jsonl",
			provider: "claude",
			file:     testUUID + ".jsonl",
			want:     testUUID,
		},
		{
			name:     "codex rollout style",
			provider: "codex",
			file:     "rollout-2026-01-02T03-04-05-" + testUUID + ".jsonl",
			want:     testUUID,
		},
		{
			name:     "uppercase hex uuid accepted",
			provider: "claude",
			file:     "550E8400-E29B-41D4-A716-446655440000.jsonl",
			want:     "550E8400-E29B-41D4-A716-446655440000",
		},
		{
			name:     "garbage name -> empty",
			provider: "claude",
			file:     "notes.jsonl",
			want:     "",
		},
		{
			name:     "timestamp alone does not match",
			provider: "codex",
			file:     "rollout-2026-01-02T03-04-05.jsonl",
			want:     "",
		},
		{
			name:     "truncated uuid -> empty",
			provider: "claude",
			file:     "550e8400-e29b-41d4-a716.jsonl",
			want:     "",
		},
		{
			name:     "empty filename",
			provider: "claude",
			file:     "",
			want:     "",
		},
		{
			// provider arg is currently ignored: uuidRe handles both layouts.
			name:     "unknown provider still extracts uuid",
			provider: "somethingelse",
			file:     testUUID + ".jsonl",
			want:     testUUID,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := uuidFromFilename(tc.provider, tc.file); got != tc.want {
				t.Errorf("uuidFromFilename(%q, %q) = %q, want %q", tc.provider, tc.file, got, tc.want)
			}
		})
	}
}

func TestValidUUID(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"valid lowercase v4-ish", testUUID, true},
		{"valid uppercase", "550E8400-E29B-41D4-A716-446655440000", true},
		{"mixed case", "550e8400-E29B-41d4-A716-446655440000", true},
		{"all-digit hex still matches", "12345678-1234-1234-1234-123456789012", true},
		{"empty", "", false},
		{"plain word", "not-a-uuid", false},
		{"non-hex letters in groups", "550g8400-e29b-41d4-a716-446655440000", false},
		{"missing dashes", "550e8400e29b41d4a716446655440000", false},
		{"too-short last group", "550e8400-e29b-41d4-a716-44665544000", false},
		{
			name: "uuid embedded in surrounding junk rejected",
			in:   "xx" + testUUID + "yy",
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidUUID(tc.in); got != tc.want {
				t.Errorf("ValidUUID(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestClaudeSlug(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"unix abs path", "/home/user/proj", "-home-user-proj"},
		{"dots and underscores become dashes", "/a/b.c_d", "-a-b-c-d"},
		{"spaces become dashes", "/home/user/my project", "-home-user-my-project"},
		{"alnum preserved incl digits and case", "/Repos2/App", "-Repos2-App"},
		{"empty", "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeSlug(tc.in); got != tc.want {
				t.Errorf("claudeSlug(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSessionDirUnknownProvider(t *testing.T) {
	if got := SessionDir("no-such-provider", "/tmp"); got != "" {
		t.Errorf("SessionDir(unknown) = %q, want empty", got)
	}
}

// registerFakeProvider points a synthetic provider at dir and restores the
// providerSessionDir map entry on cleanup.
func registerFakeProvider(t *testing.T, name, dir string) {
	t.Helper()
	prev, existed := providerSessionDir[name]
	providerSessionDir[name] = func(cwd string) string { return dir }
	t.Cleanup(func() {
		if existed {
			providerSessionDir[name] = prev
		} else {
			delete(providerSessionDir, name)
		}
	})
}

func TestSnapshotJsonls(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.jsonl"), "x")
	writeFile(t, filepath.Join(dir, "ignored.txt"), "x")
	if err := os.Mkdir(filepath.Join(dir, "sub.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}

	snap := snapshotJsonls(dir)
	if len(snap) != 1 {
		t.Fatalf("snapshot has %d entries, want 1: %v", len(snap), snap)
	}
	if _, ok := snap["a.jsonl"]; !ok {
		t.Errorf("snapshot missing a.jsonl: %v", snap)
	}

	if got := snapshotJsonls(""); len(got) != 0 {
		t.Errorf("snapshotJsonls(\"\") = %v, want empty", got)
	}
	if got := snapshotJsonls(filepath.Join(dir, "does-not-exist")); len(got) != 0 {
		t.Errorf("snapshotJsonls(missing dir) = %v, want empty", got)
	}
}

func TestCaptureUUIDNewFile(t *testing.T) {
	dir := t.TempDir()
	registerFakeProvider(t, "faketest-new", dir)

	// pre-existing file that must not be picked up
	writeFile(t, filepath.Join(dir, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.jsonl"), "old")

	before := SnapshotFor("faketest-new", "/whatever")
	if len(before) != 1 {
		t.Fatalf("before snapshot = %v, want 1 entry", before)
	}

	// simulate provider creating a fresh session log after spawn
	writeFile(t, filepath.Join(dir, testUUID+".jsonl"), "new")

	got := CaptureUUID("faketest-new", "/whatever", before, 2*time.Second)
	if got != testUUID {
		t.Errorf("CaptureUUID = %q, want %q", got, testUUID)
	}
}

func TestCaptureUUIDTouchedExistingFile(t *testing.T) {
	dir := t.TempDir()
	registerFakeProvider(t, "faketest-touch", dir)

	path := filepath.Join(dir, testUUID+".jsonl")
	writeFile(t, path, "old")

	before := SnapshotFor("faketest-touch", "/whatever")

	// bump mtime well forward so it is strictly After the snapshot value even
	// on coarse-mtime filesystems.
	future := time.Now().Add(5 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	got := CaptureUUID("faketest-touch", "/whatever", before, 2*time.Second)
	if got != testUUID {
		t.Errorf("CaptureUUID = %q, want %q (touched existing file)", got, testUUID)
	}
}

func TestCaptureUUIDNoSessionDir(t *testing.T) {
	before := Snapshot{}
	if got := CaptureUUID("no-such-provider", "/tmp", before, 50*time.Millisecond); got != "" {
		t.Errorf("CaptureUUID(unknown provider) = %q, want empty", got)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
