# wmd — Weekly Media Downloader

Go CLI that discovers new DVD/streaming releases weekly, lets user review via TUI, then searches torrents via Prowlarr, downloads via qBittorrent, and adds to Radarr/Sonarr.

## Commands

```bash
go build ./cmd/wmd            # build
go test ./...                 # test all
go vet ./...                  # static analysis
go fmt ./...                  # format
golangci-lint run ./...       # full lint
go run ./cmd/wmd discover     # scrape & notify
go run ./cmd/wmd review       # TUI approve/reject
go run ./cmd/wmd process      # TUI picker + download
```

Run all checks before committing:
```bash
go fmt ./... && go vet ./... && golangci-lint run ./... && go test ./...
```

## Project Structure

```
cmd/wmd/          — cobra CLI commands
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

## Key Dependencies

- `spf13/cobra` + `spf13/viper` — CLI + config
- `modernc.org/sqlite` — SQLite (no CGo)
- `chromedp/chromedp` — Brave CDP
- `charmbracelet/bubbletea` — TUI
- `gocolly/colly` — scraping
- Standard library HTTP client

## State Storage

SQLite at `~/.local/share/wmd/wmd.db`. Key tables: `titles`, `release_events`, `downloads`.

## Config

`~/.config/wmd/config.yaml` — server URLs, tokens, quality prefs.

## Conventions

- Go 1.22+ stdlib patterns
- Interface-based design for providers/downloaders
- Clean errors propagated to CLI, no panics
- zerolog for structured logging

## Agent Rules

- **No file editing with bash commands** — never use sed, awk, echo >, or cat <<EOF to modify files. Use dedicated write/edit tools only.
- **Verify after edit** — after writing or editing a file, always re-read it to confirm the change is correct. Edits can silently go wrong.
- **Self-debug first** — debug by running bash commands (curl, jq, test programs) directly. Only ask the user to run/report back when absolutely unavoidable.
- **Tests** — write tests alongside implementation, especially for quality/filter logic, API clients, and parsing code.
- **Context for API calls** — use `http.NewRequestWithContext` with timeouts for all external HTTP calls.
- **Config validation** — validate config at startup with clear error messages, not mid-operation panics.
- **Compile-time interface checks** — use `var _ Client = (*Impl)(nil)` to verify implementations satisfy interfaces.
- **Graceful shutdown** — handle SIGINT/SIGTERM for clean exits, especially during TUI sessions.
