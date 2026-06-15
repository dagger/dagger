package main

import (
	"errors"
	"testing"

	"github.com/spf13/pflag"
)

func TestFlagExistsError(t *testing.T) {
	err := &FlagExistsError{Name: "workspace"}
	if err.Error() != "flag already exists: workspace" {
		t.Fatalf("unexpected error message: %s", err.Error())
	}
}

func TestAddFlag_FlagExistsError(t *testing.T) {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.String("workspace", "", "existing flag")

	// Create a dummy arg using the modFunctionArg and its type definition.
	// Since we only care about AddFlag erroring out early on Lookup(name),
	// we just need Name and Description (via FlagName() and Description).
	arg := &modFunctionArg{
		Name:        "workspace",
		Description: "workspace argument",
	}

	err := arg.AddFlag(fs)
	if err == nil {
		t.Fatal("expected error when flag already exists")
	}

	var fe *FlagExistsError
	if !errors.As(err, &fe) {
		t.Fatalf("expected FlagExistsError, got %T: %v", err, err)
	}

	if fe.Name != "workspace" {
		t.Fatalf("expected flag name 'workspace', got %q", fe.Name)
	}
}
