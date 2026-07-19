# Cost-aware Claude Code statusline (Go) — Design

## Problem

Claude Code's built-in statusline shows raw numbers (cost, context %) but doesn't
help a user reason about the thing that actually drives their bill: prompt-cache
behavior. Per Anthropic's prompt-caching docs and community cost-analysis writeups
(recca0120's "session cost & cache misconception" and 3-month billing postmortem
posts), the dominant cost lever isn't session length or turn count — it's whether
the next turn hits a warm cache (10% of base input price) or pays a cold rewrite
(100%, or more with an editing/model-switch/tool-change invalidation). The two
`/clear`-adjacent decisions that matter are:

1. Is the cache about to go cold from idle time (default 5-minute TTL), so the
   user should send their next message now rather than in six minutes?
2. Is context approaching the point where auto-compact will fire anyway (article's
   ~78% "safe zone" heuristic), such that a deliberate `/compact` or `/clear` now
   is better than an involuntary one later?

This tool answers both, visually, at a glance, using only data Claude Code's
statusline protocol already exposes (stdin JSON + the transcript JSONL file).

## Non-goals

- Not a daemon; no persistent process, no IPC.
- Not a replacement for `ccusage`/`claude-view`-style historical analytics —
  single-session, present-moment only.
- Doesn't attempt to change Anthropic's server-side billing behavior or verify
  the actual bill; `cost.total_cost_usd` is Claude Code's own client-side
  estimate, and this tool inherits that caveat.
- Model-pinning / Opus-vs-Sonnet routing advice (from the billing postmortem) is
  explicitly out of scope for v1 — that's a `settings.json`/workflow habit, not
  something a per-turn statusline can act on.

## Architecture

Single Go binary, `statusline`, invoked per Claude Code's documented
`statusLine.command` contract: JSON on stdin, formatted text on stdout, exits.
`refreshInterval` is set (~15s) so the cache countdown advances even when the
user is idle reading rather than typing — Claude Code's event-driven triggers
alone (post-message, post-compact, etc.) wouldn't tick during idle gaps, which
is exactly when the TTL warning matters most.

```
stdin JSON ─┬─> input (parse)
            │
            ├─> transcript.LastAssistantUsage(transcript_path)
            │     → last timestamp, cache_creation breakdown (5m vs 1h)
            │
            ├─> git.Status(cwd)   [cached to /tmp keyed by session_id]
            │
            └─> config.Load(~/.claude/statusline-config.json)  [optional]
                       │
                       v
                 cache.Evaluate(lastWrite, ttl, now) → state
                 context.Evaluate(used_pct, size, threshold) → compactDue
                       │
                       v
                 render.Lines(...) → stdout (2 fixed + conditional 3rd)
```

## Components

- **`internal/input`** — typed structs mirroring the documented statusline JSON
  schema (`model`, `cost`, `context_window`, `rate_limits`, `workspace`,
  `transcript_path`, `session_id`, etc.). All fields that the docs mark as
  possibly-null/absent are typed as pointers or have explicit zero-value
  handling — never a bare unmarshal into a required field.

- **`internal/transcript`** — opens `transcript_path`, reads from the end
  (no full-file parse; seek backward in chunks looking for the last line(s)
  starting with `{"type":"assistant"`), extracts `timestamp` and
  `message.usage.cache_creation.{ephemeral_5m_input_tokens,ephemeral_1h_input_tokens}`.
  Whichever of the two is non-zero on the most recent write tells us which TTL
  is actually active for this session — this is discovered directly from real
  transcript data (confirmed via `tail` on a live transcript file during
  design), not inferred from settings. Falls back to "unknown, assume 5m" if
  the file is missing, unreadable, or has no assistant messages yet (fresh
  session, first turn in flight).

- **`internal/cache`** — pure function: `Evaluate(lastWriteTime, ttl, now) State`.
  States: `warm` (>60s remaining), `warning` (≤60s remaining), `critical`
  (≤15s remaining), `cold` (TTL has elapsed — next turn WILL pay a full
  rewrite). `cold` and `warning`/`critical` are distinct states with distinct
  copy, not a countdown that free-runs negative. No I/O in this package — takes
  times as arguments, fully unit-testable.

- **`internal/contextstate`** — pure function: `Evaluate(usedPct, windowSize,
  thresholdPct) State`. Default `thresholdPct` is 80, matching the article's
  ~155K/200K heuristic scaled proportionally so it also makes sense for
  1M-token models. Returns `ok` or `compactDue`.

- **`internal/gitstatus`** — shells `git branch --show-current`,
  `git diff --numstat` (staged/unstaged counts). Result cached to
  `/tmp/statusline-git-cache-<session_id>` with a 5s max age, following the
  pattern Claude Code's own docs specify (keyed by `session_id`, not PID,
  since PID changes every invocation and defeats the cache). Absent git repo
  → segment omitted, not an error.

- **`internal/config`** — optional JSON file at
  `~/.claude/statusline-config.json`:
  ```json
  {
    "compact_threshold_pct": 80,
    "cache_ttl_override": "5m",
    "color": true
  }
  ```
  All fields optional; missing file is not an error. `cache_ttl_override` exists
  as an escape hatch for the rare case the transcript-based auto-detect is
  ambiguous (e.g., no writes yet this session) — auto-detection is the primary
  path, config is the fallback, not the other way around.

- **`internal/render`** — builds output text. Two fixed lines, one conditional:
  - **Line 1:** `[Model] 📁 dir | 🌿 branch +staged ~modified`
  - **Line 2:** context bar (color-ramped green/yellow/red at the same
    70/90 thresholds Claude Code's own multiline example uses) + `%` | `$cost`
    | cache hit-rate this turn (`cache_read / (cache_read + cache_creation +
    input)`) | `5h: NN% 7d: NN%` (omitted if `rate_limits` absent)
  - **Line 3 (conditional, only one of these, highest-priority wins):**
    - `critical`: `🔴 cache expires in 12s — send now or pay full rewrite`
    - `warning`: `🟡 cache expires in 47s`
    - `cold`: `❄️ cache cold — next message re-reads ~42K tokens at full price`
    - `compactDue` (only shown if cache line has nothing more urgent):
      `⚠️ context at 82% — /compact or /clear recommended`

## Error handling

- Any missing/null JSON field degrades only its own segment (matches Claude
  Code's documented null-before-first-response behavior) — the script never
  fails outright over one absent field.
- Transcript read errors → cache segment shows nothing rather than a wrong
  guess; never fabricate a countdown from incomplete data.
- All subprocess calls (git) and file reads get a hard timeout (~200ms) so a
  slow filesystem or huge repo can't block the statusline from updating —
  Claude Code's docs are explicit that a hung script blanks the whole line.
- Binary always exits 0 and always writes *something* to stdout, even in a
  degraded state, since a non-zero exit or empty stdout blanks the entire
  statusline per documented behavior.

## Testing

- `internal/cache`, `internal/contextstate`: table-driven unit tests over
  synthetic timestamps/percentages — no I/O, so this is where the actual
  cost-optimization logic gets the most coverage.
- `internal/transcript`: unit tests against small fixture JSONL files
  (including edge cases: no assistant messages yet, malformed trailing line
  from a crash mid-write, both TTL types present across different messages).
- `internal/render`: golden-file tests feeding the exact mock stdin JSON
  pattern Claude Code's own docs recommend for manual testing, asserting
  exact output strings including ANSI codes.
- Manual: `echo '<mock json>' | ./statusline` per Claude Code's documented
  testing tip, plus a real Claude Code session for end-to-end visual check of
  layout width/wrapping in a real terminal.

## Open questions for implementation planning

- Exact cache-hit-rate formula and whether to average over the session or show
  only the most recent turn (leaning: most recent turn, since that's what's
  actionable right now).
- Whether `used_percentage`'s null-before-first-response state should render
  line 2 at all, or a placeholder — leaning: omit context bar, show model/cost
  only, until the first API response populates it.
