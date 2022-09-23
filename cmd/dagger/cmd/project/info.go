package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mitchellh/go-homedir"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.dagger.io/dagger/cmd/dagger/logger"
	"go.dagger.io/dagger/pkg"
	"go.dagger.io/dagger/plan"
)

var infoCmd = &cobra.Command{
	Use:   "info",
	Short: "Lists project location on file system",
	Args:  cobra.MaximumNArgs(1),
	PreRun: func(cmd *cobra.Command, args []string) {
		// Fix Viper bug for duplicate flags:
		// https://github.com/spf13/viper/issues/233
		if err := viper.BindPFlags(cmd.Flags()); err != nil {
			panic(err)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		lg := logger.New()

		cueModPath, cueModExists := pkg.GetCueModParent()
		if !cueModExists {
			lg.Fatal().Msg("dagger project not found. Run `dagger project init`")
		}

		fmt.Printf("\nCurrent dagger project in: %s\n", cueModPath)

		// load available dagger plan
		ctx := context.Background()
		plan, err := loadPlan(ctx, viper.GetString("plan"))
		if err != nil {
			fmt.Printf("Failed to load dagger plan\n\n%s", err)
			return
		}
		children := plan.Action().Children
		if len(children) == 0 {
			return
		}
		table := setTable(children)
		table.Render()
	},
}

// Action name starting with plan.HiddenActionNamePrefix are not displayed
func setTable(children []*plan.Action) *tablewriter.Table {
	row := [][]string{}
	for _, ac := range children {
		if !strings.HasPrefix(ac.Name, plan.HiddenActionNamePrefix) {
			row = append(row, []string{ac.Name, ac.Documentation})
		}
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"ACTION", "DESCRIPTION"})
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	for _, v := range row {
		table.Append(v)
	}
	return table
}

func loadPlan(ctx context.Context, planPath string) (*plan.Plan, error) {
	// support only local filesystem paths
	// even though CUE supports loading module and package names
	homedirPlanPathExpanded, err := homedir.Expand(planPath)
	if err != nil {
		return nil, err
	}

	absPlanPath, err := filepath.Abs(homedirPlanPathExpanded)
	if err != nil {
		return nil, err
	}

	_, err = os.Stat(absPlanPath)
	if err != nil {
		return nil, err
	}

	return plan.Load(ctx, plan.Config{
		Args:   []string{planPath},
		With:   []string{},
		DryRun: true,
	})
}

func init() {
	infoCmd.Flags().StringP("plan", "p", ".", "Path to plan (defaults to current directory)")
}
