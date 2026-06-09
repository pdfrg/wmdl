# wmdl — Weekly Media Downloader

Go CLI that discovers new DVD/streaming releases weekly, lets user review via TUI, then searches torrents via Prowlarr, downloads via your preferred torrent client, and adds to Radarr/Sonarr.

## Commands

```bash
go build ./cmd/wmdl            # build
go test ./...                 # test all
go vet ./...                  # static analysis
go fmt ./...                  # format
golangci-lint run ./...       # full lint
go run ./cmd/wmdl discover     # scrape & notify
go run ./cmd/wmdl review       # TUI approve/reject
go run ./cmd/wmdl process      # TUI picker + download
```

Run all checks before committing:
```bash
go fmt ./... && go vet ./... && golangci-lint run ./... && go test ./...
```

## Project Structure

```
cmd/wmdl/          — cobra CLI commands
internal/discover/  — scrapers (interface-based)
internal/review/    — bubbletea TUI list
internal/process/   — bubbletea release picker
internal/browser/   — chromedp tab grabber
internal/search/    — Prowlarr API
internal/download/  — download client interface + impls
internal/library/   — Radarr/Sonarr API
internal/quality/   — release title parser + scorer
internal/model/     — shared types
internal/db/        — SQLite state
internal/config/    — viper config loader
```

## WMDL Atomic Week

All discovery, review, and processing is scoped to a single **WMDL atomic week** — a fixed Wednesday–Tuesday window. This model originates from physical media (DVD/BluRay) which typically releases on Tuesday. The first opportunity to process a given week is Wednesday morning.

- The **reference Tuesday** is the most recently completed Tuesday (i.e. yesterday if today is Wednesday).
- A WMDL week runs from that reference Tuesday **back 6 days to the prior Wednesday**.
- All media types (movies, TV, music, books, anime) use the same Wed–Tue window, with optional `initial_timeshift_weeks` to look back N atomic weeks.
- `addISOWeekOffset` and `bookTargetWeekFrom`/`musicTargetWeek` apply the timeshift. The underlying ISO week math uses `tuesdayOfISOWeek` to anchor the week boundaries.
- Timeshifted weeks still respect Wed–Tue boundaries — the shift just picks an earlier reference Tuesday.

**Example:** On Thursday June 4, the most recent Tuesday is June 2, so the current atomic week is **Wed May 27 – Tue June 2**. Timeshifting 1 week for books targets **Wed May 20 – Tue May 26**.

## Bookshop Week Assignment

Bookshop.org only shows the current week's new releases — no archive URLs exist for past weeks. The bookshop scraper always targets the **current real ISO week** (ignoring `initial_timeshift_weeks`).

Each scraped book is stored under its **actual release week**, computed from the enrichment-provided release date (falling back to the page header date). The storage week is `t.ISOWeek()` of that date — NOT the runner's target week and NOT the timeshifted book week.

Since `parseWeekFlag("")` anchors to the most recent completed Tuesday:
  - On Tuesday: the reference Tuesday is the *previous* Tuesday (offset = 7 days)
  - On Wednesday: the reference Tuesday is *yesterday*

A bookshop item discovered during a timeshifted run is stored for the week of its actual release, not the timeshifted target. It sits in `book_release_events` with no corresponding `week_state` row. The user discovers the week naturally later when `wmdl discover` is run for it.

**Dedup merge:** When a different scraper later finds the same book, `GetLatestBookReleaseEventTx` finds the existing bookshop event. Instead of silently skipping, the event's `source` and `notes` fields are extended via `mergeBookItems` — e.g. `source="bookshop"` becomes `"bookshop,goodreads"`, and notes carry data from both sources (bookshop URLs + bookmarks critic summaries).

**Example log on Tuesday June 9 (timeshift_weeks=1, book targets May 20-26):**
```
INFO[0002] found items                                   count=15 provider=bookshop
INFO[0003] bookshop: future week pre-population           count=15 release_date=2026-06-08 stored_under=2026-W24 timeshift_weeks=1 review_from=2026-06-10
```

## Key Dependencies

- `spf13/cobra` + `spf13/viper` — CLI + config
- `modernc.org/sqlite` — SQLite (no CGo)
- `chromedp/chromedp` — Brave CDP
- `charm.land/bubbletea/v2` + `charm.land/lipgloss/v2` — TUI (v2 series, import from charm.land)
- `gocolly/colly` — scraping
- Standard library HTTP client

## State Storage

SQLite at `~/.local/share/wmdl/wmdl.db`. Key tables: `titles`, `release_events`, `downloads`.

## Config

`~/.config/wmdl/config.yaml` — server URLs, tokens, quality prefs.

## Conventions

- Go 1.22+ stdlib patterns
- Interface-based design for providers/downloaders
- Clean errors propagated to CLI, no panics
- zerolog for structured logging

## Agent Rules

- **No file editing with bash commands** — never use sed, awk, echo >, or cat <<EOF to modify files. Use dedicated write/edit tools only.
- **Verify after edit** — after writing or editing a file, always re-read it to confirm the change is correct. Edits can silently go wrong.
- **Test integrations/API responses before and after coding** — Always run cli test commands (e.g. curl) to see if API response is as expected before writing new code. Then test API again with exact code pattern used before committing. 
- **Self-debug first** — debug by running bash commands (curl, jq, test programs) directly. Only ask the user to run/report back when absolutely unavoidable.
- **Tests** — write tests alongside implementation, especially for quality/filter logic, API clients, and parsing code.
- **Context for API calls** — use `http.NewRequestWithContext` with timeouts for all external HTTP calls.
- **Config validation** — validate config at startup with clear error messages, not mid-operation panics.
- **Compile-time interface checks** — use `var _ Client = (*Impl)(nil)` to verify implementations satisfy interfaces.
- **Graceful shutdown** — handle SIGINT/SIGTERM for clean exits, especially during TUI sessions.
- **NEVER REVERT FROM GIT WITHOUT EXPLICIT USER AGREEMENT** — numerous desired changes have been lost this way.
Do not revert, delete, or do any destructive file actions (other than on /tmp files) without ASKING FOR
CONFIRMATION FIRST!
