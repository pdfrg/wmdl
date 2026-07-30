package lock

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAcquireRelease(t *testing.T) {
	dir := t.TempDir()
	l, err := Acquire(dir)
	require.NoError(t, err)
	require.NotNil(t, l)

	// Lock file should exist
	_, err = os.Stat(filepath.Join(dir, "wmdl.lock"))
	assert.NoError(t, err)

	l.Release()
}

func TestAcquireInvalidDir(t *testing.T) {
	_, err := Acquire("/nonexistent/path")
	assert.Error(t, err)
}

func TestReleaseTwice(t *testing.T) {
	dir := t.TempDir()
	l, err := Acquire(dir)
	require.NoError(t, err)
	l.Release()
	// Second release should not panic
	l.Release()
}

func TestContention(t *testing.T) {
	dir := t.TempDir()

	acquired := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})

	go func() {
		l, err := Acquire(dir)
		require.NoError(t, err)
		close(acquired)
		<-release
		l.Release()
		close(done)
	}()

	<-acquired

	_, err := Acquire(dir)
	assert.ErrorContains(t, err, "already running")

	close(release)
	<-done
}

func TestAcquireAfterRelease(t *testing.T) {
	dir := t.TempDir()

	l, err := Acquire(dir)
	require.NoError(t, err)
	l.Release()

	l2, err := Acquire(dir)
	require.NoError(t, err)
	require.NotNil(t, l2)
	l2.Release()
}
