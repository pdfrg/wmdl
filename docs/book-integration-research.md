# Book Integration Research

Research conducted 2026-06-09 for wmdl book integration architecture.

---

## Current State

The book pipeline ends at "download + trigger ABS scan" — there is **no *arr-style library management** for books. No `addToReadarr` step exists. Series info (`Book.SeriesID`/`SeriesName`) is stored but unused in processing logic. Books are skipped in `"arr"`, `"auto"`, and `"yolo"` modes.

### Current book flow

```
wmdl discover → review → search Prowlarr → pick torrent → download → trigger ABS scan
                                                                      └─ ABS has NO auto-import, NO renaming
```

### Desired *arr-style pattern (same as movies/TV)

```
wmdl discover → review → search Prowlarr → pick torrent → download → *arr app (add to library, monitor, rename, organize)
```

---

## Readarr Replacements

| App | Type | Ebooks | Audiobooks | Series | Prowlarr | API | Maturity | Verdict |
|-----|------|--------|------------|--------|----------|-----|----------|---------|
| **LazyLibrarian** | *arr-style (monitored) | ✅ | ✅ | ✅ Full | ✅ Torznab | ✅ 100+ cmds | 15+ yrs, ~280★ | **Recommended** |
| **Shelfarr** | Request-based | ✅ | ✅ | ✅ Metadata | ✅ Native | ✅ REST | Active, ~200★ | Contender |
| Chaptarr | Not public | - | - | - | - | - | Too early | ❌ |
| Listenarr | Audiobook only | ❌ | ✅ | - | - | - | N/A | ❌ |
| ReadMeABook | Shadow library | Partial | Primary | - | ❌ | - | Wrong model | ❌ |
| PennyDreadful/Bookshelf | Separate instances | ✅ | ✅ | Roadmap | - | - | Not unified | ❌ |
| Livrarr | Alpha | - | - | - | - | - | Messy | ❌ |
| Bothari/Athenaeum | Buggy | - | - | - | - | - | Too early | ❌ |

---

## LazyLibrarian (Recommended)

**Repository:** https://gitlab.com/LazyLibrarian/LazyLibrarian
**Language:** Python
**License:** GPL v3
**Category:** *arr-style app (NOT a media server)

| Feature | Details |
|---------|---------|
| **Auto-import** | ✅ `forceProcess` API — scans download dir. ✅ `importAlternate` API — processes alternate import folder via file metadata matching (EPUB tags, id3 tags, OPF files). Scheduled `process_dir()` runs every 10 min. |
| **Renaming** | ✅ Configurable naming patterns (`{author}`, `{title}`, `{series}`, `{seriesNum}`, `{narrator}`). Writes metadata.opf. |
| **Torrent clients** | ✅ Native: qBittorrent, Deluge, Transmission, rTorrent, utorrent |
| **Prowlarr** | ✅ Via Torznab. Separate cat config: ebook=7020, audiobook=3030. Categories auto-routed to ebook vs audiobook search. |
| **API** | ✅ 100+ REST commands at `/api?apikey=X&cmd=...`. JSON responses. Full access required. |
| **Both formats** | ✅ Separate `Status` (ebook) and `AudioStatus` (audiobook) per book with independent wanted/skipped/have tracking. |
| **Series tracking** | ✅ `getSeriesMembers`, `getSeriesAuthors`, `deleteEmptySeries` APIs. Series detail in UI. |
| **DB** | SQLite (matches wmdl stack). No additional DB server needed. |
| **Standalone** | Acts as both *arr AND book library manager. OPDS for external readers. No separate media server required. |

### Key API Commands for wmdl Integration

| Command | Parameters | Description |
|---------|------------|-------------|
| `addAuthorID` | `&id=<AuthorID> [&books=true]` | Add author (and optionally all books) with default status `Skipped` (unmonitored) |
| `addBook` | `&id=<BookID>` | Add individual books to DB |
| `queueBook` | `&id=<BookID> [&type=eBook\|AudioBook]` | Set book to `Wanted` — triggers LL search |
| `unqueueBook` | `&id=<BookID> [&type=eBook\|AudioBook]` | Set book to `Skipped` — stops search |
| `importAlternate` | `[&dir=<path>] [&library=eBook\|AudioBook]` | Import books from alternate folder — reads file metadata, matches to DB, copies/moves to library |
| `forceProcess` | `[&dir=<path>] [ignorekeepseeding]` | Process download dir — match files to DB, import, rename, organize |
| `forceLibraryScan` | `[&dir=] [&id=] [&wait] [&remove]` | Scan library dir — auto-add new authors/books from files |
| `forceBookSearch` | `[&type=eBook\|AudioBook]` | Search all wanted books |
| `searchBook` | `&id=<BookID> [&type=eBook\|AudioBook]` | Trigger search for one specific book |
| `getAuthor` | `&id=<AuthorID>` | Get author + all their books with statuses |
| `getSeriesMembers` | `&series=<SeriesID>` | List members of a book series |
| `getWanted` | - | List all wanted books |

### Book Status Values

| Status | Meaning |
|--------|---------|
| `Wanted` | LL actively searches for this book |
| `Skipped` | In library, no search (default for new additions) |
| `Have` | File on disk |
| `Open` | File available/open |
| `Ignored` | Will not be searched |
| `Snatched` | Download in progress |
| `Failed` | Download failed |

### Default Book Status (Config)

| Config Key | Default | Purpose |
|------------|---------|---------|
| `NEWAUTHOR_STATUS` | `Skipped` | Default ebook status for new authors |
| `NEWAUTHOR_AUDIO` | `Skipped` | Default audiobook status for new authors |
| `NEWBOOK_STATUS` | `Skipped` | Default ebook status for existing authors' new books |
| `NEWAUDIO_STATUS` | `Skipped` | Default audiobook status for existing authors' new books |
| `NEWAUTHOR_BOOKS` | `False` | Whether to fetch books when adding author |

The defaults are `Skipped`, but users may set `NEWBOOK_STATUS`/`NEWAUDIO_STATUS` to `Wanted` so that author-update-scanning discovers new books as wanted. For wmdl-driven search, wmdl calls `addBook` (which uses these config keys) then immediately calls `unqueueBook` to revert to `Skipped` — preventing LL from searching in parallel while keeping the user's preferred defaults for other workflows.

### Integration Flow: wmdl-Drives-Search (recommended)

This is the "add as unmonitored + post-process" pattern, matching how wmdl works with Radarr/Sonarr for movies/TV:

1. wmdl discovers books via scrapers + enrichment (existing pipeline)
2. User reviews in wmdl TUI, approves specific books
3. wmdl calls `addBook?&id=BOOKID` → adds to LL (status uses `NEWBOOK_STATUS`/`NEWAUDIO_STATUS`, typically `Wanted` if user has those set)
4. wmdl calls `unqueueBook?&id=BOOKID&type=eBook` → sets ebook status to `Skipped`
5. wmdl calls `unqueueBook?&id=BOOKID&type=AudioBook` → sets audiobook status to `Skipped`
6. wmdl handles Prowlarr search + torrent picking (existing book pipeline)
7. wmdl sends download to qBittorrent with `ebooks`/`audiobooks` category → downloads to LL's alternate import folder
8. External trigger (qBittorrent "on completion" script or cron) calls:
   - `importAlternate?&library=eBook` → LL reads file metadata (EPUB tags, OPF), matches to DB, copies to library, marks `Have`
   - `importAlternate?&library=AudioBook` → same for audiobooks (reads id3 tags)

**Why this works:** LL's `importAlternate` (backed by `process_alternate()` in `manual_import.py`) does metadata-based matching — it reads embedded EPUB/MOBI/AZW tags or id3 tags for audiobooks and fuzzy-matches author+title against the DB. No `LL.(bookid)` filename hack needed. The `DESTINATION_COPY` config controls whether files are copied or moved.

**Why the two-step add→unqueue:** Users often set `NEWBOOK_STATUS=Wanted` so that LL's scheduled author-update scans discover new books as Wanted. wmdl separates concerns by calling `addBook` (respects user's config) then immediately calling `unqueueBook` to prevent LL from also searching for the same book. Since LL searches on a schedule (not instantly), there's no race condition.

---

## Shelfarr (Contender)

**Repository:** https://github.com/Pedro-Revez-Silva/shelfarr
**Language:** Ruby on Rails 8, SQLite
**Stars:** ~200
**License:** GPL-3.0
**Category:** Request-based management system (not monitor-based)

### Strengths
- Native Prowlarr integration (not just Torznab)
- Configurable renaming templates (`{author}`, `{series}`, `{seriesNum:00}`)
- Audiobookshelf sync built in
- Clean API: `POST /api/v1/requests`, `GET /api/v1/requests`, search endpoint
- Both ebook + audiobook in single instance
- Multiple download clients with priority ordering
- Direct download sources (Anna's Archive, Z-Library, LibriVox)

### Weaknesses for wmdl
- **Request-based model** (not monitor-based like Radarr) — no per-book monitored/unmonitored toggle
- Queue processes every 5 min; no way to fully disable interval searching
- Ebook renaming reported to have issues
- If wmdl initiates Prowlarr searches, Shelfarr needs to be in "passthrough" mode it wasn't designed for
- Each request goes through fixed lifecycle; no "add unmonitored" concept

### Verdict
Worth watching. Well-designed software, but the request-based model differs from the monitor-based *arr pattern wmdl uses for movies/TV.

---

## Book Server Alternatives (NOT Readarr replacements)

These are media servers. None have built-in search/download automation. They serve as the "destination" after downloads complete, or as an alternative to Audiobookshelf.

### Comparison

| Feature | Audiobookshelf (current) | Grimmory | Calibre-Web-Automated | BookOrbit |
|---------|--------------------------|----------|-----------------------|-----------|
| *Arr automation | ❌ | ❌ | ❌ | ❌ |
| Auto-import folder | ❌ (scan only) | ✅ BookDrop | ✅ Ingest | ✅ Book Dock |
| Ebooks | ✅ | ✅ | ✅ | ✅ |
| Audiobooks | ✅ | ✅ | ❌ | ✅ |
| DB | SQLite | **MariaDB** | SQLite | **PostgreSQL** |
| Renaming | ❌ | Partial | Via Calibre | ✅ Templates |
| Web reader | ✅ | ✅ | ✅ | ✅ |
| Device sync | ✅ | ✅ Kobo/KOReader | ✅ Kobo/KOReader | ✅ Kobo/KOReader |
| Auto-finalize | N/A | ❌ | ✅ | ❌ |
| Stars | ~7k | ~3.4k | ~5.7k | ~900 |

### Auto-Import Behavior

| Server | Watched Folder | Auto-Detect | Auto-Finalize | wmdl API Call Needed |
|--------|---------------|-------------|---------------|---------------------|
| **Audiobookshelf** | ❌ None | N/A | N/A | Scan trigger only |
| **Grimmory** | `/bookdrop` | ✅ FS events | ❌ Manual finalize | `POST /api/v1/bookdrop/imports/finalize` |
| **Calibre-Web-Automated** | `/cwa-book-ingest` | ✅ Polling | ✅ Auto-ingests | None needed |
| **BookOrbit** | `{data}/book-dock` | ✅ `@parcel/watcher` | ❌ Manual finalize | `POST /book-dock/finalize` |

**Key finding:** Grimmory and BookOrbit require explicit finalize API calls. Neither has auto-finalize. Both auto-detect metadata from file internals + online enrichment — no prior book registration required. They take everything found in the watched folder.

**Only Calibre-Web-Automated** auto-ingests fully automatically, but is **ebooks only** (no audiobooks).

---

## Long-Term: Flexibility Architecture

The self-hosted ebook/audiobook space is fragmented. No single tool is "definitive." The recommendation is a **pluggable backend interface** supporting multiple tools:

```go
type BookClient interface {
    Ping(ctx context.Context) error
    AddAuthor(ctx context.Context, authorID string, fetchBooks bool) (*AuthorResult, error)
    AddBook(ctx context.Context, bookID string) (*BookResult, error)
    QueueBook(ctx context.Context, bookID string, format BookFormat) error
    UnqueueBook(ctx context.Context, bookID string, format BookFormat) error
    GetBookStatus(ctx context.Context, bookID string) (*BookStatus, error)
    GetSeriesMembers(ctx context.Context, seriesID string) ([]*SeriesMember, error)
    ImportAlternate(ctx context.Context, dir string, format BookFormat) error
}
```

### Supported Backend Candidates

| Backend | Pattern | Ebooks | Audiobooks | Status |
|---------|---------|--------|------------|--------|
| **LazyLibrarian** | *arr-style (monitored) | ✅ | ✅ | **Phase 1 target** |
| **Shelfarr** | Request-based | ✅ | ✅ | Phase 2 candidate |
| **Audiobookshelf** | Media server (scan only) | ✅ | ✅ | Keep as optional |
| **Grimmory** | Media server (BookDrop) | ✅ | ✅ | Phase 2 candidate |
| **BookOrbit** | Media server (Book Dock) | ✅ | ✅ | Phase 2 candidate |

---

## Recommended Implementation Phases

### Phase 1: LazyLibrarian Integration
- [ ] Add `internal/library/lazylibrarian.go` — `LazyLibrarianClient` following the same interface pattern as `radarr.go`/`sonarr.go` (`Ping`, `AddBook`, `UnqueueBook`, `GetBookStatus`, `GetSeriesMembers`, `ImportAlternate`)
- [ ] Add LL config to `internal/config/config.go` (url, api_key, root_folder, quality_profile)
- [ ] Integrate into book processing pipeline in `internal/process/executor.go`:
  - `addToLazyLibrarian()` — called after user approves book, adds unmonitored
  - Wire into arr/auto/yolo modes alongside current download-flow
- [ ] Show series info in review TUI (already stored as `Book.SeriesID`/`SeriesName` but not displayed)
- [ ] Add Phase 3 book series gap checking: after adding a book, check if earlier books in series exist in LL's library
- [ ] Keep ABS integration as optional fallback for users who don't want LL

### Phase 2: Interface Abstraction
- [ ] Extract `BookClient` interface from LL implementation
- [ ] Add `book_backend` config key (`lazylibrarian`, `audiobookshelf`, `shelfarr`, `grimmory`, `bookorbit`)
- [ ] Implement Shelfarr backend (API-based request creation)
- [ ] Implement Grimmory/BookOrbit backends (BookDrop/Book Dock finalize pattern)
- [ ] Document backend integration patterns in AGENTS.md

### Phase 3: Polish & Dual-Format
- [ ] Handle ebook+audiobook dual-format properly across all backends
- [ ] Upgrade detection for books (similar to streaming→bluray for movies)
- [ ] Config validation per backend
