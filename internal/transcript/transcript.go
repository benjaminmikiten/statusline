// Package transcript reads a Claude Code session transcript (JSONL) to find
// the timestamp and detected cache TTL of the most recent assistant
// message. This is the only place that idle-time-since-last-write is
// observable, since the statusline JSON itself carries no such field.
package transcript

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

var ErrNoAssistantMessage = errors.New("transcript: no assistant message found")

type Usage struct {
	Timestamp time.Time
	// TTL is the cache TTL detected from the last assistant message's
	// cache_creation breakdown. Zero when it can't be determined (e.g. the
	// write used neither ephemeral field, or both are zero because the
	// message was a full cache read with no new write).
	TTL time.Duration
}

type transcriptLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Usage struct {
			CacheCreation struct {
				Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
				Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
}

// LastAssistantUsage scans the transcript at path for the most recent line
// with "type":"assistant" and returns its timestamp and detected TTL.
// Malformed trailing lines (e.g. a transcript truncated mid-write) are
// skipped rather than treated as fatal, since only the last VALID assistant
// line matters.
func LastAssistantUsage(path string) (Usage, error) {
	f, err := os.Open(path)
	if err != nil {
		return Usage{}, fmt.Errorf("transcript: open %s: %w", path, err)
	}
	defer f.Close()

	var found bool
	var result Usage

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // lines can be long
	for scanner.Scan() {
		var line transcriptLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			continue // skip malformed/truncated lines
		}
		if line.Type != "assistant" {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, line.Timestamp)
		if err != nil {
			continue
		}

		ttl := time.Duration(0)
		switch {
		case line.Message.Usage.CacheCreation.Ephemeral1h > 0:
			ttl = time.Hour
		case line.Message.Usage.CacheCreation.Ephemeral5m > 0:
			ttl = 5 * time.Minute
		}

		result = Usage{Timestamp: ts, TTL: ttl}
		found = true
	}
	if err := scanner.Err(); err != nil {
		return Usage{}, fmt.Errorf("transcript: scan %s: %w", path, err)
	}
	if !found {
		return Usage{}, ErrNoAssistantMessage
	}
	return result, nil
}
