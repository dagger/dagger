package daggercmd

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	cloudapi "github.com/dagger/dagger/internal/cloud"
)

func TestPrintCloudNotice(t *testing.T) {
	t.Run("a notice", func(t *testing.T) {
		var buf bytes.Buffer
		printCloudNotice(&buf, &cloudapi.CLINotice{
			Level: cloudapi.CLINoticeLevelWarning,
			Title: "You are on a free Dagger Cloud trial for acme, ending in 2 days.",
			Body:  `Enter payment details with "dagger cloud billing" to avoid service disruption once your trial ends.`,
		})
		out := buf.String()
		require.Contains(t, out, "You are on a free Dagger Cloud trial for acme, ending in 2 days.")
		require.Contains(t, out, `Enter payment details with "dagger cloud billing" to avoid service disruption once your trial ends.`)
		require.Contains(t, out, "To hide set DAGGER_NO_NAG=1")
	})

	t.Run("a notice without a body", func(t *testing.T) {
		var buf bytes.Buffer
		printCloudNotice(&buf, &cloudapi.CLINotice{Level: cloudapi.CLINoticeLevelInfo, Title: "Hello."})
		require.Contains(t, buf.String(), "Hello.\n")
		require.Contains(t, buf.String(), "To hide set DAGGER_NO_NAG=1")
	})

	t.Run("no notice prints nothing", func(t *testing.T) {
		var buf bytes.Buffer
		printCloudNotice(&buf, nil)
		require.Empty(t, buf.String())
	})
}

func TestSkipNag(t *testing.T) {
	for _, env := range []string{"DAGGER_NO_NAG", "NOTHANKS", "SHUTUP", "GOAWAY", "STOPIT"} {
		t.Setenv(env, "")
	}
	require.False(t, skipNag())
	t.Setenv("DAGGER_NO_NAG", "1")
	require.True(t, skipNag())
}
