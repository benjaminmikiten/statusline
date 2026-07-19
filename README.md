# statusline

A Claude Code statusline focused on prompt-cache cost visibility: it warns
when the cache is about to expire, has already gone cold, or context is past
the compact/clear threshold, instead of just showing raw cost and context
numbers. Background and rationale: `docs/superpowers/specs/2026-07-19-statusline-design.md`.

## Build

```bash
go build -o statusline .
```

## Install

1. Copy (or symlink) the built binary somewhere stable, e.g. `~/.claude/statusline`.
2. Add it to your Claude Code settings (`~/.claude/settings.json`):

```json
{
  "statusLine": {
    "type": "command",
    "command": "~/.claude/statusline",
    "refreshInterval": 15
  }
}
```

`refreshInterval` is required for the cache-expiry countdown to advance while
you're idle — without it, the statusline only updates after you send a
message, which defeats the early warning.

## Configuration (optional)

Create `~/.claude/statusline-config.json` (see `examples/statusline-config.json`)
to override defaults:

| Field | Default | Meaning |
| --- | --- | --- |
| `compact_threshold_pct` | `80` | Context `used_percentage` at which the third line recommends `/compact` or `/clear`. |
| `cache_ttl_override` | `""` (unset) | Fallback TTL (e.g. `"5m"`, `"1h"`) used only when the TTL can't be auto-detected from the transcript (e.g. no assistant message yet this session). Auto-detection from the transcript's `cache_creation` breakdown is authoritative when available. |
| `color` | `true` | Set `false` to disable ANSI color codes. |

Missing file or missing fields fall back to defaults — no config file is required.

## Output

Two lines always; a third appears only when action is worth taking:

```
[Opus] 📁 my-project | 🌿 main +2 ~1
██████████ 82% | $3.41 | cache: 91% | 5h: 23% 7d: 41%
⚠️ context at 82% — /compact or /clear recommended
```

or, when the cache is about to go cold from idle:

```
[Opus] 📁 my-project | 🌿 main
███░░░░░░░ 30% | $1.10
🟡 cache expires in 47s
```
