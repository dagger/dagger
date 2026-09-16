package daggercmd

import (
	"fmt"
	"os"

	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

func newSignupCmd() *cobra.Command {
	cmd := newLoginCmd(false)
	cmd.Use = "signup [options] [org]"
	cmd.Short = "Create or select a Dagger Cloud account and organization"
	cmd.Long = `Create or select a Dagger Cloud account and organization.

Use the same account and organization flow as dagger cloud login.
If human action is required, non-interactive mode returns instructions
without opening a browser or waiting for input.`
	cmd.RunE = cloudCLI.Signup
	return cmd
}

func (cli *CloudCLI) Signup(cmd *cobra.Command, args []string) error {
	// Explicit interactive signup uses the existing login flow, independent
	// of the progress renderer. A configured Cloud token needs no browser.
	if os.Getenv("DAGGER_CLOUD_TOKEN") == "" && isatty.IsTerminal(os.Stdin.Fd()) {
		return cli.Login(cmd, args)
	}

	ctx := cmd.Context()
	cloudAuth, err := auth.GetCloudAuth(ctx)
	if err != nil {
		return err
	}
	if os.Getenv("DAGGER_CLOUD_TOKEN") != "" && loginSwitchAccount {
		return fmt.Errorf("unset DAGGER_CLOUD_TOKEN before using --switch-account")
	}
	if cloudAuth == nil || loginSwitchAccount {
		return fmt.Errorf("human action required: run dagger cloud signup in a terminal to open https://dagger.cloud and select an account")
	}

	client, err := cloud.NewClient(ctx, cloudAuth)
	if err != nil {
		return err
	}
	var orgName string
	if len(args) > 0 {
		orgName = args[0]
	}
	if orgName == "" {
		orgName = cloudOrgFlag
	}
	if os.Getenv("DAGGER_CLOUD_TOKEN") != "" {
		if orgName != "" {
			current, err := auth.CurrentOrgName()
			if err != nil || current != orgName {
				return fmt.Errorf("DAGGER_CLOUD_TOKEN does not select organization %q", orgName)
			}
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Dagger Cloud authentication is configured with DAGGER_CLOUD_TOKEN.")
		return nil
	}

	return finishCloudLogin(cmd, orgName, client, false)
}
