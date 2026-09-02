package app

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

const (
	maxEvidenceEntries = 50_000
	maxEvidenceMatches = 256
)

type Evidence struct {
	Sequence         uint64   `json:"sequence"`
	Operation        string   `json:"operation"`
	Path             string   `json:"path"`
	Outcome          string   `json:"outcome"`
	Digest           string   `json:"digest,omitempty"`
	MatchCount       int      `json:"matchCount,omitempty"`
	Matches          []string `json:"matches,omitempty"`
	MatchesTruncated bool     `json:"matchesTruncated,omitempty"`
	Cached           bool     `json:"cached,omitempty"`
	ErrorCode        string   `json:"errorCode,omitempty"`
}

type EvidenceRecorder struct {
	mu        sync.RWMutex
	sequence  uint64
	entries   []Evidence
	seen      map[string]struct{}
	truncated bool
}

func NewEvidenceRecorder() *EvidenceRecorder {
	return &EvidenceRecorder{seen: make(map[string]struct{})}
}

func (r *EvidenceRecorder) record(entry Evidence) {
	if r == nil {
		return
	}
	if entry.MatchCount == 0 {
		entry.MatchCount = len(entry.Matches)
	}
	var matchesTruncated bool
	entry.Matches, matchesTruncated = evidenceMatches(entry.Matches)
	entry.MatchesTruncated = entry.MatchesTruncated || matchesTruncated
	key := evidenceKey(entry)

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.seen[key]; exists {
		return
	}
	if len(r.entries) >= maxEvidenceEntries {
		r.truncated = true
		return
	}
	r.sequence++
	entry.Sequence = r.sequence
	r.seen[key] = struct{}{}
	r.entries = append(r.entries, entry)
}

func (r *EvidenceRecorder) Mark() uint64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Evidence is deduplicated within a phase. A mark starts a new phase so an
	// operation repeated during initialization or planning is still attributable
	// to that phase.
	r.seen = make(map[string]struct{})
	return r.sequence
}

func (r *EvidenceRecorder) Since(mark uint64) []Evidence {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	entries := make([]Evidence, 0)
	for _, entry := range r.entries {
		if entry.Sequence <= mark {
			continue
		}
		copyEntry := entry
		copyEntry.Matches = append([]string(nil), entry.Matches...)
		entries = append(entries, copyEntry)
	}
	return entries
}

func (r *EvidenceRecorder) Truncated() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.truncated
}

func evidenceKey(entry Evidence) string {
	return strings.Join([]string{
		entry.Operation,
		entry.Path,
		entry.Outcome,
		entry.Digest,
		fmt.Sprint(entry.MatchCount),
		strings.Join(entry.Matches, "\x00"),
		fmt.Sprint(entry.MatchesTruncated),
		fmt.Sprint(entry.Cached),
		entry.ErrorCode,
	}, "\x01")
}

func evidenceMatches(matches []string) ([]string, bool) {
	copyMatches := append([]string(nil), matches...)
	sort.Strings(copyMatches)
	if len(copyMatches) <= maxEvidenceMatches {
		return copyMatches, false
	}
	return copyMatches[:maxEvidenceMatches], true
}

func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "not_found"
	case strings.Contains(message, "escapes") || strings.Contains(message, "outside root") || strings.Contains(message, "path escapes"):
		return "outside_root"
	case errors.Is(err, os.ErrPermission):
		return "permission_denied"
	default:
		return "io_error"
	}
}

func errorCode(err error) string {
	return ErrorCode(err)
}
