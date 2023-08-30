package main

import (
	"context"
	"fmt"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/iancoleman/strcase"
	"github.com/juju/ansiterm/tabwriter"
	"github.com/moby/buildkit/identity"
	"github.com/muesli/termenv"
	"github.com/opencontainers/go-digest"
	"github.com/spf13/cobra"
	"github.com/vito/progrock"
	"golang.org/x/sync/errgroup"
)

var checkCmd = &cobra.Command{
	Use:  "checks [suite]",
	Long: `Query the status of your environment's checks.`,
	RunE: loadEnvCmdWrapper(RunCheck),
}

func init() {
	rootCmd.AddCommand(checkCmd)

	checkCmd.PersistentFlags().BoolVar(&focus, "focus", true, "Only show output for focused commands.")

	checkCmd.AddCommand(
		&cobra.Command{
			Use:          "list",
			Long:         `List your environment's checks without updating their status.`,
			SilenceUsage: true,
			RunE:         loadEnvCmdWrapper(ListChecks),
		},
	)

}

func ListChecks(ctx context.Context, engineClient *client.Client, env *dagger.Environment, cmd *cobra.Command, dynamicCmdArgs []string) (err error) {
	rec := progrock.RecorderFromContext(ctx)
	vtx := rec.Vertex("cmd-list-checks", "list checks", progrock.Focused())
	defer func() { vtx.Done(err) }()

	envChecks, err := env.Checks(ctx)
	if err != nil {
		return fmt.Errorf("failed to get environment commands: %w", err)
	}

	tw := tabwriter.NewWriter(vtx.Stdout(), 0, 0, 2, ' ', 0)

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
		return nil
	}

	for _, check := range envChecks {
		// TODO: this shouldn't be needed, there is a bug in our codegen for lists of objects. It should
		// internally be doing this so it's not needed explicitly
		checkID, err := check.ID(ctx)
		if err != nil {
			return fmt.Errorf("failed to get check id: %w", err)
		}

		check = *engineClient.Dagger().Check(dagger.CheckOpts{ID: checkID})
		err = printCheck(&check)
		if err != nil {
			return err
		}
	}

	return tw.Flush()
}

func RunCheck(ctx context.Context, engineClient *client.Client, env *dagger.Environment, cmd *cobra.Command, dynamicCmdArgs []string) (err error) {
	c := engineClient.Dagger()
	rec := progrock.RecorderFromContext(ctx)

	// TODO(vito): this is pretty confusing, but we need to initialize a root
	// group for subsequent groups + vertices
	rec = rec.WithGroup(progrock.RootGroup)
	ctx = progrock.RecorderToContext(ctx, rec)

	var selectedChecks []*dagger.Check
	if len(dynamicCmdArgs) > 0 {
		for _, checkName := range dynamicCmdArgs {
			// we accept check names in both kebab-case and lowerCamelCase
			checkName = strcase.ToLowerCamel(checkName)
			selectedChecks = append(selectedChecks, env.Check(checkName))
		}
	} else {
		envChecks, err := env.Checks(ctx)
		if err != nil {
			return fmt.Errorf("failed to get environment commands: %w", err)
		}
		// TODO: workaround bug in codegen
		for _, check := range envChecks {
			check := check
			checkID, err := check.ID(ctx)
			if err != nil {
				return fmt.Errorf("failed to get check id: %w", err)
			}
			selectedChecks = append(selectedChecks, c.Check(dagger.CheckOpts{ID: checkID}))
		}
	}

	var eg errgroup.Group
	for _, check := range selectedChecks {
		check := check
		if err := runCheckHierarchy(ctx, c, rec, &eg, check); err != nil {
			return err
		}
	}
	return eg.Wait()
}

func runCheckHierarchy(
	ctx context.Context,
	c *dagger.Client,
	rec *progrock.Recorder,
	eg *errgroup.Group,
	check *dagger.Check,
) error {
	name, err := check.Name(ctx)
	if err != nil {
		return fmt.Errorf("failed to get check name: %w", err)
	}
	description, err := check.Description(ctx)
	if err != nil {
		return fmt.Errorf("failed to get check description: %w", err)
	}

	if name == "" {
		name = description
	}
	if name == "" {
		name = identity.NewID()
	}
	name = strcase.ToKebab(name)

	checkID, err := check.ID(ctx)
	if err != nil {
		return fmt.Errorf("failed to get check id: %w", err)
	}
	digest := digest.FromString(string(checkID))

	eg.Go(func() (rerr error) {
		subChecks, err := check.Subchecks(ctx)
		if err != nil {
			return fmt.Errorf("failed to get check subchecks: %w", err)
		}

		if len(subChecks) > 0 {
			rec = rec.WithGroup(name, progrock.WithGroupID(digest.String()))
			ctx = progrock.RecorderToContext(ctx, rec)
		}

		if len(subChecks) == 0 {
			vtx := rec.Vertex(digest+":result", name, progrock.Focused())
			var success bool
			var output string
			defer func() {
				if rerr != nil {
					fmt.Fprintln(vtx.Stderr(), rerr.Error())
					vtx.Done(rerr)
				} else if !success {
					rerr = fmt.Errorf("check failed")
				}
			}()

			result := check.Result()
			success, err = result.Success(ctx)
			if err != nil {
				return fmt.Errorf("failed to get check result success: %w", err)
			}
			output, err = result.Output(ctx)
			if err != nil {
				return fmt.Errorf("failed to get check result output: %w", err)
			}

			fmt.Fprint(vtx.Stdout(), output)
			if success {
				vtx.Complete()
			} else {
				vtx.Done(fmt.Errorf("failed"))
			}
			return nil
		}

		for _, subCheck := range subChecks {
			subCheck := subCheck
			// TODO: workaround bug in codegen
			subCheckID, err := subCheck.ID(ctx)
			if err != nil {
				return fmt.Errorf("failed to get check id: %w", err)
			}
			subCheck = *c.Check(dagger.CheckOpts{ID: subCheckID})
			err = runCheckHierarchy(ctx, c, rec, eg, &subCheck)
			if err != nil {
				return err
			}
		}
		return nil
	})

	return nil
}
