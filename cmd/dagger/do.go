package main

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/codegen/generator"
	"github.com/dagger/dagger/codegen/introspection"
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

		engineConf := &engine.Config{
			RunnerHost:    internalengine.RunnerHost(),
			NoExtensions:  true, // we load them ourselves for now
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
			_, err := getProjectSchema(ctx, workdir, configPath, r)
			if err != nil {
				return fmt.Errorf("failed to get project schema: %w", err)
			}
			schema, err := generator.Introspect(ctx, r)
			if err != nil {
				return fmt.Errorf("failed to introspect schema: %w", err)
			}
			typesByName := map[string]*introspection.Type{}
			for _, t := range schema.Types {
				typesByName[t.Name] = t
			}

			// TODO: add some metadata to the introspection to make this more maintainable
			coreQueries := map[string]struct{}{
				"cacheVolume":     {},
				"container":       {},
				"defaultPlatform": {},
				"directory":       {},
				"file":            {},
				"git":             {},
				"host":            {},
				"http":            {},
				"pipeline":        {},
				"project":         {},
				"secret":          {},
				"setSecret":       {},
				"socket":          {},
			}

			for _, field := range schema.Query().Fields {
				if _, ok := coreQueries[field.Name]; ok {
					continue
				}
				if err := addCmd(cmd, field, typesByName, r); err != nil {
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

func addCmd(parentCmd *cobra.Command, field *introspection.Field, typesByName map[string]*introspection.Type, r *router.Router) error {
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

			// TODO: better to print this after session closes so there's less overlap with progress output
			fmt.Println(res)
			return nil
		},
	}
	parentCmd.AddCommand(subcmd)
	for _, arg := range field.Args {
		argType := arg.TypeRef.Name
		if ofType := arg.TypeRef.OfType; ofType != nil {
			argType = ofType.Name
		}
		if argType != "String" {
			return fmt.Errorf("unsupported argument type %q for %q", argType, arg.Name)
		}
		subcmd.Flags().String(arg.Name, "", "")
	}

	fieldType := field.TypeRef.Name
	if ofType := field.TypeRef.OfType; ofType != nil {
		fieldType = ofType.Name
	}

	// TODO: add metadata to introspection to make this more maintainable
	coreTypes := map[string]struct{}{
		"Project":       {},
		"Label":         {},
		"__Schema":      {},
		"__Type":        {},
		"Directory":     {},
		"File":          {},
		"Secret":        {},
		"Socket":        {},
		"CacheVolume":   {},
		"GitRef":        {},
		"__EnumValue":   {},
		"GitRepository": {},
		"Container":     {},
		"Host":          {},
		"HostVariable":  {},
		"__InputValue":  {},
		"Query":         {},
		"Port":          {},
		"EnvVariable":   {},
		"__Field":       {},
		"__Directive":   {},
	}

	fieldKind := field.TypeRef.Kind
	if ofType := field.TypeRef.OfType; ofType != nil {
		fieldKind = ofType.Kind
	}
	if fieldKind == introspection.TypeKindObject {
		if _, ok := coreTypes[fieldType]; !ok {
			obj, ok := typesByName[fieldType]
			if !ok {
				return fmt.Errorf("undefined type %s", fieldType)
			}
			for _, objField := range obj.Fields {
				if err := addCmd(subcmd, objField, typesByName, r); err != nil {
					return fmt.Errorf("failed to add subcmd: %w", err)
				}
			}
		}
	}
	return nil
}

// TODO: update to just install, rename
func getProjectSchema(ctx context.Context, workdir, configPath string, r *router.Router) (string, error) {
	url, err := url.Parse(workdir)
	if err != nil {
		return "", fmt.Errorf("failed to parse config path: %w", err)
	}
	switch url.Scheme {
	case "":
		return getLocalProjectSchema(ctx, workdir, configPath, r)
	case "git":
		return getGitProjectSchema(ctx, url, configPath, r)
	}
	return "", fmt.Errorf("unsupported scheme %s", url.Scheme)
}

func getLocalProjectSchema(ctx context.Context, workdir, configPath string, r *router.Router) (string, error) {
	res := struct {
		Host struct {
			Workdir struct {
				LoadProject struct {
					Schema string
				}
			}
		}
	}{}

	configRelPath, err := filepath.Rel(workdir, configPath)
	if err != nil {
		return "", fmt.Errorf("failed to get config relative path: %w", err)
	}

	_, err = r.Do(ctx,
		`query LoadProject($configPath: String!) {
					host {
						workdir {
							loadProject(configPath: $configPath) {
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
		return "", err
	}

	return res.Host.Workdir.LoadProject.Schema, nil
}

func getGitProjectSchema(ctx context.Context, url *url.URL, configPath string, r *router.Router) (string, error) {
	// TODO: cleanup, factor project url parsing out into its own thing
	if url.Scheme != "git" {
		return "", fmt.Errorf("expected git scheme, got %s", url.Scheme)
	}
	repo := url.Host + url.Path
	ref := url.Fragment
	if ref == "" {
		ref = "main"
	}

	res := struct {
		Git struct {
			Branch struct {
				Tree struct {
					LoadProject struct {
						Name   string
						Schema string
					}
				}
			}
		}
	}{}
	_, err := r.Do(ctx,
		`query LoadProject($repo: String!, $ref: String!, $configPath: String!) {
				git(url: $repo) {
					branch(name: $ref) {
						tree {
							loadProject(configPath: $configPath) {
								name
								schema
								install
							}
						}
					}
				}
			}`,
		"LoadProject",
		map[string]any{
			"repo":       repo,
			"ref":        ref,
			"configPath": configPath,
		},
		&res,
	)
	if err != nil {
		return "", err
	}

	return res.Git.Branch.Tree.LoadProject.Schema, nil
}
