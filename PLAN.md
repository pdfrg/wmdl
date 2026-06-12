# wmdl — Architecture Plan

## Overview

Automated weekly media workflow:

1. **discover** (cron) — scrape release sources, enrich with TMDB/RT, notify
2. **review** (user) — TUI approve/reject/open in Brave
3. **process** (user) — grab tabs → Prowlarr search → TUI picker → download → library

---

## Workflow

```
┌─────────────────┐     ┌─────────────────┐     ┌──────────────────────────┐
│  wmdl discover  │ ──> │  wmdl review     │ ──> │  wmdl process            │
│ (cron weekly)   │     │ (user-initiated)│     │ (user-initiated)         │
├─────────────────┤     ├─────────────────┤     ├──────────────────────────┤
│ Scrape sources  │     │ TUI: list view  │     │ Grab Brave tabs (CDP)    │
│ TMDB lookup     │     │ approve/reject  │     │ Match to approved titles │
│ RT URL search   │     │ open in Brave   │     │ For each:                │
│ Save to SQLite  │     │ (via CDP)       │     │  ├ Prowlarr search       │
│ Gotify notify   │     │ update DB state │     │  ├ Score + sort results  │
└─────────────────┘     └─────────────────┘     │  ├ TUI picker (top N)   │
                                                  │  └ User selects one     │
                                                  │ Add to qBittorrent      │
                                                  │ Add to Radarr/Sonarr    │
                                                  │ Check missing media     │
                                                  │ (earlier seasons,       │
                                                  │  collection gaps)       │
                                                  │ Gotify notify done      │
                                                  └──────────────────────────┘
```

## Project Tree

```
cmd/wmdl/
├── main.go              # cobra root
├── discover.go          # wmdl discover
├── review.go            # wmdl review (TUI list)
└── process.go           # wmdl process (TUI picker)

internal/
├── discover/
│   ├── provider.go          # ReleaseProvider interface
│   ├── dvdsreleasedates.go  # DVDReleaseDates scraper
│   ├── flixpatrol.go        # FlixPatrol scraper
│   ├── tmdb.go              # TMDB lookup (enrichment)
│   └── rottentomatoes.go    # RT search (URL only)
├── review/
│   └── tui.go               # bubbletea list-based TUI
├── process/
│   ├── selector.go          # bubbletea release picker TUI
│   └── executor.go          # orchestrate process per title
├── browser/
│   └── brave.go             # chromedp → open tabs
├── notifier/
│   └── gotify.go            # POST /message
├── search/
│   └── prowlarr.go          # GET /api/v1/search
├── download/
│   ├── client.go            # DownloadClient interface
│   ├── qbittorrent.go       # qBittorrent REST impl
│   ├── transmission.go      # Transmission RPC impl
│   └── deluge.go            # Deluge RPC impl
├── library/
│   ├── radarr.go            # Radarr API (add, search, collection)
│   └── sonarr.go            # Sonarr API (add, search, seasons)
├── quality/
│   └── filter.go            # parse release name, score, sort
├── model/
│   └── types.go             # shared domain types
├── db/
│   └── sqlite.go            # SQLite queries, migrations
└── config/
    └── config.go            # viper-based config loader
```

## Database Schema

```sql
titles (
  id              INTEGER PRIMARY KEY,
  tmdb_id         INTEGER UNIQUE,
  title           TEXT NOT NULL,
  year            INTEGER,
  media_type      TEXT CHECK(media_type IN ('movie','tv')),
  imdb_id         TEXT,
  rt_url          TEXT,
  rt_critics_score REAL,
  rt_audience_score REAL,
  tmdb_rating     REAL,
  created_at      TEXT DEFAULT (datetime('now'))
);

release_events (
  id              INTEGER PRIMARY KEY,
  title_id        INTEGER REFERENCES titles(id),
  source          TEXT NOT NULL,        -- 'dvdsreleasedates', 'flixpatrol'
  release_type    TEXT,                 -- 'physical', 'streaming'
  release_date    TEXT,
  status          TEXT DEFAULT 'pending'
                    CHECK(status IN ('pending','approved','rejected','downloaded')),
  previous_status TEXT,                 -- for upgrade detection
  created_at      TEXT DEFAULT (datetime('now'))
);

downloads (
  id              INTEGER PRIMARY KEY,
  title_id        INTEGER REFERENCES titles(id),
  release_event_id INTEGER REFERENCES release_events(id),
  quality         TEXT,                 -- '2160p', '1080p'
  source_type     TEXT,                 -- 'bluray', 'web-dl', 'webrip'
  codec           TEXT,                 -- 'h265', 'x264', etc
  info_hash       TEXT,
  category        TEXT,                 -- 'Movies', 'TV'
  status          TEXT DEFAULT 'added' CHECK(status IN ('added','downloading','complete','upgraded')),
  client_torrent_id TEXT,
  radarr_id       INTEGER,
  sonarr_id       INTEGER,
  created_at      TEXT DEFAULT (datetime('now'))
);
```

## Quality Scoring

Score each Prowlarr result and show top N in TUI picker:

| Factor | Max Points | Notes |
|--------|-----------|-------|
| Resolution match | 200 | 2160p=200 for movies, 1080p=200 for TV |
| HDR bonus | 50 | Movies at 2160p only |
| Source priority | 100 | bluray=100, web-dl=60, webrip=20 |
| Codec priority | 80 | h265/x265=80, h264=40, av1=30 |
| Release group | 50 | If in preferred_groups config |
| Seeders | 40 | min(40, log2(seeders+1) × 6) |
| Bitrate estimate | 30 | size/time if runtime known |

## Config File (~/.config/wmdl/config.yaml)

```yaml
notifier:
  gotify_url: "http://gotify.local:8080"
  gotify_token: ""

browser:
  debug_port: 9222
  profile: "wmd-review"

prowlarr:
  url: "http://prowlarr.local:9696"
  api_key: ""

tmdb:
  api_key: "your_tmdb_api_key"       # Get at https://www.themoviedb.org/settings/api
  access_token: ""                     # Alternative: use v4 bearer token instead of api_key

downloader:
  type: "qbittorrent"  # qbittorrent, transmission, deluge
  qbittorrent:
    url: "http://qb.local:8080"
    username: "admin"
    password: ""
  categories:
    movies: "Movies"
    tv: "TV"

library:
  radarr:
    url: "http://radarr.local:7878"
    api_key: ""
    root_folder: "/media/movies"
    quality_profile: "1080p"
    monitor: false
  sonarr:
    url: "http://sonarr.local:8989"
    api_key: ""
    root_folder: "/media/tv"
    quality_profile: "1080p"
    monitor_new_episodes: true
    season_folders: true

quality:
  movies:
    resolution: "2160p"
    prefer_hdr: true
    source_priority: ["bluray", "web-dl", "webrip"]
    codec_priority: ["h265", "x265", "h264", "av1"]
  tv:
    resolution: "1080p"
    prefer_hdr: false
    source_priority: ["bluray", "web-dl", "webrip"]
    codec_priority: ["h265", "x265", "h264", "av1"]

show_top_n: 10
min_seeders: 3
preferred_release_groups: []
```

## Implementation Phases

### Phase 1: Foundation
- [ ] Config loading (viper)
- [ ] SQLite schema + migrations
- [ ] Model types
- [ ] CLI skeleton (cobra with subcommands)

### Phase 2: Discovery
- [ ] TMDB API client (search, enrich)
- [ ] Rotten Tomatoes URL search
- [ ] DVDReleaseDates scraper
- [ ] FlixPatrol scraper
- [ ] Gotify notifier
- [ ] `wmdl discover` command

### Phase 3: Review
- [ ] bubbletea list-based TUI
- [ ] Approve/reject/open-in-brave actions
- [ ] `wmdl review` command

### Phase 4: Processing
- [ ] Brave CDP tab grabber
- [ ] Prowlarr search client
- [ ] Release title parser + quality scorer
- [ ] bubbletea release picker TUI
- [ ] `wmdl process` command

### Phase 5: Download & Library
- [ ] DownloadClient interface + qBittorrent impl
- [ ] Radarr API: add, search, collections
- [ ] Sonarr API: add, search, seasons
- [ ] Missing-media detection (earlier seasons, collection gaps)
- [ ] Transmission/Deluge backends (optional)

### Phase 6: Polish
- [ ] Rotten Tomatoes ratings scraper
- [ ] Upgrade detection (streaming → bluray)
- [ ] Error handling, retries, logging
- [ ] AUR package, systemd timer

### Phase 7: Book Integration — LazyLibrarian Backend

Goal: Replace the current "download → ABS scan" pipeline with proper *arr-style library management using LazyLibrarian.

**Architecture:**
```
wmdl discover → review → addToLL (unmonitored) → queueBook → LL searches via Torznab/Prowlarr → download → auto-import+rename

                    OR (wmdl-drives-search):

wmdl discover → review → addToLL (unmonitored) → wmdl searches Prowlarr → download → forceProcess (LL imports+renames)
```

**7.1 — LazyLibrarian Client** (`internal/library/lazylibrarian.go`)
- [ ] `LazyLibrarianClient` struct following same pattern as `radarr.go`/`sonarr.go`
- [ ] `Ping()` — health check
- [ ] `AddAuthor(authorID, fetchBooks)` — add author with all books as `Skipped` (unmonitored)
- [ ] `AddBook(bookID)` — add individual book to DB as `Skipped`
- [ ] `QueueBook(bookID, format)` — set `Wanted` (triggers LL search)
- [ ] `UnqueueBook(bookID, format)` — set `Skipped`
- [ ] `GetBookStatus(bookID)` — get current status (Skipped/Wanted/Have/Snatched/Failed)
- [ ] `GetSeriesMembers(seriesID)` — series membership for Phase 3
- [ ] `TriggerImport(dir)` — call `forceProcess` on completed download dir
- [ ] `SearchBook(bookID, format)` — trigger specific book search
- [ ] Compile-time interface check: `var _ Client = (*LazyLibrarianClient)(nil)`

**7.2 — Config** (`internal/config/config.go`)
- [ ] `LazyLibrarianConfig` struct: URL, APIKey, RootFolder, QualityProfile, Timeout
- [ ] Validation rules (non-empty URL, API key)
- [ ] Config defaults
- [ ] `book_backend: "lazylibrarian"` top-level config

**7.3 — Pipeline Integration** (`internal/process/executor.go`)
- [ ] `addToLazyLibrarian()` — called from book processing pipeline
- [ ] For arr/auto/yolo modes: `queueBook` after add → LL handles search
- [ ] For interactive mode: add as `Skipped`, wmdl handles Prowlarr, then call LL import
- [ ] Replace `UploadToAudiobookshelf()` with LL import flow
- [ ] Keep ABS as optional scan trigger alongside LL

**7.4 — Review TUI Enhancement** (`internal/review/tui.go`)
- [ ] Display series info for book items (already stored in `Book.SeriesID`/`SeriesName`)
- [ ] Show LL library status (Have/Wanted/Skipped) similar to ABS cache

**7.5 — Phase 3: Book Series Gap Detection**
- [ ] After adding a book, call `GetSeriesMembers` to find other books in series
- [ ] Check if earlier books are in LL's library
- [ ] Pre-queue search for missing series entries
- [ ] Handle dual-format (ebook + audiobook) for series entries

### Phase 8: Book Backend Interface (Flexibility)

Goal: Abstract book backend behind a common interface so users can choose their preferred stack.

**8.1 — Interface Definition**
- [ ] Extract `BookClient` interface from LL implementation in `internal/library/book_client.go`
- [ ] Methods: `Ping`, `AddAuthor`, `AddBook`, `QueueBook`, `UnqueueBook`, `GetBookStatus`, `GetSeriesMembers`, `TriggerImport`, `SearchBook`
- [ ] Factory function: `NewBookClient(config)` based on `book_backend` type

**8.2 — Shelfarr Backend** (`internal/library/shelfarr.go`)
- [ ] Implement `BookClient` interface using Shelfarr REST API
- [ ] Map Shelfarr request lifecycle to `BookClient` methods
- [ ] Note: Shelfarr is request-based, not monitor-based — `QueueBook` creates a request, no "unmonitored add" concept

**8.3 — Grimmory/BookOrbit Backends** (stretch)
- [ ] Implement `BookClient` for media-server import pattern
- [ ] `AddBook` = copy file to BookDrop/Book Dock + finalize via API
- [ ] No search/queue functionality (media servers only)

### Phase 9: Dual-Format Polish
- [ ] Proper ebook+audiobook dual-format handling across all backends
- [ ] Upgrade detection for books (similar to streaming→bluray)
- [ ] Config validation per backend
- [ ] Documentation for each supported backend
