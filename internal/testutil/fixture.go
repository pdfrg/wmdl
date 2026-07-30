package testutil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func LoadFixture(tb testing.TB, path ...string) []byte {
	tb.Helper()
	p := filepath.Join(path...)
	data, err := os.ReadFile(p)
	if err != nil {
		tb.Fatalf("LoadFixture(%s): %v", p, err)
	}
	return data
}

func LoadFixtureJSON(tb testing.TB, dst any, path ...string) {
	tb.Helper()
	data := LoadFixture(tb, path...)
	if err := json.Unmarshal(data, dst); err != nil {
		tb.Fatalf("LoadFixtureJSON(%s): %v", filepath.Join(path...), err)
	}
}
