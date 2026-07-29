package daggercmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateChangesetDisposition(t *testing.T) {
	tests := []struct {
		name           string
		list           bool
		apply          bool
		noApply        bool
		runningInAgent bool
		want           changesetDisposition
		wantErr        string
	}{
		{name: "human prompt", want: changesetDispositionPrompt},
		{name: "agent requires choice", runningInAgent: true, want: changesetDispositionPrompt, wantErr: "requires an explicit changeset choice"},
		{name: "agent apply", apply: true, runningInAgent: true, want: changesetDispositionApply},
		{name: "agent no apply", noApply: true, runningInAgent: true, want: changesetDispositionNoApply},
		{name: "human apply", apply: true, want: changesetDispositionApply},
		{name: "human no apply", noApply: true, want: changesetDispositionNoApply},
		{name: "agent list is exempt", list: true, runningInAgent: true, want: changesetDispositionPrompt},
		{name: "conflicting choices", apply: true, noApply: true, want: changesetDispositionPrompt, wantErr: "cannot be used together"},
		{name: "conflicting choices in list mode", list: true, apply: true, noApply: true, want: changesetDispositionPrompt, wantErr: "cannot be used together"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := generateChangesetDisposition(test.list, test.apply, test.noApply, test.runningInAgent)
			require.Equal(t, test.want, got)
			if test.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, test.wantErr)
			}
		})
	}
}

func TestGenerateNoApplyIsLongFormOnly(t *testing.T) {
	flag := generateCmd.Flags().Lookup("no-apply")
	require.NotNil(t, flag)
	require.Empty(t, flag.Shorthand)
}
