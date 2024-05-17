package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
)

func init() {
	cloud := &CloudCLI{}

	rootCmd.PersistentFlags().MarkHidden("api")

	group := &cobra.Group{
		ID:    "cloud",
		Title: "Dagger Cloud Commands",
	}
	rootCmd.AddGroup(group)

	loginCmd := &cobra.Command{
		Use:     "login [flags] [ORG]",
		Short:   "Log in to Dagger Cloud",
		GroupID: group.ID,
		RunE:    cloud.Login,
	}
	rootCmd.AddCommand(loginCmd)

	logoutCmd := &cobra.Command{
		Use:     "logout",
		Short:   "Log out from Dagger Cloud",
		GroupID: group.ID,
		RunE:    cloud.Logout,
	}
	rootCmd.AddCommand(logoutCmd)
}

type CloudCLI struct {
}

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

	if err := auth.Login(ctx); err != nil {
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
		fmt.Fprintln(errW, "You are not a member of any organizations.")
		return Fail
	case 1:
		selectedOrg = &user.Orgs[0]
	default:
		if orgName == "" {
			fmt.Fprintf(errW, "You are a member of multiple organizations. Please select one with `dagger login ORG`:\n\n")
			for _, org := range user.Orgs {
				fmt.Fprintf(errW, "- %s\n", org.Name)
			}
			return Fail
		}
		for _, org := range user.Orgs {
			org := org
			if org.Name == orgName {
				selectedOrg = &org
				break
			}
		}
		if selectedOrg == nil {
			fmt.Fprintln(errW, "Organization", orgName, "not found.")
			return Fail
		}
	}

	if err := auth.SetCurrentOrg(selectedOrg); err != nil {
		return err
	}

	fmt.Fprintln(outW, "Success.")

	return nil
}

func (cli *CloudCLI) Logout(cmd *cobra.Command, args []string) error {
	return auth.Logout()
}
