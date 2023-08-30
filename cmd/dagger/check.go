package main

import (
	"context"
	"fmt"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine/client"
	"github.com/iancoleman/strcase"
	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"github.com/vito/progrock"
)

var checkCmd = &cobra.Command{
	Use:                "checks [suite]",
	DisableFlagParsing: true,
	Long:               `Query the status of your environment's checks.`,
	RunE:               loadEnvCmdWrapper(RunCheck),
}

var (
	doFocus bool
)

func init() {
	rootCmd.AddCommand(checkCmd)

	checkCmd.PersistentFlags().BoolVar(&doFocus, "focus", true, "Only show output for focused commands.")

	checkCmd.AddCommand(
		&cobra.Command{
			Use:          "list",
			Long:         `List your environment's checks without updating their status.`,
			SilenceUsage: true,
			RunE:         loadEnvCmdWrapper(ListChecks),
		},
	)

}

func ListChecks(ctx context.Context, _ *client.Client, c *dagger.Client, loadedEnv *dagger.Environment, cmd *cobra.Command, dynamicCmdArgs []string) (err error) {
	rec := progrock.RecorderFromContext(ctx)
	vtx := rec.Vertex("cmd-list-checks", "list checks", progrock.Focused())
	defer func() { vtx.Done(err) }()

	envChecks, err := loadedEnv.Checks(ctx)
	if err != nil {
		return fmt.Errorf("failed to get environment commands: %w", err)
	}

	tw := tabwriter.NewWriter(vtx.Stdout(), 0, 0, 2, ' ', 0)

	// TODO:
	// TODO:
	// TODO:
	envid, err := loadedEnv.ID(ctx)
	if err != nil {
		return fmt.Errorf("failed to get environment id: %w", err)
	}
	fmt.Fprintf(tw, "ENVID: %d\n", len(envid))

	loadedEnv = c.Environment(dagger.EnvironmentOpts{ID: envid})
	envid, err = loadedEnv.ID(ctx)
	if err != nil {
		return fmt.Errorf("failed to get environment: %w", err)
	}
	fmt.Fprintf(tw, "ENVID: %d\n", len(envid))

	loadedEnv = c.Environment(dagger.EnvironmentOpts{ID: envid})
	envid, err = loadedEnv.ID(ctx)
	if err != nil {
		return fmt.Errorf("failed to get environment: %w", err)
	}
	fmt.Fprintf(tw, "ENVID: %d\n", len(envid))

	if stdoutIsTTY {
		fmt.Fprintf(tw, "%s\t%s\n", termenv.String("check name").Bold(), termenv.String("description").Bold())
	}

	var printCheck func(*dagger.Check) error
	printCheck = func(check *dagger.Check) error {

		name, err := check.Name(ctx)
		if err != nil {
			return fmt.Errorf("failed to get check name: %w", err)
		}
		name = strcase.ToKebab(name)

		descr, err := check.Description(ctx)
		if err != nil {
			return fmt.Errorf("failed to get check description: %w", err)
		}
		fmt.Fprintf(tw, "%s\t%s\n", name, descr)
		subChecks, err := check.Subchecks(ctx)
		if err != nil {
			return fmt.Errorf("failed to get check subchecks: %w", err)
		}

		for _, subCheck := range subChecks {
			// TODO: this shouldn't be needed, there is a bug in our codegen for lists of objects. It should
			// internally be doing this so it's not needed explicitly
			subCheckID, err := subCheck.ID(ctx)
			if err != nil {
				return fmt.Errorf("failed to get check id: %w", err)
			}
			subCheck = *c.Check(dagger.CheckOpts{ID: subCheckID})
			err = printCheck(&subCheck)
			if err != nil {
				return err
			}
		}
		return nil
	}

	for _, check := range envChecks {
		// TODO: this shouldn't be needed, there is a bug in our codegen for lists of objects. It should
		// internally be doing this so it's not needed explicitly
		checkID, err := check.ID(ctx)
		if err != nil {
			return fmt.Errorf("failed to get check id: %w", err)
		}

		// TODO:
		// TODO:
		// TODO:
		fmt.Fprintf(tw, "CHECKID: %d\n", len(checkID))

		check = *c.Check(dagger.CheckOpts{ID: checkID})
		err = printCheck(&check)
		if err != nil {
			return err
		}
	}

	return tw.Flush()
}

func RunCheck(ctx context.Context, _ *client.Client, c *dagger.Client, loadedEnv *dagger.Environment, cmd *cobra.Command, dynamicCmdArgs []string) (err error) {
	subCmd, restOfArgs, err := cmd.Find(dynamicCmdArgs)
	if err != nil {
		return fmt.Errorf("failed to find: %w", err)
	}

	// prevent errors below from double printing
	cmd.Root().SilenceErrors = true
	cmd.Root().SilenceUsage = true
	// If there's any overlaps between dagger cmd args and the dynamic cmd args
	// we want to ensure they are parsed separately. For some reason, this flag
	// does that ¯\_(ツ)_/¯
	cmd.Root().TraverseChildren = true

	if subCmd.Name() == cmd.Name() {
		envChecks, err := loadedEnv.Checks(ctx)
		if err != nil {
			return fmt.Errorf("failed to get environment commands: %w", err)
		}

		// default to running all checks
		// TODO: this case also gets triggered if you try to run a check that doesn't exist, fix
		allChecks := c.Check()
		for _, check := range envChecks {
			check := check
			// TODO:
			// TODO:
			// TODO:
			// TODO:
			checkID, err := check.ID(ctx)
			if err != nil {
				return fmt.Errorf("failed to get check id: %w", err)
			}
			ch, err := core.CheckID(checkID).ToCheck()
			if err != nil {
				return fmt.Errorf("failed to convert check id to check: %w", err)
			}
			fmt.Printf("CHECK: %+v\n", ch)

			allChecks = allChecks.WithSubcheck(&check)
		}

		result := allChecks.Result()

		output, err := result.Output(ctx)
		if err != nil {
			return fmt.Errorf("failed to get check result output: %w", err)
		}

		success, err := result.Success(ctx)
		if err != nil {
			return fmt.Errorf("failed to get check result success: %w", err)
		}
		if !success {
			return fmt.Errorf("checks failed: %s", output)
		}
		return nil
	}

	subCmd.SetArgs(restOfArgs)
	err = subCmd.Execute()
	if err != nil {
		return fmt.Errorf("failed to execute subcmd: %w", err)
	}

	return nil
}
