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
	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/engine/client"
	"github.com/go-git/go-git/v5"
	"github.com/iancoleman/strcase"
	"github.com/juju/ansiterm/tabwriter"
	"github.com/moby/buildkit/util/gitutil"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/vito/progrock"
)

var (
	moduleGroup = &cobra.Group{
		ID:    "module",
		Title: "Dagger Module Commands (Experimental)",
	}

	moduleURL   string
	moduleFlags = pflag.NewFlagSet("module", pflag.ContinueOnError)

	sdk       string
	licenseID string

	moduleName       string
	moduleSourcePath string

	installName string

	developSDK        string
	developSourcePath string

	force bool
)

const (
	moduleURLDefault = "."

	defaultModuleSourceDirName = "dagger"
)

func init() {
	moduleFlags.StringVarP(&moduleURL, "mod", "m", "", "Path to dagger.json config file for the module or a directory containing that file. Either local path (e.g. \"/path/to/some/dir\") or a github repo (e.g. \"github.com/dagger/dagger/path/to/some/subdir\")")
	moduleFlags.BoolVar(&focus, "focus", true, "Only show output for focused commands")

	listenCmd.PersistentFlags().AddFlagSet(moduleFlags)
	queryCmd.PersistentFlags().AddFlagSet(moduleFlags)
	funcCmds.AddFlagSet(moduleFlags)

	moduleInitCmd.Flags().StringVar(&sdk, "sdk", "", "Optionally initialize module for development in the given SDK")
	moduleInitCmd.Flags().StringVar(&moduleName, "name", "", "Name of the new module (defaults to parent directory name)")
	moduleInitCmd.Flags().StringVar(&moduleSourcePath, "source", "", "Directory to store the module implementation source code in (defaults to \"dagger/ if \"--sdk\" is provided)")
	moduleInitCmd.Flags().StringVar(&licenseID, "license", "", "License identifier to generate - see https://spdx.org/licenses/")

	modulePublishCmd.Flags().BoolVarP(&force, "force", "f", false, "Force publish even if the git repository is not clean")
	modulePublishCmd.Flags().AddFlagSet(moduleFlags)

	moduleInstallCmd.Flags().StringVarP(&installName, "name", "n", "", "Name to use for the dependency in the module. Defaults to the name of the module being installed.")
	moduleInstallCmd.Flags().AddFlagSet(moduleFlags)

	moduleDevelopCmd.Flags().StringVar(&developSDK, "sdk", "", "New SDK for the module")
	moduleDevelopCmd.Flags().StringVar(&developSourcePath, "source", "", "Directory to store the module implementation source code in")
	moduleDevelopCmd.PersistentFlags().AddFlagSet(moduleFlags)

	configCmd.PersistentFlags().AddFlagSet(moduleFlags)
	configCmd.AddCommand(oldInitCmd, oldInstallCmd, oldSyncCmd)
	configCmd.AddGroup(moduleGroup)
}

var oldInitCmd = &cobra.Command{
	Use:                "init",
	Short:              "Initialize a new Dagger module",
	Hidden:             true,
	SilenceUsage:       true,
	DisableFlagParsing: true,
	Args: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf(`"dagger mod init" has been replaced by "dagger init"`)
	},
	Run: func(cmd *cobra.Command, args []string) {
		// do nothing
	},
}

var oldInstallCmd = &cobra.Command{
	Use:                "install",
	Short:              "Add a new dependency to a Dagger module",
	Hidden:             true,
	SilenceUsage:       true,
	DisableFlagParsing: true,
	GroupID:            moduleGroup.ID,
	Annotations: map[string]string{
		"experimental": "true",
	},
	Args: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf(`"dagger mod install" has been replaced by "dagger install"`)
	},
	Run: func(cmd *cobra.Command, args []string) {
		// do nothing
	},
}

var oldSyncCmd = &cobra.Command{
	Use:          "sync",
	Short:        "Setup or update all the resources needed to develop on a module locally",
	Hidden:       true,
	SilenceUsage: true,
	GroupID:      moduleGroup.ID,
	Annotations: map[string]string{
		"experimental": "true",
	},
	DisableFlagParsing: true,
	Args: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf(`"dagger mod sync" has been replaced by "dagger develop"`)
	},
	Run: func(cmd *cobra.Command, args []string) {
		// do nothing
	},
}

var configCmd = &cobra.Command{
	Use:     "config",
	Aliases: []string{"mod"},
	Short:   "Get or set the configuration of a Dagger module",
	Long:    "Get or set the configuration of a Dagger module. By default, print the configuration of the specified module.",
	Example: strings.TrimSpace(`
dagger config -m /path/to/some/dir
dagger config -m github.com/dagger/hello-dagger
`,
	),
	GroupID: moduleGroup.ID,
	Annotations: map[string]string{
		"experimental": "true",
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		return withEngineAndTUI(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			cmd.SetContext(ctx)

			modConf, err := getDefaultModuleConfiguration(ctx, engineClient.Dagger(), true)
			if err != nil {
				return fmt.Errorf("failed to load module: %w", err)
			}
			if !modConf.FullyInitialized() {
				return fmt.Errorf("module must be fully initialized")
			}
			mod := modConf.Source.AsModule()

			name, err := mod.Name(ctx)
			if err != nil {
				return fmt.Errorf("failed to get module name: %w", err)
			}
			sdk, err := mod.SDK(ctx)
			if err != nil {
				return fmt.Errorf("failed to get module SDK: %w", err)
			}

			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', tabwriter.DiscardEmptyColumns)
			fmt.Fprintf(tw, "%s\t%s\n",
				"Name:",
				name,
			)
			fmt.Fprintf(tw, "%s\t%s\n",
				"SDK:",
				sdk,
			)
			fmt.Fprintf(tw, "%s\t%s\n",
				"Root Directory:",
				modConf.LocalContextPath,
			)
			fmt.Fprintf(tw, "%s\t%s\n",
				"Source Directory:",
				modConf.LocalRootSourcePath,
			)

			return tw.Flush()
		})
	},
}

var moduleInitCmd = &cobra.Command{
	Use:   "init [flags] [PATH]",
	Short: "Initialize a new Dagger module",
	Long: `Initialize a new Dagger module in a local directory.
By default, create a new dagger.json configuration in the current working directory. If the positional argument PATH is provided, create the module in that directory instead.

The configuration will default the name of the module to the parent directory name, unless specified with --name.

Any module can be installed to via "dagger install".

A module can only be called once it has been initialized with an SDK though. The "--sdk" flag can be provided to init here, but if it's not the configuration can be updated later via "dagger develop".

The "--source" flag allows controlling the directory in which the actual module source code is stored. By default, it will be stored in a directory named "dagger". 
`,
	Example: "dagger mod init --name=hello --sdk=python --source=some/subdir",
	GroupID: moduleGroup.ID,
	Annotations: map[string]string{
		"experimental": "true",
	},
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()

		return withEngineAndTUI(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()

			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get current working directory: %w", err)
			}
			srcRootPath := cwd
			if len(extraArgs) > 0 {
				srcRootPath = extraArgs[0]
			}
			if filepath.IsAbs(srcRootPath) {
				srcRootPath, err = filepath.Rel(cwd, srcRootPath)
				if err != nil {
					return fmt.Errorf("failed to get relative path: %w", err)
				}
			}

			modConf, err := getModuleConfigurationForSourceRef(ctx, dag, srcRootPath, false)
			if err != nil {
				return fmt.Errorf("failed to get configured module: %w", err)
			}

			if modConf.SourceKind != dagger.LocalSource {
				return fmt.Errorf("module must be local")
			}
			if modConf.ModuleSourceConfigExists {
				return fmt.Errorf("module already exists")
			}

			// default module name to directory of source root
			if moduleName == "" {
				moduleName = filepath.Base(modConf.LocalRootSourcePath)
			}

			// only bother setting source path if there's an sdk at this time
			if sdk != "" {
				if moduleSourcePath == "" {
					moduleSourcePath = filepath.Join(modConf.LocalRootSourcePath, defaultModuleSourceDirName)
				}
				// ensure source path is relative to the source root
				sourceAbsPath, err := filepath.Abs(moduleSourcePath)
				if err != nil {
					return fmt.Errorf("failed to get absolute source path for %s: %w", moduleSourcePath, err)
				}
				moduleSourcePath, err = filepath.Rel(modConf.LocalRootSourcePath, sourceAbsPath)
				if err != nil {
					return fmt.Errorf("failed to get relative source path: %w", err)
				}
			}

			_, err = modConf.Source.
				WithName(moduleName).
				WithSDK(sdk).
				WithSourceSubpath(moduleSourcePath).
				ResolveFromCaller().
				AsModule().
				GeneratedContextDiff().
				Export(ctx, modConf.LocalContextPath)
			if err != nil {
				return fmt.Errorf("failed to generate code: %w", err)
			}

			if err := findOrCreateLicense(ctx, modConf.LocalRootSourcePath); err != nil {
				return err
			}

			return nil
		})
	},
}

var moduleInstallCmd = &cobra.Command{
	Use:     "install [flags] MODULE",
	Aliases: []string{"use"},
	Short:   "Add a new dependency to a Dagger module",
	Long:    "Add a Dagger module as a dependency of a local module.",
	// TODO: use example from a reference module, using a tag instead of commit
	Example: "dagger mod install github.com/shykes/daggerverse/ttlsh@16e40ec244966e55e36a13cb6e1ff8023e1e1473",
	GroupID: moduleGroup.ID,
	Annotations: map[string]string{
		"experimental": "true",
	},
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		return withEngineAndTUI(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()
			modConf, err := getDefaultModuleConfiguration(ctx, dag, false)
			if err != nil {
				return fmt.Errorf("failed to get configured module: %w", err)
			}
			if modConf.SourceKind != dagger.LocalSource {
				return fmt.Errorf("module must be local")
			}
			if !modConf.FullyInitialized() {
				return fmt.Errorf("module must be fully initialized")
			}

			depRefStr := extraArgs[0]
			depSrc := dag.ModuleSource(depRefStr)
			depSrcKind, err := depSrc.Kind(ctx)
			if err != nil {
				return fmt.Errorf("failed to get module ref kind: %w", err)
			}
			if depSrcKind == dagger.LocalSource {
				// need to ensure that local dep paths are relative to the parent root source
				depAbsPath, err := filepath.Abs(depRefStr)
				if err != nil {
					return fmt.Errorf("failed to get dep absolute path for %s: %w", depRefStr, err)
				}
				depRelPath, err := filepath.Rel(modConf.LocalRootSourcePath, depAbsPath)
				if err != nil {
					return fmt.Errorf("failed to get dep relative path: %w", err)
				}

				depSrc = dag.ModuleSource(depRelPath)
			}
			dep := dag.ModuleDependency(depSrc, dagger.ModuleDependencyOpts{
				Name: installName,
			})

			modSrc := modConf.Source.
				WithDependencies([]*dagger.ModuleDependency{dep}).
				ResolveFromCaller()

			_, err = modSrc.
				AsModule().
				GeneratedContextDiff().
				Export(ctx, modConf.LocalContextPath)
			if err != nil {
				return fmt.Errorf("failed to generate code: %w", err)
			}

			depSrc = modSrc.ResolveDependency(depSrc)

			name, err := depSrc.ModuleName(ctx)
			if err != nil {
				return err
			}
			sdk, err := depSrc.AsModule().SDK(ctx)
			if err != nil {
				return err
			}
			depRootSubpath, err := depSrc.SourceRootSubpath(ctx)
			if err != nil {
				return err
			}

			if depSrcKind == dagger.GitSource {
				git := depSrc.AsGitSource()
				gitURL, err := git.CloneURL(ctx)
				if err != nil {
					return err
				}
				gitVersion, err := git.Version(ctx)
				if err != nil {
					return err
				}
				gitCommit, err := git.Commit(ctx)
				if err != nil {
					return err
				}

				analytics.Ctx(ctx).Capture(ctx, "module_install", map[string]string{
					"module_name":   name,
					"install_name":  installName,
					"module_sdk":    sdk,
					"source_kind":   "git",
					"git_symbolic":  filepath.Join(gitURL, depRootSubpath),
					"git_clone_url": gitURL,
					"git_subpath":   depRootSubpath,
					"git_version":   gitVersion,
					"git_commit":    gitCommit,
				})
			} else if depSrcKind == dagger.LocalSource {
				analytics.Ctx(ctx).Capture(ctx, "module_install", map[string]string{
					"module_name":   name,
					"install_name":  installName,
					"module_sdk":    sdk,
					"source_kind":   "local",
					"local_subpath": depRootSubpath,
				})

			}

			return nil
		})
	},
}

var moduleDevelopCmd = &cobra.Command{
	Use:   "develop",
	Short: "Setup or update all the resources needed to develop on a module locally",
	Long: `Setup or update all the resources needed to develop on a module locally.

This command re-regerates the module's generated code based on dependencies
and the current state of the module's source code.

If --sdk is set, the config file and generated code will be updated with those values reflected. It currently can only be used to set the SDK of a module that does not have one already.

--source allows controlling the directory in which the actual module source code is stored. By default, it will be stored in a directory named "dagger".

:::note
If not updating source or SDK, this is only required for IDE auto-completion/LSP purposes.
:::
`,
	GroupID: moduleGroup.ID,
	Annotations: map[string]string{
		"experimental": "true",
	},
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		return withEngineAndTUI(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()
			modConf, err := getDefaultModuleConfiguration(ctx, dag, false)
			if err != nil {
				return fmt.Errorf("failed to get configured module: %w", err)
			}
			if modConf.SourceKind != dagger.LocalSource {
				return fmt.Errorf("module must be local")
			}

			src := modConf.Source
			// use this one to read sdk/source path since they require the host filesystem be loaded.
			// this is kind of inefficient, could update the engine to support these APIs without a full
			// ResolveFromCaller call first
			modConf.Source = modConf.Source.ResolveFromCaller()

			modSDK, err := modConf.Source.AsModule().SDK(ctx)
			if err != nil {
				return fmt.Errorf("failed to get module SDK: %w", err)
			}
			if developSDK != "" {
				if modSDK != "" && modSDK != developSDK {
					return fmt.Errorf("cannot update module SDK that has already been set to %q", modSDK)
				}
				modSDK = developSDK
				src = src.WithSDK(modSDK)
			}

			modSourcePath, err := modConf.Source.SourceSubpath(ctx)
			if err != nil {
				return fmt.Errorf("failed to get module source subpath: %w", err)
			}
			// if SDK is set but source path isn't and the user didn't provide --source, we'll use the default source path
			if modSDK != "" && modSourcePath == "" && developSourcePath == "" {
				developSourcePath = filepath.Join(modConf.LocalRootSourcePath, defaultModuleSourceDirName)
			}
			// if there's no SDK and the user isn't changing the source path, there's nothing to do.
			// error out rather than silently doing nothing.
			if modSDK == "" && developSourcePath == "" {
				return fmt.Errorf("dagger develop on a module without an SDK requires either --sdk or --source")
			}
			if developSourcePath != "" {
				// ensure source path is relative to the source root
				sourceAbsPath, err := filepath.Abs(developSourcePath)
				if err != nil {
					return fmt.Errorf("failed to get absolute source path for %s: %w", developSourcePath, err)
				}
				developSourcePath, err = filepath.Rel(modConf.LocalRootSourcePath, sourceAbsPath)
				if err != nil {
					return fmt.Errorf("failed to get relative source path: %w", err)
				}

				if modSourcePath != "" && modSourcePath != developSourcePath {
					return fmt.Errorf("cannot update module source path that has already been set to %q", modSourcePath)
				}

				modSourcePath = developSourcePath
				src = src.WithSourceSubpath(modSourcePath)
			}

			_, err = src.ResolveFromCaller().AsModule().GeneratedContextDiff().Export(ctx, modConf.LocalContextPath)
			if err != nil {
				return fmt.Errorf("failed to generate code: %w", err)
			}

			return nil
		})
	},
}

const daDaggerverse = "https://daggerverse.dev"

var modulePublishCmd = &cobra.Command{
	Use:   "publish",
	Short: "Publish a Dagger module to the Daggerverse",
	Long: fmt.Sprintf(`Publish a local module to the Daggerverse (%s).

The module needs to be committed to a git repository and have a remote
configured with name "origin". The git repository must be clean (unless
forced), to avoid mistakingly depending on uncommitted files.
`,
		daDaggerverse,
	),
	GroupID: moduleGroup.ID,
	Annotations: map[string]string{
		"experimental": "true",
	},
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		return withEngineAndTUI(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			rec := progrock.FromContext(ctx)

			vtx := rec.Vertex("publish", strings.Join(os.Args, " "), progrock.Focused())
			defer func() { vtx.Done(err) }()
			cmd.SetOut(vtx.Stdout())
			cmd.SetErr(vtx.Stderr())

			dag := engineClient.Dagger()
			modConf, err := getDefaultModuleConfiguration(ctx, dag, true)
			if err != nil {
				return fmt.Errorf("failed to get configured module: %w", err)
			}
			if modConf.SourceKind != dagger.LocalSource {
				return fmt.Errorf("module must be local")
			}
			if !modConf.FullyInitialized() {
				return fmt.Errorf("module must be fully initialized")
			}
			repo, err := git.PlainOpenWithOptions(modConf.LocalRootSourcePath, &git.PlainOpenOptions{
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
			pathFromRoot, err := filepath.Rel(gitRoot, modConf.LocalRootSourcePath)
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
			req, err := http.NewRequest(http.MethodPut, crawlURL, strings.NewReader(data.Encode()))
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

type configuredModule struct {
	Source     *dagger.ModuleSource
	SourceKind dagger.ModuleSourceKind

	LocalContextPath    string
	LocalRootSourcePath string

	// whether the dagger.json in the module source dir exists yet
	ModuleSourceConfigExists bool
}

func (c *configuredModule) FullyInitialized() bool {
	return c.ModuleSourceConfigExists
}

func getDefaultModuleConfiguration(
	ctx context.Context,
	dag *dagger.Client,
	// if true, will resolve local sources from the caller
	// before returning the source. This should be set false
	// if the caller wants to mutate configuration (sdk/dependency/etc.)
	// since those changes require the source be resolved after
	// they are made (due to the fact that they may result in more
	// files needing to be loaded).
	resolveFromCaller bool,
) (*configuredModule, error) {
	srcRefStr := moduleURL
	if srcRefStr == "" {
		// it's unset or default value, use mod if present
		if v, ok := os.LookupEnv("DAGGER_MODULE"); ok {
			srcRefStr = v
		}

		// it's still unset, set to the default
		if srcRefStr == "" {
			srcRefStr = moduleURLDefault
		}
	}

	return getModuleConfigurationForSourceRef(ctx, dag, srcRefStr, resolveFromCaller)
}

func getModuleConfigurationForSourceRef(
	ctx context.Context,
	dag *dagger.Client,
	srcRefStr string,
	resolveFromCaller bool,
) (*configuredModule, error) {
	conf := &configuredModule{}

	conf.Source = dag.ModuleSource(srcRefStr)
	var err error
	conf.SourceKind, err = conf.Source.Kind(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get module ref kind: %w", err)
	}

	if conf.SourceKind == dagger.LocalSource {
		// first check if this is a named module from the default find-up dagger.json
		// TODO: can move this to engine now?
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get current working directory: %w", err)
		}
		defaultConfigDir, foundDefaultConfig, err := findUp(cwd)
		if err != nil {
			return nil, fmt.Errorf("error trying to find config path for %s: %s", cwd, err)
		}
		if foundDefaultConfig {
			configPath := filepath.Join(defaultConfigDir, modules.Filename)
			contents, err := os.ReadFile(configPath)
			if err != nil {
				return nil, fmt.Errorf("failed to read %s: %w", configPath, err)
			}
			var modCfg modules.ModuleConfig
			if err := json.Unmarshal(contents, &modCfg); err != nil {
				return nil, fmt.Errorf("failed to unmarshal %s: %s", configPath, err)
			}
			if namedDep, ok := modCfg.DependencyByName(srcRefStr); ok {
				depPath := filepath.Join(defaultConfigDir, namedDep.Source)
				srcRefStr = depPath
			}
		}

		conf.LocalRootSourcePath, err = filepath.Abs(srcRefStr)
		if err != nil {
			return nil, fmt.Errorf("failed to get absolute path for %s: %w", srcRefStr, err)
		}

		if filepath.IsAbs(srcRefStr) {
			srcRefStr, err = filepath.Rel(cwd, srcRefStr)
			if err != nil {
				return nil, fmt.Errorf("failed to get relative path for %s: %w", srcRefStr, err)
			}
		}
		if err := os.MkdirAll(srcRefStr, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory for %s: %w", srcRefStr, err)
		}
		conf.Source = dag.ModuleSource(srcRefStr)

		conf.LocalContextPath, err = conf.Source.ResolveContextPathFromCaller(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get local root path: %w", err)
		}
		_, err = os.Lstat(filepath.Join(conf.LocalRootSourcePath, modules.Filename))
		conf.ModuleSourceConfigExists = err == nil

		if resolveFromCaller {
			conf.Source = conf.Source.ResolveFromCaller()
		}
	} else {
		conf.ModuleSourceConfigExists, err = conf.Source.ConfigExists(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to check if module config exists: %w", err)
		}
	}

	return conf, nil
}

func findUp(curDirPath string) (string, bool, error) {
	if !filepath.IsAbs(curDirPath) {
		return "", false, fmt.Errorf("path is not absolute: %s", curDirPath)
	}

	configPath := filepath.Join(curDirPath, modules.Filename)
	stat, err := os.Lstat(configPath)
	switch {
	case os.IsNotExist(err):

	case err == nil:
		// make sure it's a file
		if !stat.Mode().IsRegular() {
			return "", false, fmt.Errorf("expected %s to be a file", configPath)
		}
		return curDirPath, true, nil

	default:
		return "", false, fmt.Errorf("failed to lstat %s: %s", configPath, err)
	}

	// didn't exist, try parent unless we've hit "/" or a git repo checkout root
	if curDirPath == "/" {
		return curDirPath, false, nil
	}

	_, err = os.Lstat(filepath.Join(curDirPath, ".git"))
	if err == nil {
		return curDirPath, false, nil
	}

	parentDirPath := filepath.Dir(curDirPath)
	return findUp(parentDirPath)
}

// Wraps a command with optional module loading. If a module was explicitly specified by the user,
// it will try to load it and error out if it's not found or invalid. If no module was specified,
// it will try the current directory as a module but provide a nil module if it's not found, not
// erroring out.
func optionalModCmdWrapper(
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

			modConf, err := getDefaultModuleConfiguration(ctx, engineClient.Dagger(), true)
			if err != nil {
				return fmt.Errorf("failed to get configured module: %w", err)
			}
			var loadedMod *dagger.Module
			if modConf.FullyInitialized() {
				loadedMod = modConf.Source.AsModule().Initialize()
				load := vtx.Task("loading module")
				_, err := loadedMod.Serve(ctx)
				load.Done(err)
				if err != nil {
					return fmt.Errorf("failed to serve module: %w", err)
				}
			}

			return fn(ctx, engineClient, loadedMod, cmd, cmdArgs)
		})
	}
}

// loadModTypeDefs loads the objects defined by the given module in an easier to use data structure.
func loadModTypeDefs(ctx context.Context, dag *dagger.Client, mod *dagger.Module) (*moduleDef, error) {
	var res struct {
		TypeDefs []*modTypeDef
	}

	const query = `
fragment TypeDefRefParts on TypeDef {
	kind
	optional
	asObject {
			name
	}
	asInterface {
			name
	}
	asInput {
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
					asInput {
							name
					}
			}
	}
}

fragment FunctionParts on Function {
	name
	description
	returnType {
		...TypeDefRefParts
	}
	args {
		name
		description
		defaultValue
		typeDef {
			...TypeDefRefParts
		}
	}
}

fragment FieldParts on FieldTypeDef {
	name
	description
	typeDef {
		...TypeDefRefParts
	}
}

query TypeDefs($module: ModuleID!) {
	typeDefs: currentTypeDefs {
		kind
		optional
		asObject {
			name
			sourceModuleName
			constructor {
				...FunctionParts
			}
			functions {
				...FunctionParts
			}
			fields {
				...FieldParts
			}
		}
		asInterface {
			name
			sourceModuleName
			functions {
				...FunctionParts
			}
		}
		asInput {
			name
			fields {
				...FieldParts
			}
		}
	}
}
`

	err := dag.Do(ctx, &dagger.Request{
		Query: query,
		Variables: map[string]interface{}{
			"module": mod,
		},
	}, &dagger.Response{
		Data: &res,
	})
	if err != nil {
		return nil, fmt.Errorf("query module objects: %w", err)
	}

	name, err := mod.Name(ctx)
	if err != nil {
		return nil, fmt.Errorf("get module name: %w", err)
	}

	modDef := &moduleDef{Name: name}
	for _, typeDef := range res.TypeDefs {
		switch typeDef.Kind {
		case dagger.ObjectKind:
			modDef.Objects = append(modDef.Objects, typeDef)
		case dagger.InterfaceKind:
			modDef.Interfaces = append(modDef.Interfaces, typeDef)
		case dagger.InputKind:
			modDef.Inputs = append(modDef.Inputs, typeDef)
		}
	}
	return modDef, nil
}

// moduleDef is a representation of dagger.Module.
type moduleDef struct {
	Name       string
	Objects    []*modTypeDef
	Interfaces []*modTypeDef
	Inputs     []*modTypeDef
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

func (m *moduleDef) AsInputs() []*modInput {
	var defs []*modInput
	for _, typeDef := range m.Inputs {
		if typeDef.AsInput != nil {
			defs = append(defs, typeDef.AsInput)
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

// GetInterface retrieves a saved object or interface type definition from the module as a functionProvider.
func (m *moduleDef) GetFunctionProvider(name string) functionProvider {
	if obj := m.GetObject(name); obj != nil {
		return obj
	}
	if iface := m.GetInterface(name); iface != nil {
		return iface
	}
	return nil
}

// GetInput retrieves a saved interface type definition from the module.
func (m *moduleDef) GetInput(name string) *modInput {
	for _, input := range m.AsInputs() {
		// Normalize name in case an SDK uses a different convention for input names.
		if gqlObjectName(input.Name) == gqlObjectName(name) {
			return input
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
	if typeDef.AsInput != nil && typeDef.AsInput.Fields == nil {
		input := m.GetInput(typeDef.AsInput.Name)
		if input != nil {
			typeDef.AsInput = input
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
	AsInput     *modInput
	AsList      *modList
}

type functionProvider interface {
	ProviderName() string
	GetFunctions() []*modFunction
	GetFunction(name string) (*modFunction, error)
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

func (o *modObject) GetFunction(name string) (*modFunction, error) {
	for _, fn := range o.Functions {
		if fn.Name == name || cliName(fn.Name) == name {
			return fn, nil
		}
	}
	for _, f := range o.Fields {
		if f.Name == name || cliName(f.Name) == name {
			return &modFunction{
				Name:        f.Name,
				Description: f.Description,
				ReturnType:  f.TypeDef,
			}, nil
		}
	}
	return nil, fmt.Errorf("no function '%s' in object type '%s'", name, o.Name)
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

func (o *modInterface) GetFunction(name string) (*modFunction, error) {
	for _, fn := range o.Functions {
		if fn.Name == name || cliName(fn.Name) == name {
			return fn, nil
		}
	}
	return nil, fmt.Errorf("no function '%s' in interface type '%s'", name, o.Name)
}

type modInput struct {
	Name   string
	Fields []*modField
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
