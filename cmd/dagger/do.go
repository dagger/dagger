package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/iancoleman/strcase"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/formatter"
	"github.com/vito/progrock"
)

var (
	outputPath string
	doFocus    bool
)

const (
	commandSeparator = ":"
)

func init() {
	doCmd.PersistentFlags().StringVarP(&outputPath, "output", "o", "", "If the command returns a file or directory, it will be written to this path. If --output is not specified, the file or directory will be written to the environment's root directory when using a environment loaded from a local dir.")
	doCmd.PersistentFlags().BoolVar(&doFocus, "focus", true, "Only show output for focused commands.")
}

// environment flags (--environment) for do command are setup in env.go

var doCmd = &cobra.Command{
	Use:                "do",
	DisableFlagParsing: true,
	Hidden:             true, // for now, remove once we're ready for primetime
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

		focus = doFocus
		return withEngineAndTUI(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) (rerr error) {
			rec := progrock.RecorderFromContext(ctx)
			vtx := rec.Vertex("cmd-loader", strings.Join(os.Args, " "))
			if !silent {
				cmd.SetOut(vtx.Stdout())
				cmd.SetErr(vtx.Stderr())
			}
			defer func() { vtx.Done(rerr) }()

			connect := vtx.Task("connecting to Dagger")
			c, err := dagger.Connect(ctx, dagger.WithConn(EngineConn(engineClient)))
			connect.Done(err)
			if err != nil {
				return fmt.Errorf("failed to connect to dagger: %w", err)
			}
			defer c.Close()

			load := vtx.Task("loading environment")
			loadedEnv, err := loadEnv(ctx, c)
			load.Done(err)
			if err != nil {
				return err
			}

			envCmds, err := loadedEnv.Commands(ctx)
			if err != nil {
				return fmt.Errorf("failed to get environment commands: %w", err)
			}
			helpVtx := rec.Vertex("cmd-help", "help", progrock.Focused())
			defer func() { helpVtx.Done(rerr) }()
			for _, envCmd := range envCmds {
				subCmds, err := addCmd(ctx, nil, loadedEnv, envCmd, c, engineClient, helpVtx)
				if err != nil {
					return fmt.Errorf("failed to add cmd: %w", err)
				}
				cmd.AddCommand(subCmds...)
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
			fmt.Fprintf(vtx.Stderr(), "Running command %q...\n", subCmd.Name())
			err = subCmd.Execute()
			if err != nil {
				cmd.PrintErrln("Error:", err.Error())
				return fmt.Errorf("failed to execute subcmd: %w", err)
			}
			return nil
		})
	},
}

// nolint:gocyclo
func addCmd(ctx context.Context, cmdStack []*cobra.Command, env *dagger.Environment, envCmd dagger.EnvironmentCommand, c *dagger.Client, r *client.Client, helpVtx *progrock.VertexRecorder) ([]*cobra.Command, error) {
	// TODO: this shouldn't be needed, there is a bug in our codegen for lists of objects. It should
	// internally be doing this so it's not needed explicitly
	envCmdID, err := envCmd.ID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cmd id: %w", err)
	}
	envCmd = *c.EnvironmentCommand(dagger.EnvironmentCommandOpts{ID: envCmdID})

	envCmdName, err := envCmd.Name(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cmd name: %w", err)
	}
	description, err := envCmd.Description(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cmd description: %w", err)
	}

	envResultType, err := envCmd.ResultType(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cmd result type: %w", err)
	}

	envFlags, err := envCmd.Flags(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cmd flags: %w", err)
	}

	envName, err := env.Name(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get env name: %w", err)
	}

	// TODO:
	/*
		envSubcommands, err := envCmd.Subcommands(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get cmd subcommands: %w", err)
		}
	*/
	var envSubcommands []dagger.EnvironmentCommand
	isLeafCmd := len(envSubcommands) == 0

	var parentCmd *cobra.Command
	if len(cmdStack) > 0 {
		parentCmd = cmdStack[len(cmdStack)-1]
	}
	cmdName := getCommandName(parentCmd, envCmdName)

	// make a copy of cmdStack
	cmdStack = append([]*cobra.Command{}, cmdStack...)
	subcmd := &cobra.Command{
		Use:         cmdName,
		Short:       description,
		Annotations: map[string]string{},
		RunE: func(cmd *cobra.Command, args []string) error {
			if !isLeafCmd {
				// just print the usage
				return pflag.ErrHelp
			}

			queryVars := map[string]any{}
			varDefinitions := ast.VariableDefinitionList{}
			topSelection := &ast.Field{}
			curSelection := topSelection

			// select the object for the environment
			curSelection.Name = envName
			newSelection := &ast.Field{}
			curSelection.SelectionSet = ast.SelectionSet{newSelection}
			curSelection = newSelection

			for i, cmd := range cmdStack {
				cmdName := getSubcommandName(cmd)
				curSelection.Name = strcase.ToLowerCamel(cmdName)
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
					queryVars[flagName] = flagVal
					curSelection.Arguments = append(curSelection.Arguments, &ast.Argument{
						Name:  flagName,
						Value: &ast.Value{Raw: flagName},
					})
					varDefinitions = append(varDefinitions, &ast.VariableDefinition{
						Variable: flagName,
						Type:     ast.NonNullNamedType("String", nil),
					})
				}
				if i < len(cmdStack)-1 {
					newSelection := &ast.Field{}
					curSelection.SelectionSet = ast.SelectionSet{newSelection}
					curSelection = newSelection
				} else {
					if outputPath == "" && returnTypeCanUseOutputFlag(envResultType) {
						return fmt.Errorf("output path not set, --output must be explicitly provided for git:// environments that return files or directories")
					}
					outputPath, err = filepath.Abs(outputPath)
					if err != nil {
						return fmt.Errorf("failed to get absolute path of output path: %w", err)
					}
					switch envResultType {
					case "File":
						curSelection.SelectionSet = ast.SelectionSet{&ast.Field{
							Name: "export",
							Arguments: ast.ArgumentList{
								&ast.Argument{
									Name: "path",
									Value: &ast.Value{
										Raw:  outputPath,
										Kind: ast.StringValue,
									},
								},
								&ast.Argument{
									Name: "allowParentDirPath",
									Value: &ast.Value{
										Raw:  "true",
										Kind: ast.BooleanValue,
									},
								},
							},
						}}
					case "Directory":
						outputStat, err := os.Stat(outputPath)
						switch {
						case os.IsNotExist(err):
						case err == nil:
							if !outputStat.IsDir() {
								return fmt.Errorf("output path %q is not a directory but the command returns a directory", outputPath)
							}
						default:
							return fmt.Errorf("failed to stat output directory: %w", err)
						}
						curSelection.SelectionSet = ast.SelectionSet{&ast.Field{
							Name: "export",
							Arguments: ast.ArgumentList{&ast.Argument{
								Name: "path",
								Value: &ast.Value{
									Raw:  outputPath,
									Kind: ast.StringValue,
								},
							}},
						}}
					}
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

			resMap := map[string]any{}
			err := r.Do(cmd.Context(), string(queryBytes), opName, queryVars, &resMap)
			if err != nil {
				return err
			}
			var res string
			resSelection := resMap
			// select the env field name under query first
			resSelection, ok := resSelection[envName].(map[string]any)
			if !ok {
				return fmt.Errorf("expected object, got %T", resSelection)
			}
			for i, cmd := range cmdStack {
				next := resSelection[strcase.ToLowerCamel(getSubcommandName(cmd))]
				switch next := next.(type) {
				case map[string]any:
					resSelection = next
				case string:
					if i < len(cmdStack)-1 {
						return fmt.Errorf("expected object, got string")
					}
					res = next
				default:
					return fmt.Errorf("unexpected type %T", next)
				}
			}
			// TODO: better to print this after session closes so there's less overlap with progress output
			cmd.Println(res)
			return nil
		},
	}
	cmdStack = append(cmdStack, subcmd)

	if parentCmd != nil {
		subcmd.Flags().AddFlagSet(parentCmd.Flags())
	}
	for _, flag := range envFlags {
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
	for _, subEnvCmd := range envSubcommands {
		subCmds, err := addCmd(ctx, cmdStack, env, subEnvCmd, c, r, helpVtx)
		if err != nil {
			return nil, err
		}
		returnCmds = append(returnCmds, subCmds...)
	}

	subcmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		fmt.Fprintf(helpVtx.Stderr(), "\nCommand %s - %s\n", cmdName, description)

		if len(returnCmds) > 1 {
			fmt.Fprintf(helpVtx.Stderr(), "\nAvailable Subcommands:\n")
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
				fmt.Fprintf(helpVtx.Stderr(), "  %s%s%s\n", getCommandName(subcmd, ""), spacing[len(getCommandName(subcmd, "")):], subcmd.Short)
			}
		}

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
		if len(flags) > 0 {
			fmt.Fprintf(helpVtx.Stderr(), "\nFlags:\n")
			flagSpacing := strings.Repeat(" ", maxFlagLen+2)
			for _, flag := range flags {
				fmt.Fprintf(helpVtx.Stderr(), "  --%s%s%s\n", flag.Name, flagSpacing[len(flag.Name):], flag.Usage)
			}
		}
	})

	return returnCmds, nil
}

func getCommandName(parentCmd *cobra.Command, newCommandName string) string {
	if parentCmd == nil {
		return strcase.ToKebab(newCommandName)
	}
	// parentCmd name is like "dagger do a:b:c", we just want "a:b:c"
	nameSplit := strings.Split(parentCmd.Name(), " ")
	if newCommandName == "" {
		return nameSplit[len(nameSplit)-1]
	}
	return nameSplit[len(nameSplit)-1] + commandSeparator + strcase.ToKebab(newCommandName)
}

func getSubcommandName(cmd *cobra.Command) string {
	// if command name is "a:b:c", we return just "c" here
	nameSplit := strings.Split(getCommandName(cmd, ""), commandSeparator)
	return nameSplit[len(nameSplit)-1]
}

func returnTypeCanUseOutputFlag(returnType string) bool {
	for _, t := range []string{
		"File",
		"Directory",
	} {
		if returnType == t {
			return true
		}
	}
	return false
}

// certain pieces of metadata about cobra commands are difficult or impossible to set
// other than in the generic annotations, this wraps that map with some helpers
type commandAnnotations map[string]string

const (
	commandSpecificFlagsKey = "flags"
)

// These are the flags defined on the command itself. Tried using cobra's Local and
// NonInheritedFlags but could not get them to work as needed.
func (m commandAnnotations) addCommandSpecificFlag(name string) {
	m[commandSpecificFlagsKey] = strings.Join(append(m.getCommandSpecificFlags(), name), ",")
}

func (m commandAnnotations) getCommandSpecificFlags() []string {
	split := strings.Split(m[commandSpecificFlagsKey], ",")
	if len(split) == 1 && split[0] == "" {
		return nil
	}
	return split
}

func EngineConn(engineClient *client.Client) DirectConn {
	return func(req *http.Request) (*http.Response, error) {
		req.SetBasicAuth(engineClient.SecretToken, "")
		resp := httptest.NewRecorder()
		engineClient.ServeHTTP(resp, req)
		return resp.Result(), nil
	}
}

type DirectConn func(*http.Request) (*http.Response, error)

func (f DirectConn) Do(r *http.Request) (*http.Response, error) {
	return f(r)
}

func (f DirectConn) Host() string {
	return ":mem:"
}

func (f DirectConn) Close() error {
	return nil
}
