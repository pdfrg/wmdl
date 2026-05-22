# wmdl

**Weekly Media Downloader** — discover new DVD and streaming releases, review them in a TUI, then auto-search torrents and add them to your media library.

Stop manually browsing release calendars and copy-pasting titles into Prowlarr. `wmdl` automates the weekly ritual:

- **Discover** — scrapes DVD release dates, streaming premieres, and TMDB for what's new this week
- **Review** — approve/reject each title in a Bubble Tea TUI with poster preview and ratings
- **Process** — searches Prowlarr across your indexers, scores releases by quality preferences, and sends the best match to your torrent client + Radarr/Sonarr

Powered by qBittorrent, Transmission, or Deluge. Notifies via Gotify, Slack, Discord, or ntfy.

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
