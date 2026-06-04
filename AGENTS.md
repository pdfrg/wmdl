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
