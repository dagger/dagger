package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/internal/engine/journal"
	"github.com/dagger/dagger/router"
	"github.com/moby/buildkit/identity"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/vektah/gqlparser/v2"
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
		workdir, configPath, err = engine.NormalizePaths(workdir, configPath)
		if err != nil {
			return fmt.Errorf("failed to normalize paths: %w", err)
		}

		cmd.Println("Loading+installing project (use --debug to track progress)...")
		return withEngine(cmd.Context(), "", journal.Discard{}, os.Stderr, func(ctx context.Context, r *router.Router) error {
			res := struct {
				Host struct {
					Workdir struct {
						LoadProject struct {
							Name   string
							Schema string
						}
					}
				}
			}{}

			configRelPath, err := filepath.Rel(workdir, configPath)
			if err != nil {
				return fmt.Errorf("failed to get config relative path: %w", err)
			}

			_, err = r.Do(ctx,
				`query LoadProject($configPath: String!) {
					host {
						workdir {
							loadProject(configPath: $configPath) {
								name
								schema
								install
							}
						}
					}
				}`,
				"LoadProject",
				map[string]any{
					"configPath": configRelPath,
				},
				&res,
			)
			if err != nil {
				return err
			}

			schemaStr := res.Host.Workdir.LoadProject.Schema
			if schemaStr == "" {
				return fmt.Errorf("invalid empty schema")
			}
			// TODO:
			// fmt.Println(schemaStr)
			schema, err := gqlparser.LoadSchema(&ast.Source{
				Input: schemaStr,
			})
			if err != nil {
				return fmt.Errorf("failed to parse schema: %w", err)
			}

			visited := map[string]struct{}{}
			for _, field := range schema.Query.Fields {
				if field.Name == "__schema" || field.Name == "__type" {
					continue
				}
				if err := addCmd(cmd, field, schema, visited, r); err != nil {
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

func addCmd(parentCmd *cobra.Command, field *ast.FieldDefinition, schema *ast.Schema, visited map[string]struct{}, r *router.Router) error {
	_, ok := visited[field.Name]
	if ok {
		return nil
	}
	visited[field.Name] = struct{}{}
	subcmd := &cobra.Command{
		Use:   field.Name,
		Short: field.Description,
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

			fmt.Println(res)
			return nil
		},
	}
	parentCmd.AddCommand(subcmd)
	for _, arg := range field.Arguments {
		if arg.Type.Name() != "String" {
			return fmt.Errorf("unsupported argument type %s", arg.Type.Name())
		}
		subcmd.Flags().String(arg.Name, "", "")
	}

	switch field.Type.Name() {
	case "String", "Int", "Float", "Boolean", "ID":
		// don't recurse
	default:
		// TODO: support lists maybe?
		obj, ok := schema.Types[field.Type.Name()]
		if !ok {
			return fmt.Errorf("undefined type %s", field.Type.Name())
		}
		for _, objField := range obj.Fields {
			if err := addCmd(subcmd, objField, schema, visited, r); err != nil {
				return fmt.Errorf("failed to add subcmd: %w", err)
			}
		}
	}
	return nil
}
