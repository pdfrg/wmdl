package browser

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/chromedp/chromedp"
)

func EnsureRunning(binary string, debugPort int, profile string, headless bool) (func() error, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", debugPort)

	// Kill any existing browser on the debug port (stale compositor state causes
	// chromedp hangs when creating tabs).
	if pid := findProcessOnPort(debugPort); pid > 0 {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Signal(syscall.SIGTERM)
			// Wait up to 5s for it to release the port
			for i := 0; i < 10; i++ {
				conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
				if err != nil {
					break
				}
				conn.Close()
				time.Sleep(500 * time.Millisecond)
			}
			_ = p.Kill() // ensure dead
		}
	}

	// Wait for the port to be free (in case the kill is still in progress)
	for i := 0; i < 10; i++ {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err != nil {
			break
		}
		conn.Close()
		time.Sleep(500 * time.Millisecond)
	}

	userDataDir := filepath.Join(os.ExpandEnv("$HOME/.local/share/wmdl/browser"), profile)
	if err := os.MkdirAll(userDataDir, 0755); err != nil {
		return nil, fmt.Errorf("creating %s data dir: %w", binary, err)
	}

	args := []string{
		fmt.Sprintf("--remote-debugging-port=%d", debugPort),
		fmt.Sprintf("--user-data-dir=%s", userDataDir),
		"--no-first-run",
	}
	if headless {
		args = append(args, "--headless=new")
	}
	args = append(args, "--new-window", "about:blank")

	cmd := exec.Command(binary, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("launching %s: %w", binary, err)
	}

	// Wait for it to start listening
	for i := 0; i < 30; i++ {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return cmd.Process.Kill, nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	cmd.Process.Kill()
	return nil, fmt.Errorf("%s started but not listening on %s within 15s", binary, addr)
}

// findProcessOnPort tries to discover the PID of a process listening on the
// given TCP port using lsof or /proc/net/tcp. Returns 0 if no process is found.
func findProcessOnPort(port int) int {
	// Try lsof first (most common on Linux)
	out, err := exec.Command("lsof", "-ti", fmt.Sprintf(":%d", port)).Output()
	if err == nil {
		pidStr := strings.TrimSpace(string(out))
		if pidStr != "" {
			pid, err := strconv.Atoi(pidStr)
			if err == nil && pid > 0 {
				return pid
			}
		}
	}

	// Fallback: parse /proc/net/tcp
	return findPIDByProcNet(port)
}

// findPIDByProcNet parses /proc/net/tcp to find the inode of a socket
// listening on the given port, then scans /proc/*/fd/* for that inode.
func findPIDByProcNet(port int) int {
	data, err := os.ReadFile("/proc/net/tcp")
	if err != nil {
		return 0
	}

	hexPort := fmt.Sprintf(":%04X", port)
	var inode string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		// Fields: sl local_address rem_address st ... inode
		// local_address is hex "00000000:XXXX"
		if strings.HasSuffix(fields[1], hexPort) && fields[3] == "0A" {
			inode = fields[9]
			break
		}
	}
	if inode == "" {
		return 0
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		fdDir := fmt.Sprintf("/proc/%d/fd", pid)
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			if strings.Contains(link, "socket:["+inode+"]") {
				return pid
			}
		}
	}
	return 0
}

func ListTabs(ctx context.Context, debugURL string) ([]Tab, error) {
	allocCtx, allocCancel := chromedp.NewRemoteAllocator(ctx, debugURL)
	defer allocCancel()

	// Need at least a short-lived context to trigger the target listing
	ct, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	// Give the allocator time to discover targets
	time.Sleep(500 * time.Millisecond)

	targets, err := chromedp.Targets(ct)
	if err != nil {
		return nil, fmt.Errorf("listing targets: %w", err)
	}

	var tabs []Tab
	for _, t := range targets {
		// Only page tabs, not DevTools, extensions, etc.
		if !isPageTarget(t.Type) {
			continue
		}
		url := t.URL
		if url == "about:blank" || url == "chrome://newtab/" {
			continue
		}
		tabs = append(tabs, Tab{
			Title: t.Title,
			URL:   url,
		})
	}

	return tabs, nil
}

func isPageTarget(targetType string) bool {
	return targetType == "page"
}

type Tab struct {
	Title string
	URL   string
}
