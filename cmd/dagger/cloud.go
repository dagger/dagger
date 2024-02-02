package main

import (
	"context"
	"os"

	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/spf13/cobra"
)

func init() {
	cloud := &CloudCLI{}

	rootCmd.PersistentFlags().StringVar(&cloud.API, "api", "https://api.dagger.cloud", "Dagger Cloud API URL")
	rootCmd.PersistentFlags().MarkHidden("api")

	group := &cobra.Group{
		ID:    "cloud",
		Title: "Dagger Cloud Commands",
	}
	rootCmd.AddGroup(group)

	loginCmd := &cobra.Command{
		Use:     "login",
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
	API string
}

func (cli *CloudCLI) Client(ctx context.Context) (*cloud.Client, error) {
	return cloud.NewClient(ctx, cli.API)
}

func (cli *CloudCLI) Login(cmd *cobra.Command, args []string) error {
	lg := Logger(os.Stderr)
	ctx := lg.WithContext(cmd.Context())

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

	lg.Info().Str("user", user.ID).Msg("logged in")
	return nil
}

func (cli *CloudCLI) Logout(cmd *cobra.Command, args []string) error {
	lg := Logger(os.Stderr)

	if err := auth.Logout(); err != nil {
		return err
	}

	lg.Info().Msg("logged out")
	return nil
}
