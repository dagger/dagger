package main

import (
	"context"
	"fmt"
	"os"

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
	var orgID string
	switch len(user.Orgs) {
	case 0:
		fmt.Fprintf(os.Stderr, "You are not a member of any organizations.\n")
		os.Exit(1)
	case 1:
		orgID = user.Orgs[0].ID
	default:
		if orgName == "" {
			fmt.Fprintf(os.Stderr, "You are a member of multiple organizations. Please select one with `dagger login ORG`:\n\n")
			for _, org := range user.Orgs {
				fmt.Fprintf(os.Stderr, "- %s\n", org.Name)
			}
			os.Exit(1)
		}
		for _, org := range user.Orgs {
			if org.Name == orgName {
				orgID = org.ID
				break
			}
		}
		if orgID == "" {
			fmt.Fprintf(os.Stderr, "Organization %s not found\n", orgName)
			os.Exit(1)
		}
	}

	if err := auth.SetOrg(orgID); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Success.\n")
	return nil
}

func (cli *CloudCLI) Logout(cmd *cobra.Command, args []string) error {
	return auth.Logout()
}
