package main

import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/iancoleman/strcase"
	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/opencontainers/go-digest"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/vito/progrock"
	"golang.org/x/sync/errgroup"
)

var checkCmd = &cobra.Command{
	Use:                "check [suite]",
	DisableFlagParsing: true,
	Aliases:            []string{"test"},
	Long:               `Run your environment's checks.`,
	RunE:               loadEnvCmdWrapper(RunCheck),
}

func init() {
	rootCmd.AddCommand(checkCmd)

	checkCmd.PersistentFlags().StringVarP(&outputPath, "output", "o", "", "If the command returns a file or directory, it will be written to this path. If --output is not specified, the file or directory will be written to the environment's root directory when using a environment loaded from a local dir.")
	checkCmd.PersistentFlags().BoolVar(&doFocus, "focus", true, "Only show output for focused commands.")

	checkCmd.AddCommand(
		&cobra.Command{
			Use:          "list",
			Long:         `List your environment's checks.`,
			SilenceUsage: true,
			RunE:         loadEnvCmdWrapper(ListChecks),
		},
		&cobra.Command{
			Use:                "run",
			Long:               `Run your environment's checks.`,
			DisableFlagParsing: true,
			RunE:               loadEnvCmdWrapper(RunCheck),
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

	if stdoutIsTTY {
		fmt.Fprintf(tw, "%s\t%s\n", termenv.String("check name").Bold(), termenv.String("description").Bold())
	}

	var printCheck func(*dagger.EnvironmentCheck) error
	printCheck = func(check *dagger.EnvironmentCheck) error {

		name, err := check.Name(ctx)
		if err != nil {
			return fmt.Errorf("failed to get check name: %w", err)
		}

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
			subCheck = *c.EnvironmentCheck(dagger.EnvironmentCheckOpts{ID: subCheckID})
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
		check = *c.EnvironmentCheck(dagger.EnvironmentCheckOpts{ID: checkID})
		err = printCheck(&check)
		if err != nil {
			return err
		}
	}

	return tw.Flush()
}

func RunCheck(ctx context.Context, _ *client.Client, c *dagger.Client, loadedEnv *dagger.Environment, cmd *cobra.Command, dynamicCmdArgs []string) (err error) {
	envChecks, err := loadedEnv.Checks(ctx)
	if err != nil {
		return fmt.Errorf("failed to get environment commands: %w", err)
	}

	allLeafCmds := map[string]*cobra.Command{}
	for _, envCheck := range envChecks {
		envCheck := envCheck
		subChecks, err := addCheck(ctx, &envCheck, c)
		if err != nil {
			return fmt.Errorf("failed to add cmd: %w", err)
		}
		for _, subCheck := range subChecks {
			if subCheck.Annotations["leaf"] == "true" {
				allLeafCmds[subCheck.Name()] = subCheck
			}
		}
	}

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
		// default to running all checks
		// TODO: this case also gets triggered if you try to run a check that doesn't exist, fix
		var eg errgroup.Group
		var i int
		for _, leafCmd := range allLeafCmds {
			leafCmd := leafCmd
			leafCmd.SetArgs(restOfArgs)
			i++
			eg.Go(leafCmd.Execute)
		}
		err := eg.Wait()
		if err != nil {
			return fmt.Errorf("failed to execute subcmd: %w", err)
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

func addCheck(ctx context.Context, envCheck *dagger.EnvironmentCheck, c *dagger.Client) ([]*cobra.Command, error) {
	rec := progrock.RecorderFromContext(ctx)

	// TODO: this shouldn't be needed, there is a bug in our codegen for lists of objects. It should
	// internally be doing this so it's not needed explicitly
	envCheckID, err := envCheck.ID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get check id: %w", err)
	}
	envCheck = c.EnvironmentCheck(dagger.EnvironmentCheckOpts{ID: envCheckID})

	envCheckName, err := envCheck.Name(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get check name: %w", err)
	}
	description, err := envCheck.Description(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get check description: %w", err)
	}

	envCheckFlags, err := envCheck.Flags(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get check flags: %w", err)
	}

	cmdName := getCommandName(nil, envCheckName)
	subcmd := &cobra.Command{
		Use:         cmdName,
		Short:       description,
		Annotations: map[string]string{},
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			vtx := rec.Vertex(
				digest.Digest("check-"+envCheckName),
				"check "+envCheckName,
				progrock.Focused(),
			)
			defer func() { vtx.Done(err) }()

			cmd.SetOut(vtx.Stdout())
			cmd.SetErr(vtx.Stderr())

			for _, flagName := range commandAnnotations(cmd.Annotations).getCommandSpecificFlags() {
				// skip help flag
				// TODO: doc that users can't name an args help
				if flagName == "help" {
					continue
				}
				flagVal, err := cmd.Flags().GetString(strcase.ToKebab(flagName))
				if err != nil {
					return fmt.Errorf("failed to get flag %q: %w", flagName, err)
				}
				envCheck = envCheck.SetStringFlag(flagName, flagVal)
			}

			results, err := envCheck.Result(ctx)
			if err != nil {
				return fmt.Errorf("failed to get check result: %w", err)
			}
			for _, result := range results {
				success, err := result.Success(ctx)
				if err != nil {
					return fmt.Errorf("failed to get check result success: %w", err)
				}
				name, err := result.Name(ctx)
				if err != nil {
					return fmt.Errorf("failed to get check result name: %w", err)
				}
				var subcheckSuffix string
				if len(results) > 1 {
					subcheckSuffix = " " + strcase.ToKebab(name)
				}
				if success {
					cmd.Println(termenv.String("PASS" + subcheckSuffix).Foreground(termenv.ANSIGreen))
				} else {
					cmd.Println(termenv.String("FAIL" + subcheckSuffix).Foreground(termenv.ANSIRed))
				}
			}
			return nil
		},
	}

	for _, flag := range envCheckFlags {
		flagName, err := flag.Name(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get flag name: %w", err)
		}
		flagDescription, err := flag.Description(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get flag description: %w", err)
		}
		subcmd.Flags().String(strcase.ToKebab(flagName), "", flagDescription)
		commandAnnotations(subcmd.Annotations).addCommandSpecificFlag(flagName)
	}
	returnCmds := []*cobra.Command{subcmd}
	subChecks, err := envCheck.Subchecks(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get subchecks: %w", err)
	}
	if len(subChecks) == 0 {
		// TODO: utter kludge
		subcmd.Annotations["leaf"] = "true"
	}
	for _, subCheck := range subChecks {
		subCheck := subCheck
		cmds, err := addCheck(ctx, &subCheck, c)
		if err != nil {
			return nil, fmt.Errorf("failed to add subcheck: %w", err)
		}
		returnCmds = append(returnCmds, cmds...)
	}

	subcmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		cmd.Printf("\nCommand %s - %s\n", cmdName, description)

		cmd.Printf("\nAvailable Subcommands:\n")
		maxNameLen := 0
		for _, subcmd := range returnCmds[1:] {
			nameLen := len(getCommandName(subcmd, ""))
			if nameLen > maxNameLen {
				maxNameLen = nameLen
			}
		}
		// we want to ensure the doc strings line up so they are readable
		spacing := strings.Repeat(" ", maxNameLen+2)
		for _, subcmd := range returnCmds[1:] {
			cmd.Printf("  %s%s%s\n", getCommandName(subcmd, ""), spacing[len(getCommandName(subcmd, "")):], subcmd.Short)
		}

		fmt.Printf("\nFlags:\n")
		maxFlagLen := 0
		var flags []*pflag.Flag
		cmd.NonInheritedFlags().VisitAll(func(flag *pflag.Flag) {
			if flag.Name == "help" {
				return
			}
			flags = append(flags, flag)
			if len(flag.Name) > maxFlagLen {
				maxFlagLen = len(flag.Name)
			}
		})
		flagSpacing := strings.Repeat(" ", maxFlagLen+2)
		for _, flag := range flags {
			cmd.Printf("  --%s%s%s\n", flag.Name, flagSpacing[len(flag.Name):], flag.Usage)
		}
	})

	return returnCmds, nil
}
