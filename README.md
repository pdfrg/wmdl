# wmdl

[![CI](https://github.com/pdfrg/wmdl/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/pdfrg/wmdl/actions/workflows/ci.yml)

**Weekly Media Downloader** — discover new DVD/Bluray and streaming releases, review them in a TUI,
then auto-search torrents and add them to your media library. Stop manually browsing release
calendars and copy-pasting titles into Prowlarr. `wmdl` automates the weekly ritual:

![wmdl-review-tui](assets/wmdl-review.png)

- **Discover** — searches DVD/Bluray release dates, streaming premieres, and TMDB for what's new this week
- **Review** — approve/reject each title in a Bubble Tea TUI with poster preview and ratings
- **Process** — searches Prowlarr across your indexers, scores releases by quality preferences,
shows you the top candidates, and sends your preferred match to your torrent client + Radarr/Sonarr

Compatible with qBittorrent, Transmission, or Deluge. Notifies via Gotify, Slack, Discord, ntfy,
or generic webhook.

![wmdl-process-tui](assets/wmdl-process.png)

## Features

- **Automation** — Run weekly with `cron` or `systemd`. Notifies you when items are ready to review.
Auto-add to torrent client, auto-add to Radarr/Sonarr (with confirmation).
- **Tracking** — Easily check if you're up to date with `wmdl status`. Lets you know if there are
any weeks where `wmdl discover` didn't run (power outage, just forgot, whatever), or where you couldn't
find the right torrent. Reports e.g. `2/4` and reminds you to run `wmdl process --week` or `wmdl
catchup`.
- **Broad Content Included** — Sick of all the same stuff? Includes numerous non-US and non-English
media, with country of origin flags and spoken language information.
- **Movie/TV Posters** — Uses Kitty image protocol.  Kitty, Ghostty, or Rio terminals recommended. Other
terminals work for core functionality, image placeholder shown.
- **Media Info and Ratings** - Brief overview, Rotten Tomatoes, IMDb, TMDB, Metacritic scores, YouTube
trailer views, genres, runtime, and US content rating (e.g. R, PG-13, TV-14).
- **Smart Torrent Filtering** — No wasting your time combing through obviously wrong search results.
Title mismatches are immediately discarded.  Remaining results ranked by your resolution, codec,
and release group preferences.  Top 10 matches presented for your choosing.
- **Private Tracker Support** — Configure your preferred indexer.  If not available, falls back to
all Prowlarr indexers.
- **TV Backlog Filling** — Just found out about a show and it's already season 3? `wmdl` detects
and offers to look for seasons 1 and 2.
- **Movie Colection Completion** — When you add a movie in a TMDB collection, `wmdl` checks if you have
all the others.  If not, offers to search.

## Quick start

```bash
go install github.com/pdfrg/wmdl/cmd/wmdl@latest
cp config.yaml.example ~/.config/wmdl/config.yaml
$EDITOR ~/.config/wmdl/config.yaml   # add TMDB API key + Prowlarr URL + downloader
wmdl all
```

## Documentation

See [DOCUMENTATION.md](DOCUMENTATION.md) for commands, configuration reference, architecture, and automation.

## License

MIT
