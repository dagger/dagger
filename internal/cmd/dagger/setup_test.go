package daggercmd

import (
	"bytes"
	"context"
	"testing"

	cloudauth "github.com/dagger/dagger/internal/cloud/auth"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestSetupStepLogin(t *testing.T) {
	for _, tt := range []struct {
		name        string
		auth        *cloudauth.Cloud
		wantAlready bool
	}{
		{name: "not logged in"},
		{name: "logged in", auth: &cloudauth.Cloud{}, wantAlready: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			var out bytes.Buffer
			cmd := &cobra.Command{}
			cmd.SetContext(ctx)
			cmd.SetOut(&out)

			err := setupStepLogin(cmd, func(context.Context) (*cloudauth.Cloud, error) {
				return tt.auth, nil
			})
			require.NoError(t, err)
			require.Equal(t, tt.wantAlready, bytes.Contains(out.Bytes(), []byte("Already logged in.")))
		})
	}
}
