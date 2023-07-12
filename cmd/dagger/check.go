package main

import (
	"context"
	"fmt"
	"os"
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
)

var checkCmd = &cobra.Command{
	Use:                "check [suite]",
	DisableFlagParsing: true,
	Aliases:            []string{"test"},
	Long:               `Run your environment's checks.`,
	RunE:               wrapper(RunCheck),
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
			RunE:         wrapper(ListChecks),
		},
		&cobra.Command{
			Use:                "run",
			Long:               `Run your environment's checks.`,
			DisableFlagParsing: true,
			RunE:               wrapper(RunCheck),
		},
	)

}

func loadEnv(ctx context.Context, c *dagger.Client) (*dagger.Environment, error) {
	env, err := getEnvironmentFlagConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get environment config: %w", err)
	}
	if env.local != nil && outputPath == "" {
		// default to outputting to the environment root dir
		rootDir, err := env.local.rootDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get environment root dir: %w", err)
		}
		outputPath = rootDir
	}

	loadedEnv, err := env.load(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to load environment: %w", err)
	}

	return loadedEnv, nil
}

func wrapper(
	fn func(context.Context, *client.Client, *dagger.Client, *dagger.Environment, *cobra.Command, []string) error,
) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		flags := pflag.NewFlagSet(cmd.Name(), pflag.ContinueOnError)
		flags.SetInterspersed(false) // stop parsing at first possible dynamic subcommand
		flags.AddFlagSet(cmd.Flags())
		flags.AddFlagSet(cmd.PersistentFlags())
		err := flags.Parse(args)
		if err != nil {
			return fmt.Errorf("failed to parse top-level flags: %w", err)
		}
		dynamicCmdArgs := flags.Args()

		focus = doFocus
		return withEngineAndTUI(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			rec := progrock.RecorderFromContext(ctx)
			vtx := rec.Vertex("cmd-loader", strings.Join(os.Args, " "))
			defer func() { vtx.Done(err) }()

			connect := vtx.Task("connecting to Dagger")
			c, err := dagger.Connect(ctx, dagger.WithConn(EngineConn(engineClient)))
			connect.Done(err)
			if err != nil {
				return fmt.Errorf("connect to dagger: %w", err)
			}

			load := vtx.Task("loading environment")
			loadedEnv, err := loadEnv(ctx, c)
			load.Done(err)
			if err != nil {
				return err
			}

			return fn(ctx, engineClient, c, loadedEnv, cmd, dynamicCmdArgs)
		})
	}
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

	for _, check := range envChecks {
		name, err := check.Name(ctx)
		if err != nil {
			return fmt.Errorf("failed to get check name: %w", err)
		}
		descr, err := check.Description(ctx)
		if err != nil {
			return fmt.Errorf("failed to get check description: %w", err)
		}
		fmt.Fprintf(tw, "%s\t%s\n", name, descr)
	}

	return tw.Flush()
}

func RunCheck(ctx context.Context, _ *client.Client, c *dagger.Client, loadedEnv *dagger.Environment, cmd *cobra.Command, dynamicCmdArgs []string) (err error) {
	envChecks, err := loadedEnv.Checks(ctx)
	if err != nil {
		return fmt.Errorf("failed to get environment commands: %w", err)
	}

	for _, envCheck := range envChecks {
		envCheck := envCheck
		subChecks, err := addCheck(ctx, nil, &envCheck, c)
		if err != nil {
			return fmt.Errorf("failed to add cmd: %w", err)
		}
		cmd.AddCommand(subChecks...)
	}

	subCmd, _, err := cmd.Find(dynamicCmdArgs)
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
		cmd.Println(subCmd.UsageString())
		return fmt.Errorf("entrypoint not found or not set")
	}

	err = subCmd.Execute()
	if err != nil {
		return fmt.Errorf("failed to execute subcmd: %w", err)
	}

	return nil
}

func addCheck(ctx context.Context, cmdStack []*cobra.Command, envCheck *dagger.EnvironmentCheck, c *dagger.Client) ([]*cobra.Command, error) {
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

	// TODO:
	isLeafCheck := true

	var parentCmd *cobra.Command
	if len(cmdStack) > 0 {
		parentCmd = cmdStack[len(cmdStack)-1]
	}
	cmdName := getCommandName(parentCmd, envCheckName)

	// make a copy of cmdStack
	cmdStack = append([]*cobra.Command{}, cmdStack...)
	subcmd := &cobra.Command{
		Use:         cmdName,
		Short:       description,
		Annotations: map[string]string{},
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			if !isLeafCheck {
				// just print the usage
				return pflag.ErrHelp
			}

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

			// TODO: awkward api
			success, err := envCheck.Result().Success(ctx)
			if err != nil {
				return fmt.Errorf("failed to get check result: %w", err)
			}

			if success {
				cmd.Println(termenv.String("PASS").Foreground(termenv.ANSIGreen))
			} else {
				cmd.Println(termenv.String("FAIL").Foreground(termenv.ANSIRed))
			}
			return nil
		},
	}
	cmdStack = append(cmdStack, subcmd)

	if parentCmd != nil {
		subcmd.Flags().AddFlagSet(parentCmd.Flags())
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
