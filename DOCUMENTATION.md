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

Pre-built binaries are available on the [releases page](https://github.com/pdfrg/wmdl/releases).

## Quick Start

Copy the example config and edit it with your credentials:

```bash
cp config.yaml.example ~/.config/wmdl/config.yaml
$EDITOR ~/.config/wmdl/config.yaml
```

Required settings:
- `tmdb.api_key` — get one free at https://www.themoviedb.org/settings/api
- `prowlarr.url` + `prowlarr.api_key`
- One downloader (`qbittorrent`, `transmission`, or `deluge`) with URL and credentials

Then run the full pipeline:

```bash
wmdl all
```

## Configuration

Full example at [config.yaml.example](config.yaml.example).

### Notifications

```yaml
notifier:
  service: gotify      # gotify, slack, discord, ntfy, webhook
  url: "http://gotify.local:8080"
  token: ""
  search_complete_notify: false   # notify when batch search finishes
```
For `process_mode: batch` (default), all Prowlarr searches in run consecutively.
If you have approved numerous items in `review`, this process can take a long time.  
Setting `search_complete_nofity: true` allows you to start `process`, walk away, and
come back to the TUI picker when the notification is received. wmdl makes every effort
to respect the user's time and attention.

A custom Go template can override the JSON payload via `custom_template` with fields
`{{.Title}}`, `{{.Message}}`, `{{.Priority}}`.

### Browser (chromedp)

Some scrapers (Rotten Tomatoes, Goodreads, Bookshop, AllMusic) require a Chrome-based browser:

```yaml
browser:
  binary: "brave"          # brave, google-chrome, chromium, microsoft-edge, vivaldi, opera
  debug_port: 9222
  profile: "wmd-review"
```

Pass `--headless` to run without a visible browser window (recommended for cron/systemd).

### Prowlarr

```yaml
prowlarr:
  url: "http://prowlarr.local:9696"
  api_key: ""
  timeout: 120
  indexer_id:
    videos: 0       # movies + TV (0 = all indexers)
    music: 0        # music
    anime: 0        # anime
    ebooks: 0       # ebooks
    audiobooks: 0   # audiobooks
```

If `indexer_id` is configured (not 0), wmdl searches preferred indexer first.  If < `show_top_n` (default 10)
results are obtained from preferred indexer, fallback searches using all indexers will be performed.

### TMDB

```yaml
tmdb:
  api_key: ""      # v3 API key (required for enrichment)
  access_token: "" # v4 bearer token (alternative to api_key)
```

Required for movie/TV enrichment and FlixPatrol scraper. Get a free key at
https://www.themoviedb.org/settings/api.

### OMDB

```yaml
omdb:
  api_key: ""  # Get a free key at https://www.omdbapi.com/apikey.aspx
```

Optional enrichment source for IMDb ratings, Metacritic scores, awards, and
box office data.

### Download Client

```yaml
downloader:
  type: "qbittorrent"                    # qbittorrent, transmission, deluge
  categories:
    movies: "Movies"
    tv: "TV"
    music: "Music"
    anime: "Anime"
    ebooks: "Books"           # for LazyLibrarian: set both to the same category whose
    audiobooks: "Books"       # save path is LL's Alternate Import Folder
```

See `config.yaml.example` for per-client credentials.

### Shell Hooks

Shell commands that run before and after `wmdl discover`. Useful for bandwidth
management — e.g., pausing your torrent client during scraping to avoid
contention.

```yaml
hooks:
  pre_discover: "docker pause qbittorrent"
  post_discover: "docker unpause qbittorrent"
```

If `pre_discover` exits non-zero, discover is aborted. `post_discover` runs
even on failure (warning only). See `config.yaml.example` for more examples
(qBittorrent/Transmission speed limits, SSH-based commands, custom scripts).

### Library Managers

| Backend | Media | Config Key | Features |
|---------|-------|------------|----------|
| **Radarr** | Movies | `library.radarr` | Add movie, collection gap checking, quality profile, monitor |
| **Sonarr** | TV, Anime | `library.sonarr` | Add series, season folders, monitor new episodes |
| **Lidarr** | Music | `library.lidarr` | Add artist + album, quality/metadata profiles, monitor |
| **LazyLibrarian** | Books | `library.lazylibrarian` | Add author + book, queue/unqueue, series members, import |

Example:

```yaml
library:
  radarr:
    url: "http://radarr.local:7878"
    api_key: ""
    root_folder: "/media/movies"
    quality_profile: "Ultra-HD"
    monitor: false
  sonarr:
    url: "http://sonarr.local:8989"
    api_key: ""
    root_folder: "/media/tv"
    quality_profile: "HD-1080p"
    monitor_new_episodes: true
    season_folders: true
  lidarr:
    url: "http://lidarr.local:8686"
    api_key: ""
    root_folder: "/media/music"
    quality_profile: "Lossless"
    metadata_profile: "Standard"
    monitor: "all"
    monitor_new_albums: true
  lazylibrarian:
    url: "http://lazylibrarian.local:5299"
    api_key: ""
  book_backend: "lazylibrarian"   # or "none"
```

### Processing Modes

Each media type (movies, TV, anime, music, books) has a `mode` setting that controls
how much wmdl automates across the three pipeline stages: **discover**, **review**,
and **process**. Modes are independent per media type — you can mix them (e.g.,
movies=`full`, books=`yolo`).

#### Mode Reference

| Mode | Prowlarr Search | Release Picker | Download | Library Add | SearchNow | Review TUI | Prompts |
|------|:---:|:---:|:---:|:---:|:---:|:---:|:---:|
| **full** | wmdl | shown | wmdl | prompted | no | shown | all interactive |
| **prowlarr-grab** | wmdl | shown | Prowlarr route | skipped | — | shown | all interactive |
| **arr** | skipped (*arr) | skipped | skipped (*arr) | auto | yes | shown | library prompts only |
| **auto** | skipped (*arr) | skipped | skipped (*arr) | auto | yes | shown | all auto-confirmed |
| **yolo** | skipped (*arr) | skipped | skipped (*arr) | auto | yes | skipped | all auto-confirmed |

**Pipeline stage breakdown:**

- **Discover** — identical for all modes: scrapers run, discovered items stored as `pending`.
- **Review** — `yolo` items are auto-approved before the TUI opens; all other modes present
  items in the review TUI for approval or rejection. If *all* items are yolo, the review
  TUI is skipped entirely.
- **Process — Searching:** `full` and `prowlarr-grab` search Prowlarr for torrents.
  `arr`/`auto`/`yolo` skip Prowlarr entirely — the *arr handles searching on its own
  schedule after wmdl adds the item.
- **Process — Picker:** `full` and `prowlarr-grab` show the release picker TUI for
  selecting which torrent to download. `arr`/`auto`/`yolo` skip the picker.
- **Process — Download:** `full` downloads via your configured download client.
  `prowlarr-grab` tells Prowlarr to route the download to its configured client.
  `arr`/`auto`/`yolo` skip download — the *arr handles it.
- **Process — Library:** `full` prompts you to confirm the *arr add (no SearchNow, so
  it won't trigger a duplicate search). `prowlarr-grab` does not add to any library.
  `arr`/`auto`/`yolo` auto-add to the *arr with `SearchNow=true` so the *arr searches
  and downloads on its own schedule.
- **Process — Prompts:** `full` and `prowlarr-grab` ask before every action (library add,
  phase 3, etc.). `arr` prompts only for library adds. `auto` and `yolo` auto-confirm
  all prompts.

**All the above too confusing? In summary:**

- `full`: Recommended for maximum control. Pick the exact torrent you want, confirm every addition to libraries.
- `prowlarr-grab`: Same as `full`, but with manual (or non-*arr) library organization.
- `arr`: You have detailed custom settings in the *arr apps and trust them to get the right torrent. Confirm additions to libraries.
- `auto`: Same as *arr, but auto-add to library (mismatches are rare).
- `yolo`: Download everything, no questions asked. HDD prices are a non-issue.

**Note on `process_mode`:** This is a separate, orthogonal setting (`batch` or
`interactive`) that controls how the release picker presents results — all at once
vs one at a time. It has no effect in `arr`/`auto`/`yolo` modes since the picker is
skipped.

### Quality Scoring

Release scoring is fully configurable per media type:

**Movies / TV:**

```yaml
quality:
  movies:
    resolution: "2160p"           # target: 2160p, 1080p, 720p
    prefer_hdr: true
    source_priority: ["bluray", "web-dl", "webrip"]
    codec_priority: ["h265", "h264", "av1"]
```

**Anime:**

```yaml
quality:
  anime:
    resolution: "1080p"
    prefer_hdr: false
    source_priority: ["bluray", "web-dl", "webrip"]
    codec_priority: ["h265", "h264", "av1"]
```

**Music:**
```yaml
quality:
  music:
    format_priority: ["flac", "mp3", "aac"]
    bitrate_priority: ["lossless", "320", "v0", "v2"]
```

**Books:**
```yaml
quality:
  books:
    ebooks:
      format_priority: ["epub", "mobi", "azw3", "pdf"]
    audiobooks:
      format_priority: ["m4b", "mp3", "flac", "aac", "opus"]
```

### Content Filters

Per media type, you can filter by language, country, and genre:

```yaml
media_types:
  movies:
    filter:
      blocked_languages: ["hi", "te", "ta"]     # origin languages to block
      blocked_countries: ["IN"]                   # origin countries to block
      blocked_genres: ["Documentary", "Musical"]  # genres to block
      allowed_languages: ["en"]                   # exclusive allow (empty = all)
      override_genres: ["Animation"]              # skip all filter checks
```

Media-type-specific filters — these apply to specific scrapers (not all scrapers
for that media type):

**Music:** `min_critic_score`, `min_critic_reviews`, `min_user_score`, `min_user_ratings`,
`include_must_hear` (for live/remix/box set releases) — **AOTY only.**
AllMusic has no comparable scoring filters.

**Anime:** `min_score`, `min_members` — **AniList/Tenrai/Jikan Phase A** (completed anime) only.
`phase_b_*` thresholds are for **Phase B** (currently-airing) only.
`filter_flixpatrol_anime` deduplicates against **FlixPatrol** results.

**Books:** `min_rating`, `min_ratings` — **Goodreads only.** Goodreads Blog, Bookshop,
and BookMarks have no comparable rating filters.

**Physical media lookback:** `media_types.physical_lookback_weeks` (default 0) controls
how many WMDL atomic weeks back to scan DVD/BluRay release dates. This is independent
of `streaming_lookback_weeks` (per media type) which controls streaming release lookback
for movies/TV, and `lookback_weeks` (per media type) which controls lookback for anime,
music, and books.

### Global Options

```yaml
show_top_n: 10                    # max releases shown per picker
min_seeders: 3                    # reject releases below this seeder count
preferred_release_groups: []      # bonus score (e.g. ["NTb", "FLUX"])
poster_mode: "auto"               # auto, kitty, text, off
process_mode: "batch"             # batch or interactive
check_collections: true           # Phase 3 collection gap checking
cache_ttl_hours: 48               # *arr library cache lifetime
include_backlog: false            # after 'process', retry previously-unfound items from past weeks
```

## Commands

### `wmdl help`

```
Usage:
  wmdl [command]

Available Commands:
  all              Run full pipeline: discover, review, and process
  catchup          Run all pending steps for incomplete weeks
  completion       Generate the autocompletion script for the specified shell
  discover         Scrape release sources and notify
  help             Help about any command
  process          Search and download approved releases
  review           Review pending releases in TUI
  search           Ad-hoc Prowlarr search + download + library add
  add              Add a title to the weekly pipeline, bypassing discovery
  mark-downloaded  Manually mark approved items as downloaded
  status           Show status of recent weeks

Flags:
      --config string   config file path
  -h, --help            help for wmdl
  -v, --verbose         enable debug logging
      --version         version for wmdl
```

### `wmdl all`

Run discover → review → process in sequence for the current week. The full pipeline
in one command.

```bash
wmdl all                         # current week
wmdl all --week 2025-W14         # specific ISO week
wmdl all --week -3               # 3 weeks ago
wmdl all --type movie            # movies only
```

### `wmdl discover`

Scrape release sources, enrich metadata (TMDB, IMDb, Rotten Tomatoes, MusicBrainz,
Hardcover), save to SQLite, and optionally send a notification.

```bash
wmdl discover                              # current week
wmdl discover --headless                   # cron/systemd (no visible browser)
wmdl discover --type movie                 # movies only
wmdl discover --type book --week 2025-W14  # books for a specific week
wmdl discover --lookback movie:2-8,tv:2-8 # one-shot lookback overrides
```

Flags:
- `--week` — target ISO week (see [week formats](#target-a-specific-week))
- `--headless` — no browser window, for cron/systemd
- `--type` — media type filter: `movie`, `tv`, `music`, `anime`, `book`
- `--lookback` — one-shot lookback override, format: `type:range[,type:range...]`
  (e.g. `movie:4-8` looks back 4-8 weeks for movie streaming)

### `wmdl review`

Opens a Bubble Tea TUI showing all pending releases. Approve or reject each one.
Shows poster art, ratings, genre, and summary.

```bash
wmdl review                        # current pending weeks
wmdl review --week 2025-W14        # specific week
```

Keyboard: `j`/`k` to move, `a`/`r` to approve/reject, `enter` to confirm,
`o` to open details page in browser.

Posters render via Kitty image protocol (Kitty, Ghostty, Rio). Other terminals
show a placeholder.

### `wmdl process`

Approved releases are searched via Prowlarr, scored by quality preferences, and
presented in a TUI picker for release selection. Chosen releases are sent to the
download client and added to your library manager.

```bash
wmdl process                         # all pending approved items
wmdl process --type tv               # TV only
wmdl process --refresh-cache         # force re-fetch *arr library caches
wmdl process --backlog               # retry previously-unfound items from all processed weeks
```

Two display modes (configurable via `process_mode`):
- `batch` (default): Searches all items first, then presents a **single
  multi-item TUI** listing all pending releases. Navigate items with `j`/`k`,
  drill into releases with `enter`, toggle with `space`, confirm with `enter`,
  skip with `s`, or abort with `q`. Phase 3 collection/series gap items appear
  under a separate section header. The TUI runs once — pick decisions for all
  items before returning to the CLI.
- `interactive`: Searches and presents each item one at a time, useful for
  seeing results immediately after each search.

**Skip handling:** If you skip a Prowlarr result, wmdl offers to add the item
to the *arr as monitored so it can search on its own schedule.

**Backlog retry:** Items that had no matching releases during a prior `wmdl process`
remain as "remaining items" (`wmdl status -v`). Retry them across all processed weeks:
- `wmdl process --backlog` — one-shot retry for all weeks with remaining items
- Set `include_backlog: true` in config to automatically retry backlog weeks after
  every normal `wmdl process` (skipped when `--week` is explicitly specified)

### `wmdl search`

Ad-hoc Prowlarr search bypassing the weekly pipeline. Supports all media types.

```bash
wmdl search "Dune: Part Two"
wmdl search --year 2024 "Dune: Part Two"
wmdl search --season 2 "Squid Game"
wmdl search --season all "Game of Thrones"       # all completed seasons
wmdl search --season 1-3 "Justified"             # season range
wmdl search --auto "The Matrix"                  # auto-confirm all prompts
wmdl search --no-library "The Office (US)"       # download only, no *arr add
wmdl search --grab "Gladiator II"                # Prowlarr grab (Prowlarr routes download)
wmdl search --format audiobook "Project Hail Mary"  # book format override
```

Flags:
- `--year`/`-y` — filter or disambiguate by year
- `--season` — season number, range (`1-3`, `S01-S03`), or `all` (TV/anime only)
- `--grab` — Prowlarr grab instead of direct download
- `--no-library` — skip adding to library manager
- `--auto` — auto-confirm all prompts
- `--format` — book format override: `ebook`, `audiobook`, `both`

### `wmdl add`

Inject a title directly into the weekly pipeline without discovery. Useful if you
heard about something outside the normal release calendar.

```bash
wmdl add --tmdb 157336                          # Movie (Interstellar)
wmdl add --tmdb 60625 --season 3                # TV (Rick and Morty S03)
wmdl add --mal 5114                             # Anime (FMAB)
wmdl add --mbid "11111111-2222-3333-4444-555555555555"  # Music release group
wmdl add --isbn "978-0-00-000000-0"             # Book
wmdl add --tmdb 27205 --status approved         # Pre-approved
```

Flags:
- `--tmdb` — TMDB ID (movie or TV)
- `--tvdb` — TVDB ID (TV only)
- `--mal` — MyAnimeList ID (anime)
- `--mbid` — MusicBrainz release group ID (music)
- `--isbn` — ISBN-13 (book)
- `--year`/`-y` — disambiguation year
- `--season` — season number (TV/anime, default: latest completed)
- `--status` — initial status: `pending` or `approved`
- `--week` — target week
- `--format` — book format: `ebook`, `audiobook`, `both`

### `wmdl mark-downloaded`

Manually mark items as downloaded. Useful when you obtained media outside wmdl
and want the pipeline to acknowledge it.

```bash
wmdl mark-downloaded release:42
wmdl mark-downloaded book:7 --format audiobook
wmdl mark-downloaded album:3
```

Flags:
- `--format` — book format: `ebook`, `audiobook`, `both`

### `wmdl status`

Displays a week-by-week progress table:

```
Week      Date        Discover  Review  Process
────────  ──────────  ────────  ──────  ───────
W21 2026  2026-05-19  ✓         ✓       ✓    

All caught up!
```

```bash
wmdl status            # progress table
wmdl status -v         # also list approved-but-not-downloaded items
```

### `wmdl catchup`

Advances each incomplete week by one step per invocation (discover → review → process).
Run repeatedly to catch up backlogged weeks in order.

### Target a specific week

All week-aware commands accept the `--week` flag:

| Format | Example | Meaning |
|--------|---------|---------|
| empty | `--week ""` | Current WMDL atomic week (most recent past Tuesday) |
| `W` + number | `--week W21` | Week 21 of current year |
| year + week | `--week 2025-W52` | ISO week 52 of 2025 |
| `MM-DD` | `--week 05-19` | May 19 of current year |
| `YYYY-MM-DD` | `--week 2025-05-19` | Specific date |
| `-N` | `--week -3` | 3 weeks ago |

## wmdl "week" definition

The WMDL atomic week runs **Wednesday to Tuesday** (anchored on Tuesday, the
traditional physical media release day). Automated setups run `wmdl discover`
every Wednesday morning by default.

**Example:** Physical release day is Tuesday May 19. The WMDL week is Wednesday
May 13 – Tuesday May 19.  Running `discover` any time from Wednesday May 20 to
Tuesday May 26 will search using May 13-19 as the atomic week.

All media types (movies, TV, music, anime, books) use the same Wed–Tue window,
with optional `lookback_weeks` to look back N atomic weeks.

Streaming releases are discovered with a default 8 week lookback to allow time for
user and critic reviews and ratings to be meaningful, and to allow series with a one
episode per week release cadence to complete and season packs to become available.
Some series have seasons of >8 episodes and during `process` will have no meaningful results.
There are 3 ways to manage: A) Set `lookback_weeks` to 10 or more. B) If presented with a choice of
unwanted torrents, skip, and later when asked, add to Sonarr as monitored. Sonarr will then
search for all missing episodes for that season and auto-grab any upcoming episodes.
C) Skip, do not add to Sonarr, and when `process` completes, check output of `wmdl status -v`
to ensure item is being tracked. Check for air date of last episode (e.g. on TVDB),
allow an additional 1-2 weeks for season packs to be released, then re-run `wmdl process`
with appropriate `--week` flag.

To see all media as soon as possible after release date, set `lookback_weeks: 0`
for all types.

When decreasing the value of `lookback_weeks` (e.g. from 8 to 2) with an existing database,
consider running `wmdl discover --lookback movie:2-8` so no releases are missed.
This command will add all streaming movies released between 2 to 8 weeks ago to the current
atomic week. Check `wmdl discover --help` for guidance. Then simply run `review` 
and `process` as usual.

## Architecture

```
              discover                      review                  process
           ┌──────────────┐            ┌──────────────┐        ┌───────────────┐
  Movies   │ DVD Release  │            │  Bubble Tea  │        │ Prowlarr      │
  TV       │ TMDB Discover│            │  approve /   │        │ search +      │
  Music    │ FlixPatrol   │  ─────►    │  reject TUI  │ ───►   │ quality       │
  Anime    │ AOTY         │            │  with poster │        │ scoring       │
  Books    │ AllMusic     │            │  preview     │        │ picker TUI    │
            │ AniList      │            │              │        │               │
            │ Tenrai       │            │              │        │               │
            │ Jikan (MAL)  │            │              │        │ download +    │
            │ Goodreads    │            │              │        │ *arr/Lidarr/  │
            │ Bookshop     │            │              │        │ LL add        │
            │ BookMarks    │            │              │        │               │
            └──────┬───────┘            └──────┬───────┘        └───────┬───────┘
                  │                           │                        │
                  └───────────────────────────┼────────────────────────┘
                                              ▼
                                         SQLite DB
                                   (~/.local/share/wmdl/wmdl.db)
```

### Enrichment

External APIs enrich each discovered item:

- **TMDB** — TMDB ID, IMDb ID, overview, genres, runtime, poster, US rating, collection membership
- **OMDB** — IMDb rating + Metacritic score + votes, awards, box office, credits
- **Rotten Tomatoes** — chromedp web scraping for critic/audience scores (requires browser)
- **MusicBrainz** — release group ID, artist details, ratings, genres
- **Hardcover** — book metadata (ISBN, pages, ratings, descriptions)
- **OpenLibrary** — book metadata fallback

### Phase 1: Prowlarr Search + TUI Picker

1. Tiered Prowlarr search (preferred indexer → resolution-specific → fallbacks)
2. Results filtered by title, year, season number, media type
3. Releases scored and sorted by quality preferences
4. TUI picker to select releases:
   - **Batch mode:** A single multi-item TUI lists all pending releases (video,
     music, books, and pre-computed Phase 3 gaps). Navigate items, pick releases
     per item, skip unwanted items — all in one session.
   - **Interactive mode:** One picker per item, shown immediately after its search
5. Torrents added to download client
6. Download records saved to SQLite

### Phase 2: Library Add

Movies/series/albums/books added to their respective library manager with
confirmation. Quality profile, root folder, and monitor settings are respected.

### Phase 3: Gap Detection

After adding an item to the library, wmdl checks for gaps:

- **Movies:** TMDB collection membership — asks if you want to add missing collection entries
- **TV:** Season > 1 — offers to search/download previous seasons
- **Books:** LazyLibrarian series membership — offers to add earlier books in the series

## Music Pipeline

### Scrapers

- **Album of the Year** (`albumoftheyear`) — HTTP+goquery scraper for critic scores and user ratings
- **AllMusic** (`allmusic`) — chromedp scraper for editor ratings (requires browser).
  Editors Choice pages provide release months only (not specific days), so results
  appear when the WMDL atomic week crosses a calendar month boundary (roughly once
  per month).

### Enrichment

MusicBrainz provides release group IDs, artist details, ratings, and genre tags.

### Quality

Music releases are scored by format priority (`flac > mp3 > aac`) and bitrate
priority (`lossless > 320 > v0 > v2`).

### Filters

```yaml
media_types:
  music:
    filter:
      min_critic_score: 75
      min_critic_reviews: 5
      min_user_score: 75
      min_user_ratings: 200
      include_must_hear: true   # special releases bypass review-count checks
```

### Library: Lidarr

Lidarr manages music with artist and album tracking. wmdl can add artists
(which auto-pulls albums) or individual albums, and trigger searches via
the Lidarr API.

```yaml
library:
  lidarr:
    url: "http://lidarr.local:8686"
    api_key: ""
    root_folder: "/media/music"
    quality_profile: "Lossless"
    metadata_profile: "Standard"
    monitor: "all"
    monitor_new_albums: true
```

## Anime Pipeline

### Scrapers

Three HTTP API providers run in priority order, then FlixPatrol as a supplemental
source:

1. **AniList** (`anilist`) — Primary provider. GraphQL API for completed anime
   (Phase A) and currently-airing (Phase B). Uses `averageScore` converted to
   a 0-10 scale. Phase A searches by season (Winter/Spring/Summer/Fall),
   Phase B tracks airing shows.
2. **Tenrai** (`tenrai`) — Secondary provider. MAL-backed API, used when
   AniList returns fewer results than expected. Same Phase A/B structure.
3. **Jikan** (`jikan`) — Tertiary fallback. MyAnimeList via Jikan API. Used
   only for `malAnimeExists` verification checks when AniList has no data.
   Same two-phase structure:
   - **Phase A:** Recently completed anime meeting score/member thresholds
   - **Phase B:** Currently-airing anime above higher thresholds — added directly
     to Sonarr without Prowlarr search (since episodes are still releasing)
- **FlixPatrol** — also catches some anime; can be deduped against all MAL-backed
  results via `filter_flixpatrol_anime`

### Filters

```yaml
media_types:
  anime:
    min_score: 7.0
    min_members: 50000
    phase_b_enabled: true
    phase_b_min_score: 7.5
    phase_b_min_members: 100000
    min_phase_b_results: 3
    filter_flixpatrol_anime: false
```

### Library

Anime goes to **Sonarr** (same config as TV). The anime-specific config
determines which seasons qualify.

## Book Pipeline

### Scrapers

- **Goodreads** (`goodreads`) — chromedp scraper for monthly popular-by-date (requires browser)
- **Goodreads Blog** (`goodreads_blog`) — HTTP+goquery scraper for weekly/editors blog posts
- **Bookshop** (`bookshop`) — chromedp scraper for curated weekly new releases (requires browser)
- **LitHub BookMarks** (`bookmarks`) — HTTP+regex scraper for more "highbrow" literary content

### Enrichment

- **Hardcover** — primary metadata (ISBN, pages, ratings, descriptions, author info)
- **OpenLibrary** — fallback when Hardcover lookup fails

### Quality

Ebook format priority: `epub > mobi > azw3 > pdf`
Audiobook format priority: `m4b > mp3 > flac > aac > opus`

### Per-Format Processing

Books support independent tracking of ebooks and audiobooks. When `default_format: "both"`,
wmdl processes each format independently — separate Prowlarr search, download, and
library add for each format.  In the `review` TUI, pressing `a` to approve an item will default to
the `default_format` setting.  Repeated presses of `a` will cycle both, ebook, audiobook, with
corresponding visual indicators (green circle, e-reader, headphones) in the TUI.

### Dedup Merge

When the same book is found by multiple scrapers, sources are merged — e.g.
`source` becomes `"goodreads,bookshop"` and notes carry data from both providers.

### Bookshop Future-Week Pre-Population

Bookshop.org has no archive URLs for past weeks, so `discover` always scrapes the current
week's releases. If `timeshift_weeks` >0, books are stored under a **future** WMDL
atomic week and appear when expected in `review` and `process`.

### Library: LazyLibrarian

wmdl manages ebooks and audiobooks through LazyLibrarian (LL), an *arr-style
library manager. When `mode: "full"`, books are downloaded to LL's **Alternate Import Folder**,
where LL reads file metadata (EPUB tags, id3 tags) to match them to library entries.
For other modes, LL handles searching and downloading to its **Download Directories**.

#### Audiobookshelf book server (optional)

While LL stores ebooks and audiobooks in separate library directories (e.g.
**eBook Library Folder** `/path/to/books/ebooks` and **AudioBook Library Folder**
`/path/to/books/audiobooks`), both download to the same Alternate Import Folder —
making Audiobookshelf a natural fit as a media server on top of that structure. Multiple options exist for detecting new media
imported by LL. The simplest is to "Enable folder watcher for library".  See this [Audiobookshelf guide](https://www.audiobookshelf.org/guides/library_creation).
Alternatively, schedule library scans or use the ABS [API to update your library](https://api.audiobookshelf.org/#scan-a-library-39-s-folders).
The example `on-dl-comp.example.sh` script could be modified to include an ABS API call.

#### Other book setups

For setups using other book library managers, set `book_backend: "none"` and configure
your download client to place completed ebook/audiobook files in your manager's
import folder. Note that these setups won't get Phase 3 series gap detection or
library status tracking (unlike LazyLibrarian). For example, [Grimmory BookDrop](https://grimmory.org/docs/bookdrop),
[BookOrbit Book Dock](https://bookorbit.app/book-dock.html) (both support ebooks and audiobooks),
or [Calibre-Web Automated](https://github.com/crocodilestick/calibre-web-automated#adding-books-to-your-library)
(ebook only, no audiobook support).

In the self-hosted book software space, no true *arr or arr-like application seems ready
to seamlessly slot-in next to the established movie, TV, and music managers.  While LL
with customization comes close, `wmdl` is designed with future support for other options
in mind. Support for the projects below is being considered but not yet implemented for the
reason(s) noted:

| Project | Notes |
|---------|-------|
| [Shelfarr](https://shelfarr.org) | request-based (like Jellyseer/Overseer), not designed to follow series or authors |
| [Chaptarr](https://hub.docker.com/r/robertlordhood/chaptarr) | private development only, no public release yet |
| [Listenarr](https://github.com/Listenarrs/Listenarr) | audiobook only |
| [ReadMeABook](https://github.com/kikootwo/readmeabook) | request-based, audiobook first, ebooks only via "shadow library" |
| [Bookshelf](https://github.com/pennydreadful/bookshelf) | requires separate instances for ebooks and audiobooks |
| [Librarry](https://github.com/bandoracer/librarry) | Go-based Readarr replacement, Hardcover-native metadata, active early alpha — most promising long-term candidate |

#### Prerequisites

**qBittorrent:** Enable `Options → Downloads → Torrent Content Layout → Create subfolder`.
Without this, all files land flat in one directory and LL may import multiple files
into a single book entry.

**LazyLibrarian config:**
- `Alternate Import/Export Folder` must differ from `Download Directory`
- `DESTINATION_COPY = True` to keep originals for seeding (`False` to move on import)
- `NEWBOOK_STATUS` / `NEWAUDIO_STATUS` — set to `Wanted` if you want author-update
  scans to auto-mark new releases as wanted. wmdl always calls `unqueueBook` after
  `addBook` in full mode to revert to `Skipped`, preventing LL from searching in parallel.

> **Metadata provider:** Phase 3 series gap detection matches Hardcover book IDs
> against LL's internal `bookid`. This works correctly only when LL is configured
> with **Hardcover as its sole metadata provider**. If LL uses additional providers
> (Goodreads, Google Books, etc.), the IDs will differ and every series member may
> appear as "missing" each run. In the WebUI, Config > Settings > Importing > Primary Information Source >
> "HardCover", then deselect "Use multiple sources for book/author information", deselect
> "Enable OpenLibrary api...", deselect "Enable Deutsche Nationalbibliothek api...", **select**
> "Enable HardCover api...", Google Books API box > empty, GoodReads API box > empty, press
> "Save Changes" button at top right. To enter your HardCover API token in the WebUI, go
> to Config > push "User Admin" button right under the top bar > Select user > pick your LL
> username > HardCover Token > enter the entire token including "Bearer" > press "Save" button
> at bottom.

**Download client categories:** Set both `ebooks` and `audiobooks` to the same category
(e.g. `"Books"`) whose save path points to LL's Alternate Import Folder. On download
completion, the external trigger script changes the torrent's category to a seeding-only
folder (e.g. `"Ebooks"` / `"Audiobooks"`) outside the import path.

#### Modes

| Mode | Prowlarr | LL Add | LL Search | Download |
|------|:---:|:---:|:---:|:---:|
| **full** | wmdl searches | `addBook` + `unqueueBook` (Skipped) | No | wmdl |
| **prowlarr-grab** | wmdl searches | skipped | No | Prowlarr route |
| **arr** | skipped | `addBook` (Wanted) | Yes (scheduled) | LL |
| **auto** | skipped | auto `addBook` (Wanted) | Yes (scheduled) | LL |
| **yolo** | skipped | auto `addBook` (Wanted) | Yes (scheduled) | LL |

- **Skipped** status (`full` mode): wmdl prevents LL from searching in parallel while
  wmdl manages the download. An external trigger (qBittorrent completion hook or cron)
  must call `importAlternate` to tell LL to scan the import folder.
- **Wanted** status (`arr`/`auto`/`yolo` modes): wmdl marks the book so LL will search
  for and download it on its own schedule.

#### External Import Trigger

After wmdl downloads a book in `full` mode, LL must be told to scan the alternate
folder. See the example script at [scripts/on-dl-comp.example.sh](scripts/on-dl-comp.example.sh)
for a complete qBittorrent completion hook that:

1. Detects format (ebook vs audiobook) from file extensions
2. Calls LL's `importAlternate` for the correct format
3. Changes the qBittorrent category to a seeding folder outside the import path
4. Sends a Gotify notification on completion

Alternatively, a cron job calling `importAlternate` every 10–30 minutes:

```bash
*/15 * * * * curl "http://lazylibrarian:5299/api?apikey=KEY&cmd=importAlternate&library=eBook"
*/15 * * * * curl "http://lazylibrarian:5299/api?apikey=KEY&cmd=importAlternate&library=AudioBook"
```
**Important:** Without moving the imported file(s) to another location, the next LL import call will re-import
the same files again. Unfortunately, LL does not skip items already present in the library.
The same file will be copied again to the same library Author/Book directory with a different filename.
If you don't need to retain the file in your download client for seeding, you can avoid this behavior by
deselecting `Settings > Processing > Keep original files`.

A Gotify notification script for LL's external script hook is at
[scripts/ll-gotify.example.sh](scripts/ll-gotify.example.sh).

## Automation (weekly discovery)

### Systemd timer

```bash
cp contrib/wmdl-discover.service ~/.config/systemd/user/
cp contrib/wmdl-discover.timer   ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now wmdl-discover.timer
```

Runs `wmdl discover --headless` every Wednesday at 05:00. Results can then be
reviewed and processed at your convenience.

### Cron

```
crontab -e
0 5 * * 3 $HOME/go/bin/wmdl discover --headless
```

## Data Storage

SQLite database at `~/.local/share/wmdl/wmdl.db`. Key tables:
`titles`, `release_events`, `downloads`, `week_state`,
`artists`, `albums`, `album_release_events`,
`authors`, `books`, `book_release_events`, `book_downloads`,
`library_cache`.

Logs at `~/.local/state/wmdl/wmdl.log` (also printed to stderr).

## Project structure

```
cmd/wmdl/           — CLI commands (cobra)
internal/discover/  — scrapers (interface-based)
internal/review/    — Bubble Tea TUI list
internal/process/   — Bubble Tea release picker + orchestration
internal/browser/   — chromedp browser tab grabber
internal/search/    — Prowlarr API client
internal/download/  — download client interface + implementations
internal/library/   — Radarr/Sonarr/Lidarr/LazyLibrarian API clients
internal/quality/   — release title parser + scorer
internal/model/     — shared types
internal/db/        — SQLite state
internal/config/    — viper config loader
internal/notifier/  — webhook notifications
```

## Example Scripts

The [scripts/](scripts/) directory contains ready-to-use shell scripts:

- **`on-dl-comp.example.sh`** — qBittorrent completion hook for LazyLibrarian import:
  detects format, triggers `importAlternate`, changes category to seeding folder,
  sends Gotify notification.
- **`ll-gotify.example.sh`** — LazyLibrarian external script hook: sends rich
  Gotify notifications (title, description, cover image) when LL imports a book.

Copy and customize these for your setup.

## Building

```bash
make build   # go build ./cmd/wmdl
make test    # go test ./...
make lint    # golangci-lint run ./...
make check   # fmt + vet + lint + test + build
```
