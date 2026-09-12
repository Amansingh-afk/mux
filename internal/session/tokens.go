// Token usage totals parsed from provider transcript jsonl, for the sidebar
// token meter.
//
// Verified on-disk shapes (2026-09):
//
//	claude (~/.claude/projects/<slug>/<uuid>.jsonl), assistant lines:
//	  {"type":"assistant","message":{"id":"msg_...","usage":{
//	    "input_tokens":2,"cache_creation_input_tokens":12458,
//	    "cache_read_input_tokens":26351,"output_tokens":75}}}
//	  Usage is per turn; streamed chunks of one API message repeat the same
//	  message id with identical usage, so dedupe by message id and sum.
//
//	codex (~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl):
//	  {"type":"event_msg","payload":{"type":"token_count","info":{
//	    "total_token_usage":{"input_tokens":479224,"cached_input_tokens":411904,
//	      "cache_write_input_tokens":0,"output_tokens":4505,...},
//	    "last_token_usage":{...}}}}
//	  total_token_usage is cumulative; the last such line wins. When no line
//	  carries a total, last_token_usage values are summed instead.
package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// TokenUsage is a cumulative token spend for one agent session. Fields are
// normalized across providers: Input counts non-cached input tokens only
// (codex reports cache reads inside input_tokens; they are subtracted out into
// CacheRead), so Total never double-counts.
type TokenUsage struct {
	Input       int64 // cumulative non-cached input tokens
	Output      int64
	CacheRead   int64 // claude cache_read_input_tokens / codex cached_input_tokens
	CacheCreate int64 // claude cache_creation_input_tokens / codex cache_write_input_tokens
}

// Total returns the overall token count across all buckets.
func (t TokenUsage) Total() int64 {
	return t.Input + t.Output + t.CacheRead + t.CacheCreate
}

// tokenMaxLine caps a single jsonl line read while summing usage; the scan for
// that file stops at the first longer line (partial totals are returned).
const tokenMaxLine = 1 << 20 // ~1MB

// tokenCache memoizes parsed totals per transcript so TokensFor is cheap to
// call every few seconds: a file whose mtime+size are unchanged is not
// re-read. Keyed by provider+sessiondir+uuid; entries also pin the resolved
// transcript path so codex rollouts (nested day dirs) are not re-walked on
// every poll.
type tokenCacheEntry struct {
	path  string
	mtime time.Time
	size  int64
	usage TokenUsage
}

const tokenCacheCap = 512

var (
	tokenCacheMu sync.Mutex
	tokenCache   = map[string]tokenCacheEntry{}
	// tokenParses counts full-file parses, for cache-behavior tests.
	tokenParses atomic.Int64
)

// TokensFor reads the agent's transcript and returns cumulative usage.
// Results are cached by (mtime, size), so repeated calls between transcript
// writes cost one stat. Returns (zero, false) when the provider is
// unsupported, the uuid is invalid, or the transcript is missing/unreadable.
func TokensFor(provider, dir, sessionUUID string) (TokenUsage, bool) {
	if provider != "claude" && provider != "codex" {
		return TokenUsage{}, false
	}
	if !ValidUUID(sessionUUID) {
		return TokenUsage{}, false
	}
	root := SessionDir(provider, dir)
	if root == "" {
		return TokenUsage{}, false
	}
	key := provider + "\x00" + root + "\x00" + sessionUUID

	tokenCacheMu.Lock()
	ent, hasEnt := tokenCache[key]
	tokenCacheMu.Unlock()

	var path string
	var info os.FileInfo
	if hasEnt {
		if fi, err := os.Stat(ent.path); err == nil {
			path, info = ent.path, fi
		}
	}
	if path == "" {
		path = tokenTranscriptPath(provider, root, sessionUUID)
		if path == "" {
			return TokenUsage{}, false
		}
		fi, err := os.Stat(path)
		if err != nil {
			return TokenUsage{}, false
		}
		info = fi
	}
	if hasEnt && ent.path == path && ent.size == info.Size() && ent.mtime.Equal(info.ModTime()) {
		return ent.usage, true
	}

	f, err := os.Open(path)
	if err != nil {
		return TokenUsage{}, false
	}
	defer f.Close()
	var usage TokenUsage
	switch provider {
	case "claude":
		usage = parseClaudeTokens(f)
	case "codex":
		usage = parseCodexTokens(f)
	}
	tokenParses.Add(1)

	tokenCacheMu.Lock()
	if _, ok := tokenCache[key]; !ok && len(tokenCache) >= tokenCacheCap {
		for k := range tokenCache {
			delete(tokenCache, k)
			if len(tokenCache) < tokenCacheCap {
				break
			}
		}
	}
	tokenCache[key] = tokenCacheEntry{path: path, mtime: info.ModTime(), size: info.Size(), usage: usage}
	tokenCacheMu.Unlock()
	return usage, true
}

// tokenTranscriptPath locates the transcript file for a session uuid. claude
// names files "<uuid>.jsonl" directly under the project dir; codex rollouts
// embed the uuid in the name and live under dated subdirs, so walk (bounded by
// codexWalkDepth, same as discovery).
func tokenTranscriptPath(provider, root, uuid string) string {
	if provider == "claude" {
		return filepath.Join(root, uuid+".jsonl")
	}
	var found string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries, keep walking
		}
		if d.IsDir() {
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				return fs.SkipDir
			}
			if rel != "." && strings.Count(rel, string(filepath.Separator))+1 > codexWalkDepth {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if strings.HasSuffix(name, ".jsonl") && strings.Contains(name, uuid) {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	return found
}

// parseClaudeTokens sums per-turn usage across assistant lines of a claude
// conversation jsonl, deduping streamed chunks by message id (chunks of one
// API message repeat identical usage). Unparseable lines are skipped.
func parseClaudeTokens(r io.Reader) TokenUsage {
	var out TokenUsage
	seen := map[string]bool{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), tokenMaxLine)
	for sc.Scan() {
		line := sc.Bytes()
		if !bytes.Contains(line, []byte(`"usage"`)) {
			continue
		}
		var rec struct {
			Type    string `json:"type"`
			Message struct {
				ID    string `json:"id"`
				Usage *struct {
					Input       int64 `json:"input_tokens"`
					Output      int64 `json:"output_tokens"`
					CacheRead   int64 `json:"cache_read_input_tokens"`
					CacheCreate int64 `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &rec) != nil || rec.Type != "assistant" || rec.Message.Usage == nil {
			continue
		}
		if id := rec.Message.ID; id != "" {
			if seen[id] {
				continue
			}
			seen[id] = true
		}
		u := rec.Message.Usage
		out.Input += u.Input
		out.Output += u.Output
		out.CacheRead += u.CacheRead
		out.CacheCreate += u.CacheCreate
	}
	return out
}

// codexTokenCounts mirrors codex's token usage payload shape.
type codexTokenCounts struct {
	Input      int64 `json:"input_tokens"`
	CachedIn   int64 `json:"cached_input_tokens"`
	CacheWrite int64 `json:"cache_write_input_tokens"`
	Output     int64 `json:"output_tokens"`
}

func (c *codexTokenCounts) add(o codexTokenCounts) {
	c.Input += o.Input
	c.CachedIn += o.CachedIn
	c.CacheWrite += o.CacheWrite
	c.Output += o.Output
}

// parseCodexTokens extracts cumulative usage from a codex rollout: the last
// token_count event's total_token_usage when present, else the sum of
// last_token_usage across events. codex reports cache reads inside
// input_tokens, so they are split out into CacheRead here.
func parseCodexTokens(r io.Reader) TokenUsage {
	var (
		total     codexTokenCounts
		sum       codexTokenCounts
		haveTotal bool
	)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), tokenMaxLine)
	for sc.Scan() {
		line := sc.Bytes()
		if !bytes.Contains(line, []byte(`"token_count"`)) {
			continue
		}
		var rec struct {
			Payload struct {
				Type string `json:"type"`
				Info struct {
					Total *codexTokenCounts `json:"total_token_usage"`
					Last  *codexTokenCounts `json:"last_token_usage"`
				} `json:"info"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &rec) != nil || rec.Payload.Type != "token_count" {
			continue
		}
		if rec.Payload.Info.Total != nil {
			total, haveTotal = *rec.Payload.Info.Total, true
		}
		if rec.Payload.Info.Last != nil {
			sum.add(*rec.Payload.Info.Last)
		}
	}
	src := sum
	if haveTotal {
		src = total
	}
	in := src.Input - src.CachedIn
	if in < 0 {
		in = 0
	}
	return TokenUsage{Input: in, Output: src.Output, CacheRead: src.CachedIn, CacheCreate: src.CacheWrite}
}

// FormatTokens renders a token count for the sidebar meter: "941", "1.2k",
// "12k", "999k", "1.2M", "15M", "2.5G" — at most 4 chars until 1000G.
// Values truncate (no rounding up), negatives render as "0".
func FormatTokens(n int64) string {
	if n < 0 {
		n = 0
	}
	if n < 1000 {
		return strconv.FormatInt(n, 10)
	}
	for _, u := range []struct {
		div int64
		suf string
	}{{1e9, "G"}, {1e6, "M"}, {1e3, "k"}} {
		if n < u.div {
			continue
		}
		whole := n / u.div
		if whole >= 10 {
			return strconv.FormatInt(whole, 10) + u.suf
		}
		tenth := (n / (u.div / 10)) % 10
		if tenth == 0 {
			return strconv.FormatInt(whole, 10) + u.suf
		}
		return strconv.FormatInt(whole, 10) + "." + strconv.FormatInt(tenth, 10) + u.suf
	}
	return strconv.FormatInt(n, 10) // unreachable
}
