package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/pkg/browser"
	"github.com/spf13/cobra"

	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
)

var cloudGroup = &cobra.Group{
	ID:    "cloud",
	Title: "Dagger Cloud Commands",
}

var cloudCLI = &CloudCLI{}

var loginCmd = &cobra.Command{
	Use:     "login [options] [org]",
	Short:   "Log in to Dagger Cloud",
	GroupID: cloudGroup.ID,
	RunE:    cloudCLI.Login,
}

var logoutCmd = &cobra.Command{
	Use:     "logout",
	Short:   "Log out from Dagger Cloud",
	GroupID: cloudGroup.ID,
	RunE:    cloudCLI.Logout,
}

func init() {
	rootCmd.AddGroup(cloudGroup)
	rootCmd.AddCommand(loginCmd, logoutCmd)
}

type CloudCLI struct{}

func (cli *CloudCLI) Client(ctx context.Context) (*cloud.Client, error) {
	return cloud.NewClient(ctx)
}

func (cli *CloudCLI) Login(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	outW := cmd.OutOrStdout()
	errW := cmd.ErrOrStderr()

	var orgName string
	if len(args) > 0 {
		orgName = args[0]
	}

	if err := auth.Login(ctx, outW); err != nil {
		return err
	}

	client, err := cli.Client(ctx)
	if err != nil {
		return err
	}

	user, err := client.User(ctx)
	if err != nil {
		return err
	}
	var selectedOrg *auth.Org
	switch len(user.Orgs) {
	case 0:
		fmt.Fprintln(errW, "You are not a member of any organizations, creating a new one...")
		selectedOrg, err = createNewOrg(ctx, client, errW)
		if err != nil {
			// logging out user here so terminal is not filled with 403
			// errors the next time `dagger login` is called since the user
			// still doesn't have an org set
			auth.Logout()
			fmt.Fprintf(errW, "Error setting up new organization: %v", err)
			return idtui.Fail
		}
	case 1:
		selectedOrg = &user.Orgs[0]
	default:
		if orgName == "" {
			for _, org := range user.Orgs {
				fmt.Fprintf(errW, "- %s\n", org.Name)
			}
			fmt.Fprintf(errW, "\n\nYou are a member of multiple organizations. Please select one with `dagger login <org>`.\n")
			return idtui.Fail
		}
		for _, org := range user.Orgs {
			if org.Name == orgName {
				selectedOrg = &org
				break
			}
		}
		if selectedOrg == nil {
			fmt.Fprintln(errW, "Organization", orgName, "not found.")
			return idtui.Fail
		}
	}

	if err := auth.SetCurrentOrg(selectedOrg); err != nil {
		return err
	}

	fmt.Fprintln(outW, "Success.")

	return nil
}

func createNewOrg(ctx context.Context, cli *cloud.Client, w io.Writer) (*auth.Org, error) {
	url := "https://dagger.cloud/traces/setup"
	err := browser.OpenURL(url)
	if err != nil {
		fmt.Fprintf(w, "Unable to open browser automatically, please visit %s to create an organization.\n", url)
	}

	timer := time.After(15 * time.Second)
	t := time.NewTicker(1 * time.Second)

	defer t.Stop()

	for {
		select {
		case <-timer:
			return nil, errors.New("timed out waiting to create an organization")
		case <-t.C:
			u, err := cli.User(ctx)
			if err != nil {
				return nil, err
			}
			if len(u.Orgs) == 0 {
				continue
			}

			user, err := cli.User(ctx)
			if err != nil {
				return nil, err
			}
			return &user.Orgs[0], nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (cli *CloudCLI) Logout(cmd *cobra.Command, args []string) error {
	return auth.Logout()
}
