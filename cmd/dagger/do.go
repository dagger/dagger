package main

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine"
	internalengine "github.com/dagger/dagger/internal/engine"
	"github.com/dagger/dagger/internal/engine/journal"
	"github.com/dagger/dagger/router"
	"github.com/moby/buildkit/identity"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
)

var doCmd = &cobra.Command{
	Use:                "do",
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		flags := pflag.NewFlagSet(cmd.Name(), pflag.ContinueOnError)
		flags.SetInterspersed(false) // stop parsing at first possible dynamic subcommand
		flags.AddFlagSet(cmd.Flags())
		flags.AddFlagSet(cmd.PersistentFlags())
		err := flags.Parse(args)
		if err != nil {
			return fmt.Errorf("failed to parse top-level flags: %w", err)
		}
		dynamicCmdArgs := flags.Args()

		engineConf := engine.Config{
			RunnerHost:    internalengine.RunnerHost(),
			JournalWriter: journal.Discard{},
		}
		if debugLogs {
			engineConf.LogOutput = os.Stderr
		}
		// TODO: dumb kludge, cleanup definnition of workdir/configPath
		workdir, configPath := workdir, configPath
		if v, ok := os.LookupEnv("DAGGER_WORKDIR"); ok && (workdir == "" || workdir == ".") {
			workdir = v
		}
		if configPath == "" {
			configPath = os.Getenv("DAGGER_CONFIG")
		}
		if !strings.HasPrefix(workdir, "git://") {
			engineConf.Workdir = workdir
			engineConf.ConfigPath = configPath
		}

		cmd.Println("Loading+installing project (use --debug to track progress)...")
		return engine.Start(cmd.Context(), engineConf, func(ctx context.Context, r *router.Router) error {
			opts := []dagger.ClientOpt{
				dagger.WithConn(router.EngineConn(r)),
			}
			if debugLogs {
				opts = append(opts, dagger.WithLogOutput(os.Stderr))
			}
			c, err := dagger.Connect(ctx, opts...)
			if err != nil {
				return fmt.Errorf("failed to connect to dagger: %w", err)
			}

			proj, err := getProject(ctx, workdir, configPath, c)
			if err != nil {
				return fmt.Errorf("failed to get project schema: %w", err)
			}
			projCmds, err := proj.Commands(ctx)
			if err != nil {
				return fmt.Errorf("failed to get project commands: %w", err)
			}
			for _, projCmd := range projCmds {
				if err := addCmd(ctx, cmd, projCmd, c, r); err != nil {
					return fmt.Errorf("failed to add cmd: %w", err)
				}
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
			cmd.Printf("Running command %q...\n", subCmd.Name())
			err = subCmd.Execute()
			if err != nil {
				cmd.PrintErrln("Error:", err.Error())
				cmd.Println(subCmd.UsageString())
				return fmt.Errorf("failed to execute subcmd: %w", err)
			}
			return nil
		})
	},
}

func addCmd(ctx context.Context, parentCmd *cobra.Command, projCmd dagger.ProjectCommand, c *dagger.Client, r *router.Router) error {
	// TODO: this shouldn't be needed, there is a bug in our codegen for lists of objects. It should
	// internally be doing this so it's not needed explicitly
	projCmdID, err := projCmd.ID(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cmd id: %w", err)
	}
	projCmd = *c.ProjectCommand(dagger.ProjectCommandOpts{ID: dagger.ProjectCommandID(projCmdID)})

	name, err := projCmd.Name(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cmd name: %w", err)
	}
	description, err := projCmd.Description(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cmd description: %w", err)
	}
	subcmd := &cobra.Command{
		Use:   name,
		Short: description,
		RunE: func(cmd *cobra.Command, args []string) error {
			var cmds []*cobra.Command
			curCmd := cmd
			for curCmd.Name() != "do" { // TODO: I guess this rules out entrypoints named do, probably fine?
				cmds = append(cmds, curCmd)
				curCmd = curCmd.Parent()
			}

			queryVars := map[string]any{}
			varDefinitions := ast.VariableDefinitionList{}
			topSelection := &ast.Field{}
			curSelection := topSelection
			for i := range cmds {
				cmd := cmds[len(cmds)-1-i]
				curSelection.Name = cmd.Name()
				cmd.NonInheritedFlags().VisitAll(func(flag *pflag.Flag) {
					// skip help flag
					// TODO: doc that users can't name an args help
					if flag.Name == "help" {
						return
					}
					val := flag.Value.String()
					/* TODO: think more about difference between required and not required, set vs. unset
					if val == "" {
						?
					}
					*/
					uniqueVarName := fmt.Sprintf("%s_%s_%s", cmd.Name(), flag.Name, identity.NewID())
					queryVars[uniqueVarName] = val
					curSelection.Arguments = append(curSelection.Arguments, &ast.Argument{
						Name:  flag.Name,
						Value: &ast.Value{Raw: uniqueVarName},
					})
					varDefinitions = append(varDefinitions, &ast.VariableDefinition{
						Variable: uniqueVarName,
						Type:     ast.NonNullNamedType("String", nil),
					})
				})
				if i < len(cmds)-1 {
					newSelection := &ast.Field{}
					curSelection.SelectionSet = ast.SelectionSet{newSelection}
					curSelection = newSelection
				}
			}

			var b bytes.Buffer
			opName := "Do"
			formatter.NewFormatter(&b).FormatQueryDocument(&ast.QueryDocument{
				Operations: ast.OperationList{&ast.OperationDefinition{
					Operation:           ast.Query,
					Name:                opName,
					SelectionSet:        ast.SelectionSet{topSelection},
					VariableDefinitions: varDefinitions,
				}},
			})
			queryBytes := b.Bytes()
			// TODO:
			// fmt.Println(string(queryBytes))

			resMap := map[string]any{}
			_, err := r.Do(cmd.Context(), string(queryBytes), opName, queryVars, &resMap)
			if err != nil {
				return err
			}
			var res string
			resSelection := resMap
			for i := range cmds {
				cmd := cmds[len(cmds)-1-i]
				next := resSelection[cmd.Name()]
				switch next := next.(type) {
				case map[string]any:
					resSelection = next
				case string:
					if i < len(cmds)-1 {
						return fmt.Errorf("expected object, got string")
					}
					res = next
				default:
					return fmt.Errorf("unexpected type %T", next)
				}
			}

			// TODO: better to print this after session closes so there's less overlap with progress output
			fmt.Println(res)
			return nil
		},
	}
	parentCmd.AddCommand(subcmd)

	projFlags, err := projCmd.Flags(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cmd flags: %w", err)
	}
	for _, flag := range projFlags {
		flagName, err := flag.Name(ctx)
		if err != nil {
			return fmt.Errorf("failed to get flag name: %w", err)
		}
		flagDescription, err := flag.Description(ctx)
		if err != nil {
			return fmt.Errorf("failed to get flag description: %w", err)
		}
		subcmd.Flags().String(flagName, "", flagDescription)
	}
	projSubcommands, err := projCmd.Subcommands(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cmd subcommands: %w", err)
	}
	for _, subProjCmd := range projSubcommands {
		err := addCmd(ctx, subcmd, subProjCmd, c, r)
		if err != nil {
			return err
		}
	}
	return nil
}

func getProject(ctx context.Context, workdir, configPath string, c *dagger.Client) (*dagger.Project, error) {
	url, err := url.Parse(workdir)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config path: %w", err)
	}
	switch url.Scheme {
	case "":
		configRelPath, err := filepath.Rel(workdir, configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to get config relative path: %w", err)
		}
		return c.Project().Load(c.Host().Directory(workdir), configRelPath), nil
	case "git":
		// TODO: cleanup, factor project url parsing out into its own thing
		repo := url.Host + url.Path
		ref := url.Fragment
		if ref == "" {
			ref = "main"
		}
		return c.Project().Load(c.Git(repo).Branch(ref).Tree(), configPath), nil
	}
	return nil, fmt.Errorf("unsupported scheme %s", url.Scheme)
}
