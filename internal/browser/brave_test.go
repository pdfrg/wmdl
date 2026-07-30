package browser

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeProcNetTCP(t *testing.T, dir, content string) {
	t.Helper()
	netDir := filepath.Join(dir, "net")
	require.NoError(t, os.MkdirAll(netDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(netDir, "tcp"), []byte(content), 0644))
}

func addProcEntry(t *testing.T, dir string, pid int, inode string) {
	t.Helper()
	fdDir := filepath.Join(dir, fmt.Sprintf("%d", pid), "fd")
	require.NoError(t, os.MkdirAll(fdDir, 0755))
	require.NoError(t, os.Symlink("socket:["+inode+"]", filepath.Join(fdDir, "3")))
}

func TestFindPIDByProcNetFS(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		procDir := t.TempDir()
		writeProcNetTCP(t, procDir, `
  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0
   1: 00000000:22B8 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 67890 1 0000000000000000 100 0 0 10 0
`)
		addProcEntry(t, procDir, 1234, "12345")
		addProcEntry(t, procDir, 5678, "67890")

		assert.Equal(t, 1234, findPIDByProcNetFS(procDir, 8080))
		assert.Equal(t, 5678, findPIDByProcNetFS(procDir, 8888))
	})

	t.Run("port_not_in_listen_state", func(t *testing.T) {
		procDir := t.TempDir()
		writeProcNetTCP(t, procDir, `
  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:1F90 00000000:0000 01 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0
`)
		assert.Equal(t, 0, findPIDByProcNetFS(procDir, 8080))
	})

	t.Run("port_not_found", func(t *testing.T) {
		procDir := t.TempDir()
		writeProcNetTCP(t, procDir, `
  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:22B8 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 67890 1 0000000000000000 100 0 0 10 0
`)
		assert.Equal(t, 0, findPIDByProcNetFS(procDir, 8080))
	})

	t.Run("inode_missing", func(t *testing.T) {
		procDir := t.TempDir()
		writeProcNetTCP(t, procDir, `
  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 99999 1 0000000000000000 100 0 0 10 0
`)
		addProcEntry(t, procDir, 1234, "12345")

		assert.Equal(t, 0, findPIDByProcNetFS(procDir, 8080))
	})

	t.Run("no_net_tcp", func(t *testing.T) {
		procDir := t.TempDir()
		assert.Equal(t, 0, findPIDByProcNetFS(procDir, 8080))
	})

	t.Run("empty_proc_dir", func(t *testing.T) {
		procDir := t.TempDir()
		writeProcNetTCP(t, procDir, `
  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0
`)
		assert.Equal(t, 0, findPIDByProcNetFS(procDir, 8080))
	})
}

func TestIsPageTarget(t *testing.T) {
	assert.True(t, isPageTarget("page"))
	assert.False(t, isPageTarget("iframe"))
	assert.False(t, isPageTarget("worker"))
	assert.False(t, isPageTarget("background"))
	assert.False(t, isPageTarget(""))
}
