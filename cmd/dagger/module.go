package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/engine/client"
	"github.com/go-git/go-git/v5"
	"github.com/iancoleman/strcase"
	"github.com/moby/buildkit/util/gitutil"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/vito/progrock"
)

var (
	moduleURL   string
	moduleFlags = pflag.NewFlagSet("module", pflag.ContinueOnError)

	sdk       string
	licenseID string

	moduleName string
	moduleRoot string

	force bool
)

const (
	moduleURLDefault = "."
)

func init() {
	moduleFlags.StringVarP(&moduleURL, "mod", "m", "", "Path to dagger.json config file for the module or a directory containing that file. Either local path (e.g. \"/path/to/some/dir\") or a github repo (e.g. \"github.com/dagger/dagger/path/to/some/subdir\").")
	moduleFlags.BoolVar(&focus, "focus", true, "Only show output for focused commands.")

	moduleCmd.PersistentFlags().AddFlagSet(moduleFlags)
	listenCmd.PersistentFlags().AddFlagSet(moduleFlags)
	queryCmd.PersistentFlags().AddFlagSet(moduleFlags)
	funcCmds.AddFlagSet(moduleFlags)

	moduleInitCmd.PersistentFlags().StringVar(&sdk, "sdk", "", "SDK name or image ref to use for the module")
	moduleInitCmd.MarkPersistentFlagRequired("sdk")
	moduleInitCmd.PersistentFlags().StringVar(&moduleName, "name", "", "Name of the new module")
	moduleInitCmd.MarkPersistentFlagRequired("name")
	moduleInitCmd.PersistentFlags().StringVar(&licenseID, "license", "", "License identifier to generate - see https://spdx.org/licenses/")
	moduleInitCmd.PersistentFlags().StringVarP(&moduleRoot, "root", "", "", "Root directory that should be loaded for the full module context. Defaults to the parent directory containing dagger.json.")

	modulePublishCmd.PersistentFlags().BoolVarP(&force, "force", "f", false, "Force publish even if the git repository is not clean.")

	// also include codegen flags since codegen will run on module init

	moduleCmd.AddCommand(moduleInitCmd)
	moduleCmd.AddCommand(moduleInstallCmd)
	moduleCmd.AddCommand(moduleSyncCmd)
	moduleCmd.AddCommand(modulePublishCmd)
}

var moduleCmd = &cobra.Command{
	Use:     "module",
	Aliases: []string{"mod"},
	Short:   "Manage dagger modules",
	Long:    "Manage dagger modules. By default, print the configuration of the specified module in json format.",
	Hidden:  true, // for now, remove once we're ready for primetime
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		return withEngineAndTUI(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			mod, _, err := getModuleRef(ctx, engineClient.Dagger())
			if err != nil {
				return fmt.Errorf("failed to get module: %w", err)
			}
			var cfg *modules.Config
			switch {
			case mod.Local:
				cfg, err = mod.Config(ctx, nil)
				if err != nil {
					return fmt.Errorf("failed to get local module config: %w", err)
				}
			case mod.Git != nil:
				rec := progrock.FromContext(ctx)
				vtx := rec.Vertex("get-mod-config", strings.Join(os.Args, " "))
				defer func() { vtx.Done(err) }()
				readConfigTask := vtx.Task("reading git module config")
				cfg, err = mod.Config(ctx, engineClient.Dagger())
				readConfigTask.Done(err)
				if err != nil {
					return fmt.Errorf("failed to get git module config: %w", err)
				}
			}
			cfgBytes, err := json.MarshalIndent(cfg, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal module config: %w", err)
			}
			cmd.Println(string(cfgBytes))
			return nil
		})
	},
}

var moduleInitCmd = &cobra.Command{
	Use:    "init",
	Short:  "Initialize a new dagger module in a local directory.",
	Hidden: false,
	RunE: func(cmd *cobra.Command, _ []string) (rerr error) {
		ctx := cmd.Context()

		return withEngineAndTUI(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()
			ref, _, err := getModuleRef(ctx, dag)
			if err != nil {
				return fmt.Errorf("failed to get module: %w", err)
			}
			moduleDir, err := ref.LocalSourcePath()
			if err != nil {
				return fmt.Errorf("module init is only supported for local modules")
			}
			if _, err := ref.Config(ctx, nil); err == nil {
				return fmt.Errorf("module init config path already exists: %s", ref.Path)
			}
			if err := findOrCreateLicense(ctx, moduleDir); err != nil {
				return err
			}
			cfg := modules.NewConfig(moduleName, sdk, moduleRoot)
			return updateModuleConfig(ctx, dag, moduleDir, ref, cfg, cmd)
		})
	},
}

var moduleInstallCmd = &cobra.Command{
	Use:     "install",
	Aliases: []string{"use"},
	Short:   "Add a new dependency to a dagger module",
	Hidden:  false,
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		return withEngineAndTUI(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()
			ref, _, err := getModuleRef(ctx, dag)
			if err != nil {
				return fmt.Errorf("failed to get module: %w", err)
			}
			moduleDir, err := ref.LocalSourcePath()
			if err != nil {
				return fmt.Errorf("module use is only supported for local modules")
			}
			modCfg, err := ref.Config(ctx, dag)
			if err != nil {
				return fmt.Errorf("failed to get module config: %w", err)
			}
			if err := modCfg.Use(ctx, dag, ref, extraArgs...); err != nil {
				return fmt.Errorf("failed to add module dependency: %w", err)
			}
			return updateModuleConfig(ctx, dag, moduleDir, ref, modCfg, cmd)
		})
	},
}

var moduleSyncCmd = &cobra.Command{
	Use:    "sync",
	Short:  "Synchronize a dagger module with the latest version of its extensions",
	Hidden: false,
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		return withEngineAndTUI(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()
			ref, _, err := getModuleRef(ctx, dag)
			if err != nil {
				return fmt.Errorf("failed to get module: %w", err)
			}
			moduleDir, err := ref.LocalSourcePath()
			if err != nil {
				return fmt.Errorf("module sync is only supported for local modules")
			}
			modCfg, err := ref.Config(ctx, dag)
			if err != nil {
				return fmt.Errorf("failed to get module config: %w", err)
			}
			return updateModuleConfig(ctx, dag, moduleDir, ref, modCfg, cmd)
		})
	},
}

const daDaggerverse = "https://daggerverse.dev"

var modulePublishCmd = &cobra.Command{
	Use:    "publish",
	Short:  fmt.Sprintf("Publish your module to The Daggerverse (%s)", daDaggerverse),
	Hidden: false,
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		return withEngineAndTUI(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			rec := progrock.FromContext(ctx)

			vtx := rec.Vertex("publish", strings.Join(os.Args, " "), progrock.Focused())
			defer func() { vtx.Done(err) }()
			cmd.SetOut(vtx.Stdout())
			cmd.SetErr(vtx.Stderr())

			dag := engineClient.Dagger()
			ref, _, err := getModuleRef(ctx, dag)
			if err != nil {
				return fmt.Errorf("failed to get module: %w", err)
			}
			moduleDir, err := ref.LocalSourcePath()
			if err != nil {
				return fmt.Errorf("module publish is only supported for local modules")
			}
			repo, err := git.PlainOpenWithOptions(moduleDir, &git.PlainOpenOptions{
				DetectDotGit: true,
			})
			if err != nil {
				return fmt.Errorf("failed to open git repo: %w", err)
			}
			wt, err := repo.Worktree()
			if err != nil {
				return fmt.Errorf("failed to get git worktree: %w", err)
			}
			st, err := wt.Status()
			if err != nil {
				return fmt.Errorf("failed to get git status: %w", err)
			}
			head, err := repo.Head()
			if err != nil {
				return fmt.Errorf("failed to get git HEAD: %w", err)
			}
			commit := head.Hash()

			rec.Debug("git commit", progrock.Labelf("commit", commit.String()))

			orig, err := repo.Remote("origin")
			if err != nil {
				return fmt.Errorf("failed to get git remote: %w", err)
			}
			refPath, err := originToPath(orig.Config().URLs[0])
			if err != nil {
				return fmt.Errorf("failed to get module path: %w", err)
			}

			// calculate path relative to repo root
			gitRoot := wt.Filesystem.Root()
			absModDir, err := filepath.Abs(moduleDir)
			if err != nil {
				return fmt.Errorf("failed to get absolute module dir: %w", err)
			}
			pathFromRoot, err := filepath.Rel(gitRoot, absModDir)
			if err != nil {
				return fmt.Errorf("failed to get path from git root: %w", err)
			}

			// NB: you might think to ignore changes to files outside of the module,
			// but we should probably play it safe. in a monorepo for example this
			// could mean publishing a broken module because it depends on
			// uncommitted code in a dependent module.
			//
			// TODO: the proper fix here might be to check for dependent code, too.
			// Specifically I should be able to publish a dependency before
			// committing + pushing its dependers. but in the end it doesn't really
			// matter; just commit everything and _then_ publish.
			if !st.IsClean() && !force {
				cmd.Println(st)
				return fmt.Errorf("git repository is not clean; run with --force to ignore")
			}

			refStr := fmt.Sprintf("%s@%s", path.Join(refPath, pathFromRoot), commit)

			crawlURL, err := url.JoinPath(daDaggerverse, "crawl")
			if err != nil {
				return fmt.Errorf("failed to get module URL: %w", err)
			}

			data := url.Values{}
			data.Add("ref", refStr)
			req, err := http.NewRequest(http.MethodPut, crawlURL, strings.NewReader(data.Encode())) // nolint: gosec
			if err != nil {
				return fmt.Errorf("failed to create request: %w", err)
			}

			req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				return fmt.Errorf("failed to get module: %w", err)
			}

			// TODO(vito): inspect response and/or poll, would be nice to surface errors here

			cmd.Println("Publishing", refStr, "to", daDaggerverse+"...")
			cmd.Println()
			cmd.Println("You can check on the crawling status here:")
			cmd.Println()
			cmd.Println("    " + res.Request.URL.String())

			modURL, err := url.JoinPath(daDaggerverse, "mod", refStr)
			if err != nil {
				return fmt.Errorf("failed to get module URL: %w", err)
			}
			cmd.Println()
			cmd.Println("Once the crawl is complete, you can view your module here:")
			cmd.Println()
			cmd.Println("    " + modURL)

			return res.Body.Close()
		})
	},
}

func originToPath(origin string) (string, error) {
	url, err := gitutil.ParseURL(origin)
	if err != nil {
		return "", fmt.Errorf("failed to parse git remote origin URL: %w", err)
	}
	return strings.TrimSuffix(path.Join(url.Host, url.Path), ".git"), nil
}

func updateModuleConfig(
	ctx context.Context,
	dag *dagger.Client,
	moduleDir string,
	modFlag *modules.Ref,
	modCfg *modules.Config,
	cmd *cobra.Command,
) (rerr error) {
	rec := progrock.FromContext(ctx)

	configPath := filepath.Join(moduleDir, modules.Filename)

	cfgBytes, err := json.MarshalIndent(modCfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal module config: %w", err)
	}
	_, parentDirStatErr := os.Stat(moduleDir)
	switch {
	case parentDirStatErr == nil:
		// already exists, nothing to do
	case os.IsNotExist(parentDirStatErr):
		// make the parent dir, but if something goes wrong, clean it up in the defer
		if err := os.MkdirAll(moduleDir, 0o755); err != nil {
			return fmt.Errorf("failed to create module config directory: %w", err)
		}
		defer func() {
			if rerr != nil {
				os.RemoveAll(moduleDir)
			}
		}()
	default:
		return fmt.Errorf("failed to stat parent directory: %w", parentDirStatErr)
	}

	var cfgFileMode os.FileMode = 0o644
	originalContents, configFileReadErr := os.ReadFile(configPath)
	switch {
	case configFileReadErr == nil:
		// attempt to restore the original file if it already existed and something goes wrong
		stat, err := os.Stat(configPath)
		if err != nil {
			return fmt.Errorf("failed to stat module config: %w", err)
		}
		cfgFileMode = stat.Mode()
		defer func() {
			if rerr != nil {
				os.WriteFile(configPath, originalContents, cfgFileMode)
			}
		}()
	case os.IsNotExist(configFileReadErr):
		// remove it if it didn't exist already and something goes wrong
		defer func() {
			if rerr != nil {
				os.Remove(configPath)
			}
		}()
	default:
		return fmt.Errorf("failed to read module config: %w", configFileReadErr)
	}

	// nolint:gosec
	if err := os.WriteFile(configPath, append(cfgBytes, '\n'), cfgFileMode); err != nil {
		return fmt.Errorf("failed to write module config: %w", err)
	}

	mod, err := modFlag.AsModule(ctx, dag)
	if err != nil {
		return fmt.Errorf("failed to load module: %w", err)
	}

	codegen := mod.GeneratedCode()

	if err := automateVCS(ctx, moduleDir, codegen); err != nil {
		return fmt.Errorf("failed to automate vcs: %w", err)
	}

	entries, err := codegen.Code().Entries(ctx)
	if err != nil {
		return fmt.Errorf("failed to get codegen output entries: %w", err)
	}

	rec.Debug("syncing generated files", progrock.Labelf("entries", "%v", entries))

	if _, err := codegen.Code().Export(ctx, moduleDir); err != nil {
		return fmt.Errorf("failed to export codegen output: %w", err)
	}

	return nil
}

func getModuleRef(ctx context.Context, dag *dagger.Client) (*modules.Ref, bool, error) {
	wasSet := false

	moduleURL := moduleURL
	if moduleURL == "" {
		// it's unset or default value, use mod if present
		if v, ok := os.LookupEnv("DAGGER_MODULE"); ok {
			moduleURL = v
			wasSet = true
		}

		// it's still unset, set to the default
		if moduleURL == "" {
			moduleURL = moduleURLDefault
		}
	} else {
		wasSet = true
	}
	cfg, err := modules.ResolveMovingRef(ctx, dag, moduleURL)
	return cfg, wasSet, err
}

func loadModCmdWrapper(
	fn func(context.Context, *client.Client, *dagger.Module, *cobra.Command, []string) error,
	presetSecretToken string,
) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, cmdArgs []string) error {
		return withEngineAndTUI(cmd.Context(), client.Params{
			SecretToken: presetSecretToken,
		}, func(ctx context.Context, engineClient *client.Client) (err error) {
			rec := progrock.FromContext(ctx)
			vtx := rec.Vertex("cmd-loader", strings.Join(os.Args, " "))
			defer func() { vtx.Done(err) }()

			load := vtx.Task("loading module")
			loadedMod, err := loadMod(ctx, engineClient.Dagger())
			load.Done(err)
			if err != nil {
				return err
			}

			return fn(ctx, engineClient, loadedMod, cmd, cmdArgs)
		})
	}
}

func loadMod(ctx context.Context, c *dagger.Client) (*dagger.Module, error) {
	mod, modRequired, err := getModuleRef(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to get module config: %w", err)
	}

	// check that the module exists first
	if _, err := mod.Config(ctx, c); err != nil {
		if !modRequired {
			// only allow failing to load the mod when it was explicitly requested
			// by the user
			return nil, nil
		}
		return nil, fmt.Errorf("failed to load module config: %w", err)
	}

	loadedMod, err := mod.AsModule(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to load module: %w", err)
	}

	_, err = loadedMod.Serve(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get loaded module ID: %w", err)
	}

	return loadedMod, nil
}

// loadModTypeDefs loads the objects defined by the given module in an easier to use data structure.
func loadModTypeDefs(ctx context.Context, dag *dagger.Client, mod *dagger.Module) (*moduleDef, error) {
	var res struct {
		Mod struct {
			Name string
		}
		TypeDefs []*modTypeDef
	}

	err := dag.Do(ctx, &dagger.Request{
		Query: `
            query Objects($module: ModuleID!) {
                mod: loadModuleFromID(id: $module) {
                    name
                }
                typeDefs: currentTypeDefs {
                    kind
                    optional
                    asObject {
                        name
                        sourceModuleName
                        constructor {
                            returnType {
                                kind
                                asObject {
                                    name
                                }
                            }
                            args {
                                name
                                description
                                defaultValue
                                typeDef {
                                    kind
                                    optional
                                    asObject {
                                        name
                                    }
                                    asInterface {
                                        name
                                    }
                                    asList {
                                        elementTypeDef {
                                            kind
                                            asObject {
                                                name
                                            }
                                            asInterface {
                                                name
                                            }
                                        }
                                    }
                                }
                            }
                        }
                        functions {
                            name
                            description
                            returnType {
                                kind
                                asObject {
                                    name
                                }
                                asInterface {
                                    name
                                }
                                asList {
                                    elementTypeDef {
                                        kind
                                        asObject {
                                            name
                                        }
                                        asInterface {
                                            name
                                        }
                                    }
                                }
                            }
                            args {
                                name
                                description
                                defaultValue
                                typeDef {
                                    kind
                                    optional
                                    asObject {
                                        name
                                    }
                                    asInterface {
                                        name
                                    }
                                    asList {
                                        elementTypeDef {
                                            kind
                                            asObject {
                                                name
                                            }
                                            asInterface {
                                                name
                                            }
                                        }
                                    }
                                }
                            }
                        }
                        fields {
                            name
                            description
                            typeDef {
                                kind
                                optional
                                asObject {
                                    name
                                }
                                asInterface {
                                    name
                                }
                                asList {
                                    elementTypeDef {
                                        kind
                                        asObject {
                                            name
                                        }
                                        asInterface {
                                            name
                                        }
                                    }
                                }
                            }
                        }
                    }
                    asInterface {
                        name
                        sourceModuleName
                        functions {
                            name
                            description
                            returnType {
                                kind
                                asObject {
                                    name
                                }
                                asInterface {
                                    name
                                }
                                asList {
                                    elementTypeDef {
                                        kind
                                        asObject {
                                            name
                                        }
                                        asInterface {
                                            name
                                        }
                                    }
                                }
                            }
                            args {
                                name
                                description
                                defaultValue
                                typeDef {
                                    kind
                                    optional
                                    asObject {
                                        name
                                    }
                                    asInterface {
                                        name
                                    }
                                    asList {
                                        elementTypeDef {
                                            kind
                                            asObject {
                                                name
                                            }
                                            asInterface {
                                                name
                                            }
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
            }
        `,
		Variables: map[string]interface{}{
			"module": mod,
		},
	}, &dagger.Response{
		Data: &res,
	})
	if err != nil {
		return nil, fmt.Errorf("query module objects: %w", err)
	}

	modDef := &moduleDef{Name: res.Mod.Name}
	for _, typeDef := range res.TypeDefs {
		switch typeDef.Kind {
		case dagger.Objectkind:
			modDef.Objects = append(modDef.Objects, typeDef)
		case dagger.Interfacekind:
			modDef.Interfaces = append(modDef.Interfaces, typeDef)
		}
	}
	return modDef, nil
}

// moduleDef is a representation of dagger.Module.
type moduleDef struct {
	Name       string
	Objects    []*modTypeDef
	Interfaces []*modTypeDef
}

func (m *moduleDef) AsFunctionProviders() []functionProvider {
	providers := make([]functionProvider, 0, len(m.Objects)+len(m.Interfaces))
	for _, obj := range m.AsObjects() {
		providers = append(providers, obj)
	}
	for _, iface := range m.AsInterfaces() {
		providers = append(providers, iface)
	}
	return providers
}

// AsObjects returns the module's object type definitions.
func (m *moduleDef) AsObjects() []*modObject {
	var defs []*modObject
	for _, typeDef := range m.Objects {
		if typeDef.AsObject != nil {
			defs = append(defs, typeDef.AsObject)
		}
	}
	return defs
}

func (m *moduleDef) AsInterfaces() []*modInterface {
	var defs []*modInterface
	for _, typeDef := range m.Interfaces {
		if typeDef.AsInterface != nil {
			defs = append(defs, typeDef.AsInterface)
		}
	}
	return defs
}

// GetObject retrieves a saved object type definition from the module.
func (m *moduleDef) GetObject(name string) *modObject {
	for _, obj := range m.AsObjects() {
		// Normalize name in case an SDK uses a different convention for object names.
		if gqlObjectName(obj.Name) == gqlObjectName(name) {
			return obj
		}
	}
	return nil
}

// GetInterface retrieves a saved interface type definition from the module.
func (m *moduleDef) GetInterface(name string) *modInterface {
	for _, iface := range m.AsInterfaces() {
		// Normalize name in case an SDK uses a different convention for interface names.
		if gqlObjectName(iface.Name) == gqlObjectName(name) {
			return iface
		}
	}
	return nil
}

func (m *moduleDef) GetMainObject() *modObject {
	return m.GetObject(m.Name)
}

// LoadTypeDef attempts to replace a function's return object type or argument's
// object type with with one from the module's object type definitions, to
// recover missing function definitions in those places when chaining functions.
func (m *moduleDef) LoadTypeDef(typeDef *modTypeDef) {
	if typeDef.AsObject != nil && typeDef.AsObject.Functions == nil && typeDef.AsObject.Fields == nil {
		obj := m.GetObject(typeDef.AsObject.Name)
		if obj != nil {
			typeDef.AsObject = obj
		}
	}
	if typeDef.AsInterface != nil && typeDef.AsInterface.Functions == nil {
		iface := m.GetInterface(typeDef.AsInterface.Name)
		if iface != nil {
			typeDef.AsInterface = iface
		}
	}
	if typeDef.AsList != nil {
		m.LoadTypeDef(typeDef.AsList.ElementTypeDef)
	}
}

// modTypeDef is a representation of dagger.TypeDef.
type modTypeDef struct {
	Kind        dagger.TypeDefKind
	Optional    bool
	AsObject    *modObject
	AsInterface *modInterface
	AsList      *modList
}

type functionProvider interface {
	ProviderName() string
	GetFunctions() []*modFunction
}

func (t *modTypeDef) Name() string {
	if t.AsObject != nil {
		return t.AsObject.Name
	}
	if t.AsInterface != nil {
		return t.AsInterface.Name
	}
	return ""
}

func (t *modTypeDef) AsFunctionProvider() functionProvider {
	if t.AsObject != nil {
		return t.AsObject
	}
	if t.AsInterface != nil {
		return t.AsInterface
	}
	return nil
}

// modObject is a representation of dagger.ObjectTypeDef.
type modObject struct {
	Name             string
	Functions        []*modFunction
	Fields           []*modField
	Constructor      *modFunction
	SourceModuleName string
}

var _ functionProvider = (*modObject)(nil)

func (o *modObject) ProviderName() string {
	return o.Name
}

// GetFunctions returns the object's function definitions as well as the fields,
// which are treated as functions with no arguments.
func (o *modObject) GetFunctions() []*modFunction {
	fns := make([]*modFunction, 0, len(o.Functions)+len(o.Fields))
	for _, f := range o.Fields {
		fns = append(fns, &modFunction{
			Name:        f.Name,
			Description: f.Description,
			ReturnType:  f.TypeDef,
		})
	}
	fns = append(fns, o.Functions...)
	return fns
}

type modInterface struct {
	Name      string
	Functions []*modFunction
}

var _ functionProvider = (*modInterface)(nil)

func (o *modInterface) ProviderName() string {
	return o.Name
}

func (o *modInterface) GetFunctions() []*modFunction {
	fns := make([]*modFunction, 0, len(o.Functions))
	fns = append(fns, o.Functions...)
	return fns
}

// modList is a representation of dagger.ListTypeDef.
type modList struct {
	ElementTypeDef *modTypeDef
}

// modField is a representation of dagger.FieldTypeDef.
type modField struct {
	Name        string
	Description string
	TypeDef     *modTypeDef
}

// modFunction is a representation of dagger.Function.
type modFunction struct {
	Name        string
	Description string
	ReturnType  *modTypeDef
	Args        []*modFunctionArg
}

// modFunctionArg is a representation of dagger.FunctionArg.
type modFunctionArg struct {
	Name         string
	Description  string
	TypeDef      *modTypeDef
	DefaultValue dagger.JSON
	flagName     string
}

// FlagName returns the name of the argument using CLI naming conventions.
func (r *modFunctionArg) FlagName() string {
	if r.flagName == "" {
		r.flagName = cliName(r.Name)
	}
	return r.flagName
}

func getDefaultValue[T any](r *modFunctionArg) (T, error) {
	var val T
	err := json.Unmarshal([]byte(r.DefaultValue), &val)
	return val, err
}

// gqlObjectName converts casing to a GraphQL object  name
func gqlObjectName(name string) string {
	return strcase.ToCamel(name)
}

// gqlFieldName converts casing to a GraphQL object field name
func gqlFieldName(name string) string {
	return strcase.ToLowerCamel(name)
}

// gqlArgName converts casing to a GraphQL field argument name
func gqlArgName(name string) string {
	return strcase.ToLowerCamel(name)
}

// cliName converts casing to the CLI convention (kebab)
func cliName(name string) string {
	return strcase.ToKebab(name)
}
