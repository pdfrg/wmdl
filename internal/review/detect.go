package review

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

const (
	posterCols = 30
)

var (
	fontW, fontH    int
	kittySupported  bool
	posterAvailable bool
)

func detectTerminal() {
	kittySupported = detectKitty()
	fontW, fontH = detectCellSize()
	posterAvailable = kittySupported && fontW > 0 && fontH > 0
}

func detectKitty() bool {
	if os.Getenv("KITTY_WINDOW_ID") != "" {
		return true
	}
	switch os.Getenv("TERM_PROGRAM") {
	case "kitty", "ghostty", "WezTerm", "rio":
		return true
	}
	return strings.Contains(os.Getenv("TERM"), "kitty")
}

func detectCellSize() (int, int) {
	fd := int(os.Stdout.Fd())
	if !term.IsTerminal(fd) {
		return 7, 14
	}

	if w, h, ok := queryCSI16t(); ok && w > 0 && h > 0 {
		if w <= h {
			return w, h
		}
		return h, w
	}

	if w, h, ok := queryTIOCGWINSZ(fd); ok && w > 0 && h > 0 {
		return w, h
	}

	return fallbackCellSize()
}

func queryCSI16t() (int, int, bool) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return 0, 0, false
	}
	defer func() { _ = f.Close() }()

	oldState, err := term.MakeRaw(int(f.Fd()))
	if err != nil {
		return 0, 0, false
	}
	defer func() { _ = term.Restore(int(f.Fd()), oldState) }()

	_, _ = f.Write([]byte("\x1b[16t"))

	_ = f.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	reader := bufio.NewReaderSize(f, 64)
	var buf strings.Builder
	for {
		b, err := reader.ReadByte()
		if err != nil {
			break
		}
		buf.WriteByte(b)
		if b == 't' {
			break
		}
	}

	raw := buf.String()
	if !strings.HasPrefix(raw, "\x1b[6;") {
		return 0, 0, false
	}
	parts := strings.Split(strings.TrimPrefix(raw, "\x1b[6;"), ";")
	if len(parts) != 2 {
		return 0, 0, false
	}
	h, _ := strconv.Atoi(strings.TrimSuffix(parts[0], "t"))
	w, _ := strconv.Atoi(strings.TrimSuffix(parts[1], "t"))
	if h <= 0 || w <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

func queryTIOCGWINSZ(fd int) (int, int, bool) {
	w, h, err := term.GetSize(fd)
	if err != nil || w <= 0 || h <= 0 {
		return 0, 0, false
	}

	ws, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
	if err != nil || ws.Xpixel <= 0 || ws.Ypixel <= 0 {
		return 0, 0, false
	}

	cellW := int(ws.Xpixel) / w
	cellH := int(ws.Ypixel) / h
	if cellW <= 0 || cellH <= 0 {
		return 0, 0, false
	}
	if cellW > cellH {
		cellW, cellH = cellH, cellW
	}
	return cellW, cellH, true
}

func fallbackCellSize() (int, int) {
	switch os.Getenv("TERM_PROGRAM") {
	case "kitty", "rio", "WezTerm":
		return 8, 16
	case "ghostty":
		return 9, 18
	}
	term := os.Getenv("TERM")
	if strings.Contains(term, "foot") {
		return 8, 16
	}
	return 7, 14
}
