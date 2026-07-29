package lock

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type Lock struct {
	f *os.File
}

// Acquire opens or creates a lock file in dataDir and attempts an exclusive,
// non-blocking flock. If the file is already locked by another process, an
// error is returned. The lock is automatically released when the process
// exits (kernel releases flocks on fd close/process death).
func Acquire(dataDir string) (*Lock, error) {
	path := filepath.Join(dataDir, "wmdl.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("opening lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another wmdl instance is already running (lock held on %s)", path)
	}
	return &Lock{f: f}, nil
}

func (l *Lock) Release() {
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
}
