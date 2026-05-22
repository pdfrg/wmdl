# wmdl Documentation

## Installation

```bash
go install github.com/pdfrg/wmdl/cmd/wmdl@latest
```

Or build locally:

```bash
git clone https://github.com/pdfrg/wmdl
cd wmdl
make build
```

## Configuration

Required settings in `~/.config/wmdl/config.yaml`:
- `tmdb.api_key` — get one free at https://www.themoviedb.org/settings/api
- `prowlarr.url` + `prowlarr.api_key`
- One downloader (`qbittorrent`, `transmission`, or `deluge`) with URL and credentials

Optional: `radarr.url` + `radarr.api_key` for automated library management, `sonarr.url` + `sonarr.api_key` for TV shows, `notifier` for webhook notifications (Gotify, Slack, Discord, ntfy).

Full example at [config.yaml.example](config.yaml.example).

## Commands

### `wmdl all`

Run discover → review → process in sequence for the current week. The full pipeline in one command.

### `wmdl discover`

Scrape release websites, enrich metadata (TMDB, IMDb, Rotten Tomatoes), save to SQLite database, and optionally send a notification.

Flags: `--headless` (no browser window, for cron/systemd)

### `wmdl review`

Opens a Bubble Tea TUI showing all pending releases. Approve or reject each one. Shows poster art, ratings, genre, and summary for each title.

### `wmdl process`

For each approved release: searches Prowlarr across configured indexers, filters results by your quality preferences (resolution, source type, codec, preferred groups, min seeders), opens a release picker TUI, then sends the chosen release to your download client and adds it to Radarr/Sonarr.

Supports two modes:
- `batch` (default): searches all items first, then presents them
- `interactive`: searches and presents each item just-in-time

### `wmdl status`

Displays a week-by-week progress table showing which weeks have been discovered, reviewed, and processed.

### `wmdl catchup`

Advances each incomplete week by one step per invocation. Run it repeatedly to catch up weeks in order.

### Target a specific week

```bash
wmdl all --week 2025-W14       # ISO week
wmdl all --week 2025-03-31     # date
wmdl all --week last           # previous week
wmdl all --week next           # next week
```

## Architecture

```
discover  →  review  →  process
   │            │           │
 scrape      TUI       Prowlarr search
 TMDB/RT   approve/    release picker
 enrich     reject     download + library
   │            │           │
   └────────────┴───────────┘
              SQLite
        (~/.local/share/wmdl/)
```

### Discover phase

Three sources are scraped:
- **dvdsreleasesdates.com** — physical DVD/Blu-ray releases (GoQuery HTML parsing)
- **TMDB Discover API** — streaming premieres in the past 2 weeks
- **FlixPatrol** — streaming release calendar (best-effort)

Each title is enriched through:
- **TMDB** — TMDB ID, IMDb ID, overview, genres, runtime, poster path, US content rating
- **IMDbAPI** — IMDb rating and Metacritic score
- **Rotten Tomatoes** — critic and audience scores via chromedp web scraping

### Review phase

Bubble Tea TUI with keyboard navigation (j/k to move, a/r to approve/reject, enter to confirm, ? for help). Posters are fetched from TMDB and rendered inline via Kitty terminal protocol when supported.

### Process phase

1. Prowlarr search with tiered queries (resolution-specific → fallbacks)
2. Results filtered by title, year, season number, and media type
3. Releases scored and sorted by quality preferences
4. TUI picker to select the best release
5. Torrent added to download client (qBittorrent/Transmission/Deluge)
6. Download record saved to SQLite
7. Movie/series added to Radarr/Sonarr with selected quality profile

### Quality scoring

Releases are scored on:
- Resolution match (2160p > 1080p > 720p)
- Source type priority (bluray > web-dl > webrip > hdtv)
- Codec priority (h265/hevc > h264/x264 > av1)
- Preferred release groups
- Minimum seeders filter
- HDR preference

## Automation

### Systemd timer (weekly discovery)

```bash
cp contrib/wmdl-discover.service ~/.config/systemd/user/
cp contrib/wmdl-discover.timer   ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now wmdl-discover.timer
```

Runs `wmdl discover --headless` every Wednesday at 05:00. Results can then be reviewed and processed at your convenience.

## Data

SQLite database at `~/.local/share/wmdl/wmdl.db`. Key tables: `titles`, `release_events`, `downloads`, `week_state`.

Logs at `~/.local/state/wmdl/wmdl.log` (also printed to stderr).

## Project structure

```
cmd/wmdl/          — CLI commands (cobra)
internal/discover/  — scrapers (interface-based)
internal/review/    — Bubble Tea TUI list
internal/process/   — Bubble Tea release picker + orchestration
internal/browser/   — chromedp Brave tab grabber
internal/search/    — Prowlarr API client
internal/download/  — download client interface + impls
internal/library/   — Radarr/Sonarr API clients
internal/quality/   — release title parser + scorer
internal/model/     — shared types
internal/db/        — SQLite state
internal/config/    — viper config loader
internal/notifier/  — webhook notifications
```

## Building

```bash
make build   # go build ./cmd/wmdl
make test    # go test ./...
make lint    # golangci-lint run ./...
make check   # fmt + vet + lint + test + build
```
