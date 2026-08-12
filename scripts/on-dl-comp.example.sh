#!/bin/bash

# =============================================
# LazyLibrarian Import Trigger for qBittorrent
# =============================================
#
# Copy this file (e.g. on-dl-comp.sh), fill in your
# credentials, then register it in qBittorrent:
#   Settings → Downloads → Run external program
#     /path/to/on-dl-comp.sh "%F" "%L" "%N" "%I"
#
# Prerequisites:
#   1. Create seeding categories in qBittorrent
#      (e.g. "Ebooks" and "Audiobooks") with save
#      paths OUTSIDE LL's Alternate Import Folder.
#   2. LL config: DESTINATION_COPY = True
#   3. qBittorrent config: Torrent Content Layout
#      → Create subfolder (enabled)
#
# How it works:
#   A gotify notification is always sent when a torrent
#   completes, regardless of category or LL availability.
#   wmdl adds torrents with category = QB_IMPORT_CAT
#   (save path = LL's Alternate Import Folder).
#   When qBittorrent finishes downloading, this script:
#     1. Detects format (ebook/audiobook) from file
#        extension in the content path
#     2. Calls LL's importAlternate for that format only
#     3. Changes the qBittorrent category to the seeding
#        category, moving files OUT of the LL import folder
#        while preserving seeding
#   Future importAlternate calls find nothing to re-import.
#   All curl calls use --connect-timeout/--max-time so a
#   stopped or unreachable LL fails fast instead of hanging.

# ── LazyLibrarian ───────────────────────────────────
LL_URL="http://lazylibrarian:5299"
API_KEY="your-lazylibrarian-api-key"
LOGFILE="/var/log/ll-import.log"

# ── qBittorrent ─────────────────────────────────────
QB_URL="http://localhost:8080"
QB_USER="admin"
QB_PASS=""                                 # leave empty if QB has no password
QB_IMPORT_CAT="Books"                      # must match wmdl's downloader.categories.ebooks
QB_CAT_EBOOK_SEEDING="Ebooks"              # where to move ebooks after LL import
QB_CAT_AUDIOBOOK_SEEDING="Audiobooks"      # where to move audiobooks after LL import

# ── Gotify (optional) ───────────────────────────────
GOTIFY_URL=""
GOTIFY_TOKEN=""

# ────────────────────────────────────────────────────
# qBittorrent passes: %F %L %N %I
CONTENT_PATH="$1"
CATEGORY="$2"
TORRENT_NAME="$3"
INFO_HASH="$4"
GOTIFY_MESSAGE="$TORRENT_NAME"

log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') - $1" >> "$LOGFILE"
}

detect_format() {
    local path="$1"
    if [ -f "$path" ]; then
        case "${path,,}" in
            *.epub|*.mobi|*.pdf|*.cbz|*.cbr|*.azw3|*.djvu|*.docx)
                echo "ebook"
                return
                ;;
            *.mp3|*.m4b|*.aax|*.opus|*.flac|*.ogg|*.wma)
                echo "audiobook"
                return
                ;;
        esac
    elif [ -d "$path" ]; then
        for f in "$path"/*; do
            [ -f "$f" ] || continue
            case "${f,,}" in
                *.epub|*.mobi|*.pdf|*.cbz|*.cbr|*.azw3)
                    echo "ebook"
                    return
                    ;;
                *.mp3|*.m4b|*.aax|*.opus|*.flac)
                    echo "audiobook"
                    return
                    ;;
            esac
        done
    fi
    echo "unknown"
}

qblogin() {
    if [ -z "$QB_PASS" ]; then
        echo ""
        return
    fi
    curl -s --connect-timeout 10 --max-time 30 -c - "${QB_URL}/api/v2/auth/login" \
        -d "username=${QB_USER}&password=${QB_PASS}" 2>/dev/null \
        | grep "SID" | awk '{print $NF}'
}

qbsetcat() {
    local hash="$1" cat="$2" sid="$3"
    if [ -z "$hash" ]; then
        log "warning: no info hash provided, cannot change category"
        return
    fi
    local args=(-s --connect-timeout 10 --max-time 30 -o /dev/null -w "%{http_code}" -X POST \
        "${QB_URL}/api/v2/torrents/setCategory" \
        -d "hashes=${hash}&category=${cat}")
    [ -n "$sid" ] && args+=(-b "SID=${sid}")
    local code
    code=$(curl "${args[@]}")
    if [ "$code" = "200" ]; then
        log "✓ category changed to '${cat}' for ${TORRENT_NAME}"
    else
        log "✗ category change failed (HTTP ${code}) for ${TORRENT_NAME}"
    fi
}

send_gotify() {
    local msg="$1"
    if [ -n "$GOTIFY_URL" ] && [ -n "$GOTIFY_TOKEN" ]; then
        curl -s --connect-timeout 10 --max-time 30 "${GOTIFY_URL}/message?token=${GOTIFY_TOKEN}" \
            -F "title=Download Finished" \
            -F "message=$msg" \
            -F "priority=5" > /dev/null 2>&1 \
            || log "✗ gotify notification failed for: $msg"
    else
        log "warning: GOTIFY_URL/GOTIFY_TOKEN not set, skipping notification for: $msg"
    fi
}

log "Torrent completed: $TORRENT_NAME (category: $CATEGORY)"
send_gotify "$GOTIFY_MESSAGE"

if [ "$CATEGORY" != "$QB_IMPORT_CAT" ]; then
    log "warning: category '$CATEGORY' != '$QB_IMPORT_CAT', skipping LL import"
    exit 0
fi

FORMAT=$(detect_format "$CONTENT_PATH")
log "Detected format: $FORMAT"

QB_SID=$(qblogin)

case "$FORMAT" in
    ebook)
        log "Triggering LL eBook import..."
        RESPONSE=$(curl -s --connect-timeout 10 --max-time 30 -w "%{http_code}" -o /dev/null \
            "${LL_URL}/api?apikey=${API_KEY}&cmd=importAlternate&library=eBook&wait=1")
        if [ "$RESPONSE" = "200" ]; then
            log "✓ eBook importAlternate successful"
            GOTIFY_MESSAGE+=" ✓ebook imported"
            sleep 3
            qbsetcat "$INFO_HASH" "$QB_CAT_EBOOK_SEEDING" "$QB_SID"
        else
            log "✗ eBook importAlternate failed (HTTP $RESPONSE)"
            GOTIFY_MESSAGE+=" ✗ebook import failed (HTTP $RESPONSE)"
        fi
        ;;
    audiobook)
        log "Triggering LL AudioBook import..."
        RESPONSE=$(curl -s --connect-timeout 10 --max-time 30 -w "%{http_code}" -o /dev/null \
            "${LL_URL}/api?apikey=${API_KEY}&cmd=importAlternate&library=AudioBook&wait=1")
        if [ "$RESPONSE" = "200" ]; then
            log "✓ AudioBook importAlternate successful"
            GOTIFY_MESSAGE+=" ✓audiobook imported"
            sleep 3
            qbsetcat "$INFO_HASH" "$QB_CAT_AUDIOBOOK_SEEDING" "$QB_SID"
        else
            log "✗ AudioBook importAlternate failed (HTTP $RESPONSE)"
            GOTIFY_MESSAGE+=" ✗audiobook import failed (HTTP $RESPONSE)"
        fi
        ;;
    *)
        log "warning: unknown format, importing both"
        RESPONSE_EB=$(curl -s --connect-timeout 10 --max-time 30 -w "%{http_code}" -o /dev/null \
            "${LL_URL}/api?apikey=${API_KEY}&cmd=importAlternate&library=eBook&wait=1")
        RESPONSE_AB=$(curl -s --connect-timeout 10 --max-time 30 -w "%{http_code}" -o /dev/null \
            "${LL_URL}/api?apikey=${API_KEY}&cmd=importAlternate&library=AudioBook&wait=1")
        [ "$RESPONSE_EB" = "200" ] && log "✓ eBook importAlternate successful" || log "✗ eBook importAlternate failed (HTTP $RESPONSE_EB)"
        [ "$RESPONSE_AB" = "200" ] && log "✓ AudioBook importAlternate successful" || log "✗ AudioBook importAlternate failed (HTTP $RESPONSE_AB)"
        sleep 3
        qbsetcat "$INFO_HASH" "${QB_CAT_EBOOK_SEEDING}" "$QB_SID"
        ;;
esac

send_gotify "$GOTIFY_MESSAGE"
