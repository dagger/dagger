package main

import (
	"os"

	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/spf13/cobra"
)

var cloudCmd = &cobra.Command{
	Use:   "cloud",
	Short: "Dagger Cloud commands",
}

var cloudAPI string

func init() {
	cloud := &CloudCLI{}
	cloudCmd.PersistentFlags().StringVar(&cloud.API, "api", "https://api.dagger.cloud", "Dagger Cloud API URL")
	cloudCmd.PersistentFlags().BoolVar(&cloud.Trace, "trace", false, "Print API request/response headers")

	loginCmd := &cobra.Command{
		Use:          "login",
		Short:        "Authenticate with Dagger Cloud",
		RunE:         cloud.Login,
		SilenceUsage: true,
	}
	cloudCmd.AddCommand(loginCmd)

	orgCmd := &cobra.Command{
		Use:    "org",
		Short:  "Dagger Cloud org management",
		Hidden: true,
	}
	cloudCmd.AddCommand(orgCmd)

	orgCreateCmd := &cobra.Command{
		Use:          "create",
		Short:        "Create a new org",
		RunE:         cloud.CreateOrg,
		Args:         cobra.ExactArgs(1),
		Hidden:       true,
		SilenceUsage: true,
	}
	orgCmd.AddCommand(orgCreateCmd)
}

type CloudCLI struct {
	API   string
	Trace bool
}

func (cli *CloudCLI) Client() (*cloud.Client, error) {
	client, err := cloud.NewClient(cli.API)
	if err != nil {
		return nil, err
	}

	client.Trace = cli.Trace
	return client, nil
}

func (cli *CloudCLI) Login(cmd *cobra.Command, args []string) error {
	lg := Logger(os.Stderr)
	ctx := lg.WithContext(cmd.Context())
	return auth.Login(ctx)
}

func (cli *CloudCLI) CreateOrg(cmd *cobra.Command, args []string) error {
	lg := Logger(os.Stderr)
	ctx := lg.WithContext(cmd.Context())

	client, err := cli.Client()
	if err != nil {
		return err
	}

	name := args[0]

	org, err := client.CreateOrg(ctx, &cloud.CreateOrgRequest{
		Name: name,
	})
	if err != nil {
		return err
	}

	lg.Info().
		Str("name", org.Name).
		Str("id", org.OrgID).
		Msg("created org")

	return nil
}
