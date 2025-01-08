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

	"github.com/go-git/go-git/v5"
	"github.com/moby/buildkit/util/gitutil"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"dagger.io/dagger"
	"github.com/dagger/dagger/analytics"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
)

var (
	moduleGroup = &cobra.Group{
		ID:    "module",
		Title: "Dagger Module Commands",
	}

	moduleURL   string
	moduleFlags = pflag.NewFlagSet("module", pflag.ContinueOnError)

	sdk           string
	licenseID     string
	compatVersion string

	moduleName       string
	moduleSourcePath string

	installName string

	developSDK        string
	developSourcePath string

	force bool

	mergeDeps bool
)

const (
	moduleURLDefault = "."
)

// if the source root path already has some files
// then use `srcRootPath/.dagger` for source
func inferSourcePathDir(srcRootPath string) (string, error) {
	list, err := os.ReadDir(srcRootPath)
	if err != nil {
		return "", err
	}

	for _, l := range list {
		if l.Name() == "dagger.json" {
			continue
		}

		// .dagger already exist, return that
		if l.Name() == ".dagger" {
			return ".dagger", nil
		}

		// ignore hidden files
		if strings.HasPrefix(l.Name(), ".") {
			continue
		}

		return ".dagger", nil
	}

	return ".", nil
}

func init() {
	moduleFlags.StringVarP(&moduleURL, "mod", "m", "", "Path to the module directory. Either local path or a remote git repo")

	for _, fc := range funcCmds {
		if !fc.DisableModuleLoad {
			fc.Command().PersistentFlags().AddFlagSet(moduleFlags)
		}
	}

	funcListCmd.PersistentFlags().AddFlagSet(moduleFlags)
	listenCmd.PersistentFlags().AddFlagSet(moduleFlags)
	queryCmd.PersistentFlags().AddFlagSet(moduleFlags)
	shellCmd.PersistentFlags().AddFlagSet(moduleFlags)
	configCmd.PersistentFlags().AddFlagSet(moduleFlags)

	moduleInitCmd.Flags().StringVar(&sdk, "sdk", "", "Optionally install a Dagger SDK")
	moduleInitCmd.Flags().StringVar(&moduleName, "name", "", "Name of the new module (defaults to parent directory name)")
	moduleInitCmd.Flags().StringVar(&moduleSourcePath, "source", "", "Source directory used by the installed SDK. Defaults to module root")
	moduleInitCmd.Flags().StringVar(&licenseID, "license", defaultLicense, "License identifier to generate. See https://spdx.org/licenses/")
	moduleInitCmd.Flags().BoolVar(&mergeDeps, "merge", false, "Merge module dependencies with existing project ones")
	moduleInitCmd.Flags().MarkHidden("merge")

	modulePublishCmd.Flags().BoolVarP(&force, "force", "f", false, "Force publish even if the git repository is not clean")
	modFlag := *moduleFlags.Lookup("mod")
	modFlag.Usage = modFlag.Usage[:strings.Index(modFlag.Usage, " Either local path")-1]
	modulePublishCmd.Flags().AddFlag(&modFlag)

	moduleInstallCmd.Flags().StringVarP(&installName, "name", "n", "", "Name to use for the dependency in the module. Defaults to the name of the module being installed.")
	moduleInstallCmd.Flags().AddFlagSet(moduleFlags)

	moduleDevelopCmd.Flags().StringVar(&developSDK, "sdk", "", "Install the given Dagger SDK. Can be builtin (go, python, typescript) or a module address")
	moduleDevelopCmd.Flags().StringVar(&developSourcePath, "source", "", "Source directory used by the installed SDK. Defaults to module root")
	moduleDevelopCmd.Flags().StringVar(&licenseID, "license", defaultLicense, "License identifier to generate. See https://spdx.org/licenses/")
	moduleDevelopCmd.Flags().StringVar(&compatVersion, "compat", modules.EngineVersionLatest, "Engine API version to target")
	moduleDevelopCmd.Flags().Lookup("compat").NoOptDefVal = "skip"
	moduleDevelopCmd.PersistentFlags().AddFlagSet(moduleFlags)
}

var moduleInitCmd = &cobra.Command{
	Use:   "init [options] [path]",
	Short: "Initialize a new module",
	Long: `Initialize a new module at the given path.

This creates a dagger.json file at the specified directory, making it the root of the new module.

If --sdk is specified, the given SDK is installed in the module. You can do this later with "dagger develop".
`,
	Example: "dagger init --sdk=python",
	GroupID: moduleGroup.ID,
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()

		return withEngine(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()

			// default the module source root to the current working directory if it doesn't exist yet
			cwd, err := client.Getwd()
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

			modConf, err := getModuleConfigurationForSourceRef(ctx, dag, srcRootPath, false, false)
			if err != nil {
				return fmt.Errorf("failed to get configured module: %w", err)
			}

			if modConf.SourceKind != dagger.ModuleSourceKindLocalSource {
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
				// if user didn't specified moduleSourcePath explicitly,
				// check if current dir is non-empty and infer the source
				// path accordingly.
				if moduleSourcePath == "" {
					inferredSourcePath, err := inferSourcePathDir(modConf.LocalRootSourcePath)
					if err != nil {
						return err
					}

					moduleSourcePath = filepath.Join(modConf.LocalRootSourcePath, inferredSourcePath)
				}

				if moduleSourcePath != "" {
					// ensure source path is relative to the source root
					sourceAbsPath, err := client.Abs(moduleSourcePath)
					if err != nil {
						return fmt.Errorf("failed to get absolute source path for %s: %w", moduleSourcePath, err)
					}

					moduleSourcePath, err = filepath.Rel(modConf.LocalRootSourcePath, sourceAbsPath)
					if err != nil {
						return fmt.Errorf("failed to get relative source path: %w", err)
					}
				}
			}

			_, err = modConf.Source.
				WithName(moduleName).
				WithSDK(sdk).
				WithInit(dagger.ModuleSourceWithInitOpts{Merge: mergeDeps}).
				WithSourceSubpath(moduleSourcePath).
				ResolveFromCaller().
				AsModule(dagger.ModuleSourceAsModuleOpts{EngineVersion: modules.EngineVersionLatest}).
				GeneratedContextDiff().
				Export(ctx, modConf.LocalContextPath)
			if err != nil {
				return fmt.Errorf("failed to generate code: %w", err)
			}

			if sdk != "" {
				// If we're generating code by setting a SDK, we should also generate a license
				// if it doesn't already exists.
				searchExisting := !cmd.Flags().Lookup("license").Changed
				if err := findOrCreateLicense(ctx, modConf.LocalRootSourcePath, searchExisting); err != nil {
					return err
				}
			}

			fmt.Fprintln(cmd.OutOrStdout(), "Initialized module", moduleName, "in", srcRootPath)

			return nil
		})
	},
}

var moduleInstallCmd = &cobra.Command{
	Use:     "install [options] <module>",
	Aliases: []string{"use"},
	Short:   "Install a dependency",
	Long:    "Install another module as a dependency to the current module. The target module must be local.",
	Example: "dagger install github.com/shykes/daggerverse/hello@v0.3.0",
	GroupID: moduleGroup.ID,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		return withEngine(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()
			modConf, err := getDefaultModuleConfiguration(ctx, dag, true, false)
			if err != nil {
				return fmt.Errorf("failed to get configured module: %w", err)
			}
			if modConf.SourceKind != dagger.ModuleSourceKindLocalSource {
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
			if depSrcKind == dagger.ModuleSourceKindLocalSource {
				// need to ensure that local dep paths are relative to the parent root source
				depAbsPath, err := client.Abs(depRefStr)
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

			if depSrcKind == dagger.ModuleSourceKindGitSource {
				git := depSrc.AsGitSource()
				gitURL, err := git.CloneRef(ctx)
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
			} else if depSrcKind == dagger.ModuleSourceKindLocalSource {
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

var moduleUpdateCmd = &cobra.Command{
	Use:     "update [options] <module>",
	Aliases: []string{"use"},
	Short:   "Update a dependency",
	Long:    "Update a dependency to the latest version (or the version specified). The target module must be local.",
	Example: `"dagger update github.com/shykes/daggerverse/hello@v0.3.0" or "dagger update hello"`,
	GroupID: moduleGroup.ID,
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		return withEngine(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()
			modConf, err := getDefaultModuleConfiguration(ctx, dag, true, false)
			if err != nil {
				return fmt.Errorf("failed to get configured module: %w", err)
			}
			if modConf.SourceKind != dagger.ModuleSourceKindLocalSource {
				return fmt.Errorf("module must be local")
			}
			if !modConf.FullyInitialized() {
				return fmt.Errorf("module must be fully initialized")
			}

			_, err = modConf.
				Source.WithUpdateDependencies(extraArgs).
				ResolveFromCaller().
				AsModule().
				GeneratedContextDiff().
				Export(ctx, modConf.LocalContextPath)
			if err != nil {
				return fmt.Errorf("failed to generate code: %w", err)
			}

			return nil
		})
	},
}

var moduleUnInstallCmd = &cobra.Command{
	Use:     "uninstall [options] <module>",
	Short:   "Uninstall a dependency",
	Long:    "Uninstall module as a dependency from the current module. The target module must be local.",
	Example: "dagger uninstall hello",
	GroupID: moduleGroup.ID,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		return withEngine(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()
			modConf, err := getDefaultModuleConfiguration(ctx, dag, true, false)
			if err != nil {
				return fmt.Errorf("failed to get configured module: %w", err)
			}
			if modConf.SourceKind != dagger.ModuleSourceKindLocalSource {
				return fmt.Errorf("module must be local")
			}

			modSrc := modConf.Source.
				WithoutDependencies([]string{extraArgs[0]}).
				ResolveFromCaller()

			_, err = modSrc.
				AsModule().
				GeneratedContextDiff().
				Export(ctx, modConf.LocalContextPath)
			if err != nil {
				return fmt.Errorf("failed to generate code: %w", err)
			}

			return nil
		})
	},
}

var moduleDevelopCmd = &cobra.Command{
	Use:   "develop [options]",
	Short: "Prepare a local module for development",
	Long: `Ensure that a module's SDK is installed, configured, and all its files re-generated.

It has different uses in different contexts:

- In a module without SDK: install an SDK and start an implementation
- In a fresh checkout of a module repository: make sure IDE auto-complete is up-to-date
- In a module with local dependencies: re-generate bindings for all dependencies
- In a module after upgrading the engine: upgrade the target engine version, and check for breaking changes

This command is idempotent: you can run it at any time, any number of times. It will:

1. Ensure that an SDK is installed
2. Ensure that custom SDK configuration is applied
3. Update the target engine version if needed
4. Ensure that a module implementation exists, and create a starter template if not
5. Generate the latest client bindings for the Dagger API and installed dependencies
`,
	GroupID: moduleGroup.ID,
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		return withEngine(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()
			modConf, err := getDefaultModuleConfiguration(ctx, dag, true, false)
			if err != nil {
				return fmt.Errorf("failed to get configured module: %w", err)
			}
			if modConf.SourceKind != dagger.ModuleSourceKindLocalSource {
				return fmt.Errorf("module must be local")
			}

			src := modConf.Source
			// use this one to read sdk/source path since they require the host filesystem be loaded.
			// this is kind of inefficient, could update the engine to support these APIs without a full
			// ResolveFromCaller call first
			modConf.Source = modConf.Source.ResolveFromCaller()

			engineVersion := compatVersion
			if engineVersion == "skip" {
				engineVersion = ""
			}

			modSDK, err := modConf.Source.AsModule(dagger.ModuleSourceAsModuleOpts{EngineVersion: engineVersion}).SDK(ctx)
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
				inferredSourcePath, err := inferSourcePathDir(modConf.LocalRootSourcePath)
				if err != nil {
					return err
				}

				developSourcePath = filepath.Join(modConf.LocalRootSourcePath, inferredSourcePath)
			}

			// if there's no SDK and the user isn't changing the source path, there's nothing to do.
			// error out rather than silently doing nothing.
			if modSDK == "" && developSourcePath == "" {
				return fmt.Errorf("dagger develop on a module without an SDK requires either --sdk or --source")
			}
			if developSourcePath != "" {
				// ensure source path is relative to the source root
				sourceAbsPath, err := client.Abs(developSourcePath)
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

			_, err = src.ResolveFromCaller().
				AsModule(dagger.ModuleSourceAsModuleOpts{EngineVersion: engineVersion}).
				GeneratedContextDiff().
				Export(ctx, modConf.LocalContextPath)
			if err != nil {
				return fmt.Errorf("failed to generate code: %w", err)
			}

			// If no license has been created yet, and SDK is set, we should create one.
			if developSDK != "" {
				searchExisting := !cmd.Flags().Lookup("license").Changed
				if err := findOrCreateLicense(ctx, modConf.LocalRootSourcePath, searchExisting); err != nil {
					return err
				}
			}

			return nil
		})
	},
}

const daDaggerverse = "https://daggerverse.dev"

var modulePublishCmd = &cobra.Command{
	Use:    "publish [options]",
	Hidden: true, // Hide while we finalize publishing workflow
	Short:  "Publish a Dagger module to the Daggerverse",
	Long: fmt.Sprintf(`Publish a local module to the Daggerverse (%s).

The module needs to be committed to a git repository and have a remote
configured with name "origin". The git repository must be clean (unless
forced), to avoid mistakenly depending on uncommitted files.
`,
		daDaggerverse,
	),
	GroupID: moduleGroup.ID,
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		return withEngine(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			slog := slog.SpanLogger(ctx, InstrumentationLibrary)

			dag := engineClient.Dagger()
			modConf, err := getDefaultModuleConfiguration(ctx, dag, true, true)
			if err != nil {
				return fmt.Errorf("failed to get configured module: %w", err)
			}
			if modConf.SourceKind != dagger.ModuleSourceKindLocalSource {
				return fmt.Errorf("module must be local")
			}
			if !modConf.FullyInitialized() {
				return fmt.Errorf("module must be fully initialized")
			}
			repo, err := git.PlainOpenWithOptions(modConf.LocalRootSourcePath, &git.PlainOpenOptions{
				DetectDotGit:          true,
				EnableDotGitCommonDir: true,
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

			slog.Debug("git commit", "commit", commit.String())

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

func getExplicitModuleSourceRef() (string, bool) {
	if moduleURL != "" {
		return moduleURL, true
	}

	// it's unset or default value, use mod if present
	if v, ok := os.LookupEnv("DAGGER_MODULE"); ok {
		return v, true
	}

	return "", false
}

func getDefaultModuleConfiguration(
	ctx context.Context,
	dag *dagger.Client,
	// if doFindUp is true, then the nearest module in parent dirs (up to the context root)
	// will be used
	doFindUp bool,
	// if resolveFromCaller is true, will resolve local sources from the caller
	// before returning the source. This should be set false
	// if the caller wants to mutate configuration (sdk/dependency/etc.)
	// since those changes require the source be resolved after
	// they are made (due to the fact that they may result in more
	// files needing to be loaded).
	resolveFromCaller bool,
) (*configuredModule, error) {
	srcRefStr, ok := getExplicitModuleSourceRef()
	if !ok {
		srcRefStr = moduleURLDefault
	}

	return getModuleConfigurationForSourceRef(ctx, dag, srcRefStr, doFindUp, resolveFromCaller)
}

func getModuleConfigurationForSourceRef(
	ctx context.Context,
	dag *dagger.Client,
	srcRefStr string,
	doFindUp bool,
	resolveFromCaller bool,
	srcOpts ...dagger.ModuleSourceOpts,
) (*configuredModule, error) {
	conf := &configuredModule{}

	conf.Source = dag.ModuleSource(srcRefStr, srcOpts...)
	var err error
	conf.SourceKind, err = conf.Source.Kind(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get module ref kind: %w", err)
	}

	if conf.SourceKind == dagger.ModuleSourceKindGitSource {
		conf.ModuleSourceConfigExists, err = conf.Source.ConfigExists(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to check if module config exists: %w", err)
		}
		return conf, nil
	}

	if doFindUp {
		// need to check if this is a named module from the *default* dagger.json found-up from the cwd
		defaultFindupConfigDir, defaultFindupExists, err := findUp(moduleURLDefault)
		if err != nil {
			return nil, fmt.Errorf("error trying to find default config path for: %w", err)
		}
		if defaultFindupExists {
			configPath := filepath.Join(defaultFindupConfigDir, modules.Filename)
			contents, err := os.ReadFile(configPath)
			if err != nil {
				return nil, fmt.Errorf("failed to read %s: %w", configPath, err)
			}
			var modCfg modules.ModuleConfig
			if err := json.Unmarshal(contents, &modCfg); err != nil {
				return nil, fmt.Errorf("failed to unmarshal %s: %w", configPath, err)
			}

			namedDep, ok := modCfg.DependencyByName(srcRefStr)
			if ok {
				opts := dagger.ModuleSourceOpts{RefPin: namedDep.Pin}
				depSrc := dag.ModuleSource(namedDep.Source, opts)
				depKind, err := depSrc.Kind(ctx)
				if err != nil {
					return nil, err
				}
				depSrcRef := namedDep.Source
				if depKind == dagger.ModuleSourceKindLocalSource {
					depSrcRef = filepath.Join(defaultFindupConfigDir, namedDep.Source)
				}
				return getModuleConfigurationForSourceRef(ctx, dag, depSrcRef, false, resolveFromCaller, opts)
			}
		}

		findupConfigDir, findupExists, err := findUp(srcRefStr)
		if err != nil {
			return nil, fmt.Errorf("error trying to find config path for %s: %w", srcRefStr, err)
		}
		if !findupExists {
			return nil, fmt.Errorf("no %s found in directory %s or any parents up to git root", modules.Filename, srcRefStr)
		}
		srcRefStr = findupConfigDir
	}

	conf.LocalRootSourcePath, err = client.Abs(srcRefStr)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for %s: %w", srcRefStr, err)
	}
	if filepath.IsAbs(srcRefStr) {
		cwd, err := client.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get current working directory: %w", err)
		}
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

	return conf, nil
}

// FIXME: huge refactor needed to remove this function - it shares a lot of
// similarity with the engine-side callerHostFindUpContext, and it would be a
// big simplification
func findUp(curDirPath string) (string, bool, error) {
	_, err := os.Lstat(curDirPath)
	if err != nil {
		return "", false, fmt.Errorf("failed to lstat %s: %w", curDirPath, err)
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
		return "", false, fmt.Errorf("failed to lstat %s: %w", configPath, err)
	}

	// didn't exist, try parent unless we've hit the root or a git repo checkout root
	curDirAbsPath, err := client.Abs(curDirPath)
	if err != nil {
		return "", false, fmt.Errorf("failed to get absolute path for %s: %w", curDirPath, err)
	}
	if curDirAbsPath[len(curDirAbsPath)-1] == os.PathSeparator {
		// path ends in separator, we're at root
		return "", false, nil
	}

	_, err = os.Lstat(filepath.Join(curDirPath, ".git"))
	if err == nil {
		return "", false, nil
	}

	parentDirPath := filepath.Join(curDirPath, "..")
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
		return withEngine(cmd.Context(), client.Params{
			SecretToken: presetSecretToken,
		}, func(ctx context.Context, engineClient *client.Client) (err error) {
			_, explicitModRefSet := getExplicitModuleSourceRef()
			modConf, err := getDefaultModuleConfiguration(ctx, engineClient.Dagger(), true, true)
			if err != nil {
				if !explicitModRefSet {
					// the user didn't explicitly try to run with a module, so just run in default mode
					return fn(ctx, engineClient, nil, cmd, cmdArgs)
				}
				return fmt.Errorf("failed to get configured module: %w", err)
			}
			var loadedMod *dagger.Module
			if modConf.FullyInitialized() {
				loadedMod = modConf.Source.AsModule().Initialize()
				err := loadedMod.Serve(ctx)
				if err != nil {
					return fmt.Errorf("failed to serve module: %w", err)
				}
			}
			return fn(ctx, engineClient, loadedMod, cmd, cmdArgs)
		})
	}
}
