package daggercmd

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

// Listing services and agents must have the same quiet default as listing checks.
func TestArtifactListProgressDefaults(t *testing.T) {
	oldOpts, oldVerbose, oldQuiet := opts, verbose, quiet
	t.Cleanup(func() { opts, verbose, quiet = oldOpts, oldVerbose, oldQuiet })
	for _, cmd := range []*cobra.Command{checksCmd, upCmd, agentCmd} {
		t.Run(cmd.Name(), func(t *testing.T) {
			list := cmd.Flags().Lookup("list")
			oldList := list.Value.String()
			t.Cleanup(func() { _ = list.Value.Set(oldList) })
			for _, tc := range []struct {
				list    string
				verbose int
				want    int
			}{
				{"true", 0, dagui.HideCompletedVerbosity},
				{"true", 1, dagui.ShowCompletedVerbosity},
			} {
				if err := list.Value.Set(tc.list); err != nil {
					t.Fatal(err)
				}
				opts, verbose, quiet = dagui.FrontendOpts{}, tc.verbose, 0
				applyCommandProgressDefaults(cmd)
				if opts.Verbosity != tc.want {
					t.Fatalf("list=%s verbose=%d: verbosity=%d, want %d", tc.list, tc.verbose, opts.Verbosity, tc.want)
				}
			}
		})
	}
}
