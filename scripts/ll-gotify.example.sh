#!/bin/bash

# =============================================
# LazyLibrarian → Gotify Book Notification
# =============================================
#
# Sends a rich notification (title, description,
# cover image) via Gotify when LL imports a book.
#
# Configure in LazyLibrarian:
#   Settings → Notification Agents → External Script
#     Script: /path/to/ll-gotify.sh
#     On Book: ✓
#
# LL passes book metadata as positional arguments.
# The field positions below match LL's documented
# external script argument layout.

# ── Gotify config ──────────────────────────────────
GOTIFY_URL="http://gotify:8080"
GOTIFY_TOKEN="your-gotify-app-token"
PRIORITY=5

# ── LazyLibrarian ──────────────────────────────────
LL_BASE="http://lazylibrarian:5299"
LOGFILE="/var/log/ll-gotify.log"

# ────────────────────────────────────────────────────
echo "=== $(date) ===" >> "$LOGFILE"

# LL passes book metadata as positional args
BOOKNAME="${4}"
BOOKSUB="${6}"
BOOKDESC="${8}"
BOOKGENRE="${10}"
BOOKPAGES="${20}"
BOOKLINK="${22}"
BOOKIMG="${18}"

json_escape() {
    printf '%s' "$1" | python3 -c '
import sys, json
print(json.dumps(sys.stdin.read()), end="")
' 2>/dev/null || printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g; s/\n/\\n/g; s/\r/\\r/g; s/\t/\\t/g'
}

# Build title
if [ -n "$BOOKSUB" ] && [ "$BOOKSUB" != "None" ]; then
    TITLE="📖 ${BOOKNAME}: ${BOOKSUB}"
else
    TITLE="📖 ${BOOKNAME}"
fi

# Detect format from argument ~$90
if [[ "${90}" == *"Audio"* ]]; then
    TYPE="🎧 Audiobook"
else
    TYPE="📘 eBook"
fi

IMAGE_URL="${LL_BASE}/${BOOKIMG}"

MESSAGE="${TYPE} added to library

![Cover](${IMAGE_URL})

${BOOKDESC}

**Genre:** ${BOOKGENRE:-None}
**Pages:** ${BOOKPAGES:-Unknown}
**Link:** [Open](${BOOKLINK})"

ESC_TITLE=$(json_escape "$TITLE")
ESC_MESSAGE=$(json_escape "$MESSAGE")
ESC_IMAGE=$(json_escape "$IMAGE_URL")

echo "Title: $TITLE" >> "$LOGFILE"
echo "Image URL: $IMAGE_URL" >> "$LOGFILE"
echo "Message length: ${#MESSAGE}" >> "$LOGFILE"

curl -s -X POST "${GOTIFY_URL}/message?token=${GOTIFY_TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '{
    "title": '"${ESC_TITLE}"',
    "message": '"${ESC_MESSAGE}"',
    "priority": '"${PRIORITY}"',
    "extras": {
      "client::display": {"contentType": "text/markdown"},
      "client::notification": {"bigImageUrl": '"${ESC_IMAGE}"'}
    }
  }' >> "$LOGFILE" 2>&1

echo "----------------------------------------" >> "$LOGFILE"
exit 0
