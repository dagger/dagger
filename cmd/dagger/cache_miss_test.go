package main

import (
	"context"
	"testing"
)

func TestGetCacheMissState(t *testing.T) {
	// When there's no state in context, it should return nil
	if s := getCacheMissState(context.Background()); s != nil {
		t.Fatalf("expected nil state, got %v", s)
	}

	// When state is present, it should return the same pointer
	expected := &cacheMissState{}
	ctx := context.WithValue(context.Background(), failOnCacheMissKey, expected)
	got := getCacheMissState(ctx)
	if got != expected {
		t.Fatalf("expected %v, got %v", expected, got)
	}
}

func TestCacheMissStateAtomic(t *testing.T) {
	s := &cacheMissState{}
	if s.failedMiss() {
		t.Fatalf("expected initial failedMiss to be false")
	}
	s.failed.Store(true)
	if !s.failedMiss() {
		t.Fatalf("expected failedMiss to be true after store")
	}
}

func TestNewCacheMissSpanExporterNonNil(t *testing.T) {
	s := &cacheMissState{}
	exp := newCacheMissSpanExporter(s)
	if exp == nil {
		t.Fatalf("expected non-nil exporter")
	}
}

func TestCacheMissErrIncludesChangedInputs(t *testing.T) {
	err := cacheMissErr([]string{"--foo", "--bar"})
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if got, want := err.Error(), "call failed because it was not served from cache; changed inputs: --foo, --bar"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestCacheMissErrNoChangedInputs(t *testing.T) {
	err := cacheMissErr(nil)
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if got := err.Error(); got != "call failed because it was not served from cache; the request inputs changed or the result was invalidated" {
		t.Fatalf("unexpected error message: %q", got)
	}
}
