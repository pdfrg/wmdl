package hook

import (
	"context"
	"testing"
	"time"
)

func TestRunEmpty(t *testing.T) {
	if err := Run(context.Background(), ""); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRunSuccess(t *testing.T) {
	if err := Run(context.Background(), "true"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRunFailure(t *testing.T) {
	err := Run(context.Background(), "false")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestRunTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := Run(ctx, "sleep 60")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}
