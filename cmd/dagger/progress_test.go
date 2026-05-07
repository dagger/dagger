package main

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/dagger/dagger/dagql/dagui"
)

func TestApplyCommandProgressDefaults(t *testing.T) {
	oldOpts := opts
	oldVerbose := verbose
	oldQuiet := quiet
	t.Cleanup(func() {
		opts = oldOpts
		verbose = oldVerbose
		quiet = oldQuiet
	})

	cmd := &cobra.Command{}

	opts = dagui.FrontendOpts{}
	verbose = 0
	quiet = 0
	applyCommandProgressDefaults(cmd)
	if opts.Verbosity != dagui.HideCompletedVerbosity {
		t.Fatalf("default verbosity = %d, want %d", opts.Verbosity, dagui.HideCompletedVerbosity)
	}

	opts = dagui.FrontendOpts{}
	verbose = 1
	quiet = 0
	applyCommandProgressDefaults(cmd)
	if opts.Verbosity != dagui.ShowCompletedVerbosity {
		t.Fatalf("default quiet + -v verbosity = %d, want %d", opts.Verbosity, dagui.ShowCompletedVerbosity)
	}

	opts = dagui.FrontendOpts{}
	verbose = 0
	quiet = 0
	showCmd := &cobra.Command{
		Annotations: map[string]string{
			showFinalProgressKey: "true",
		},
	}
	applyCommandProgressDefaults(showCmd)
	if opts.Verbosity != dagui.ShowCompletedVerbosity {
		t.Fatalf("show final progress verbosity = %d, want %d", opts.Verbosity, dagui.ShowCompletedVerbosity)
	}

	opts = dagui.FrontendOpts{}
	verbose = 0
	quiet = 1
	applyCommandProgressDefaults(showCmd)
	if opts.Verbosity != dagui.HideCompletedVerbosity {
		t.Fatalf("show final progress + -q verbosity = %d, want %d", opts.Verbosity, dagui.HideCompletedVerbosity)
	}

	childCmd := &cobra.Command{}
	showCmd.AddCommand(childCmd)
	opts = dagui.FrontendOpts{}
	verbose = 0
	quiet = 0
	applyCommandProgressDefaults(childCmd)
	if opts.Verbosity != dagui.ShowCompletedVerbosity {
		t.Fatalf("inherited show final progress verbosity = %d, want %d", opts.Verbosity, dagui.ShowCompletedVerbosity)
	}
}
