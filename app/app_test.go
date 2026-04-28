package app

import (
	"context"
	"strings"
	"testing"

	"github.com/lucinate-ai/lucinate/internal/client"
	"github.com/lucinate-ai/lucinate/internal/config"
)

func TestNew_RequiresClient(t *testing.T) {
	_, err := New(RunOptions{})
	if err == nil {
		t.Fatal("expected error when Client is nil")
	}
	if !strings.Contains(err.Error(), "either Client/Backend or Store is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRun_RequiresClient(t *testing.T) {
	err := Run(context.Background(), RunOptions{})
	if err == nil {
		t.Fatal("expected error when Client is nil")
	}
	if !strings.Contains(err.Error(), "either Client/Backend or Store is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNew_ClientAndStoreMutuallyExclusive(t *testing.T) {
	store := &config.Connections{}
	_, err := New(RunOptions{Client: &client.Client{}, Store: store})
	if err == nil {
		t.Fatal("expected error when both Client and Store are set")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNew_StoreRequiresClientFactory(t *testing.T) {
	store := &config.Connections{}
	_, err := New(RunOptions{Store: store})
	if err == nil {
		t.Fatal("expected error when Store is set without ClientFactory")
	}
	if !strings.Contains(err.Error(), "BackendFactory is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}
