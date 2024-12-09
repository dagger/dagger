package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/iancoleman/strcase"
	"github.com/moby/buildkit/util/gitutil"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
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

// initializeCore loads the core type definitions only
func initializeCore(ctx context.Context, dag *dagger.Client) (rdef *moduleDef, rerr error) {
	def := &moduleDef{}

	if err := def.loadTypeDefs(ctx, dag); err != nil {
		return nil, err
	}

	return def, nil
}

// initializeDefaultModule loads the module referenced by the -m,--mod flag
//
// By default, looks for a module in the current directory, or above.
// Returns an error if the module is not found or invalid.
func initializeDefaultModule(ctx context.Context, dag *dagger.Client) (*moduleDef, error) {
	modRef, _ := getExplicitModuleSourceRef()
	if modRef == "" {
		modRef = moduleURLDefault
	}
	return initializeModule(ctx, dag, modRef, true)
}

// maybeInitializeDefaultModule optionally loads the module referenced by the -m,--mod flag,
// falling back to the core definitions
func maybeInitializeDefaultModule(ctx context.Context, dag *dagger.Client) (*moduleDef, string, error) {
	modRef, _ := getExplicitModuleSourceRef()
	if modRef == "" {
		modRef = moduleURLDefault
	}
	return maybeInitializeModule(ctx, dag, modRef)
}

// initializeModule loads the module at the given source ref
//
// Returns an error if the module is not found or invalid.
func initializeModule(
	ctx context.Context,
	dag *dagger.Client,
	srcRef string,
	doFindUp bool,
	srcOpts ...dagger.ModuleSourceOpts,
) (rdef *moduleDef, rerr error) {
	ctx, span := Tracer().Start(ctx, "load module")
	defer telemetry.End(span, func() error { return rerr })

	findCtx, findSpan := Tracer().Start(ctx, "finding module configuration", telemetry.Encapsulate())
	conf, err := getModuleConfigurationForSourceRef(findCtx, dag, srcRef, doFindUp, true, srcOpts...)
	defer telemetry.End(findSpan, func() error { return err })

	if err != nil {
		return nil, fmt.Errorf("failed to get configured module: %w", err)
	}
	if !conf.FullyInitialized() {
		return nil, fmt.Errorf("module must be fully initialized")
	}

	return initializeModuleConfig(ctx, dag, conf)
}

// maybeInitializeModule optionally loads the module at the given source ref,
// falling back to the core definitions if the module isn't found
func maybeInitializeModule(ctx context.Context, dag *dagger.Client, srcRef string) (*moduleDef, string, error) {
	if def, err := tryInitializeModule(ctx, dag, srcRef); def != nil || err != nil {
		return def, srcRef, err
	}

	def, err := initializeCore(ctx, dag)
	return def, "", err
}

// tryInitializeModule tries to load a module if it exists
//
// Returns an error if the module is invalid or couldn't be loaded, but not
// if the module wasn't found.
func tryInitializeModule(ctx context.Context, dag *dagger.Client, srcRef string) (rdef *moduleDef, rerr error) {
	ctx, span := Tracer().Start(ctx, "looking for module")
	defer telemetry.End(span, func() error { return rerr })

	findCtx, findSpan := Tracer().Start(ctx, "finding module configuration", telemetry.Encapsulate())
	conf, _ := getModuleConfigurationForSourceRef(findCtx, dag, srcRef, true, true)
	findSpan.End()

	if conf == nil || !conf.FullyInitialized() {
		return nil, nil
	}

	span.SetName("load module")

	return initializeModuleConfig(ctx, dag, conf)
}

// initializeModuleConfig loads a module using a detected module configuration
func initializeModuleConfig(ctx context.Context, dag *dagger.Client, conf *configuredModule) (rdef *moduleDef, rerr error) {
	serveCtx, serveSpan := Tracer().Start(ctx, "initializing module", telemetry.Encapsulate())
	err := conf.Source.AsModule().Initialize().Serve(serveCtx)
	telemetry.End(serveSpan, func() error { return err })
	if err != nil {
		return nil, fmt.Errorf("failed to serve module: %w", err)
	}

	def, err := inspectModule(ctx, dag, conf.Source)
	if err != nil {
		return nil, err
	}

	return def, def.loadTypeDefs(ctx, dag)
}

// moduleDef is a representation of a dagger module.
type moduleDef struct {
	Name        string
	Description string
	MainObject  *modTypeDef
	Objects     []*modTypeDef
	Interfaces  []*modTypeDef
	Enums       []*modTypeDef
	Inputs      []*modTypeDef

	// the ModuleSource definition for the module, needed by some arg types
	// applying module-specific configs to the arg value.
	Source *dagger.ModuleSource

	// ModRef is the human readable module source reference as returned by the API
	ModRef string

	Dependencies []*moduleDependency
}

type moduleDependency struct {
	Name        string
	Description string
	Source      *dagger.ModuleSource

	// ModRef is the human readable module source reference as returned by the API
	ModRef string

	// RefPin is the module source pin for this dependency, if any
	RefPin string
}

func (m *moduleDependency) Short() string {
	s := m.Description
	if s == "" {
		s = "-"
	}
	return strings.SplitN(s, "\n", 2)[0]
}

//go:embed modconf.graphql
var loadModConfQuery string

//go:embed typedefs.graphql
var loadTypeDefsQuery string

func inspectModule(ctx context.Context, dag *dagger.Client, source *dagger.ModuleSource) (rdef *moduleDef, rerr error) {
	ctx, span := Tracer().Start(ctx, "inspecting module metadata", telemetry.Encapsulate())
	defer telemetry.End(span, func() error { return rerr })

	// NB: All we need most of the time is the name of the dependencies.
	// We need the descriptions when listing the dependencies, and the source
	// ref if we need to load a specific dependency. However getting the refs
	// and descriptions here, at module load, doesn't add much overhead and
	// makes it easier (and faster) later.

	var res struct {
		Source struct {
			AsString string
			Module   struct {
				Name       string
				Initialize struct {
					Description string
				}
				Dependencies []struct {
					Name        string
					Description string
					Source      struct {
						AsString string
						Pin      string
					}
				}
			}
		}
	}

	id, err := source.ID(ctx)
	if err != nil {
		return nil, err
	}

	err = dag.Do(ctx, &dagger.Request{
		Query: loadModConfQuery,
		Variables: map[string]any{
			"source": id,
		},
	}, &dagger.Response{
		Data: &res,
	})
	if err != nil {
		return nil, fmt.Errorf("query module metadata: %w", err)
	}

	deps := make([]*moduleDependency, 0, len(res.Source.Module.Dependencies))
	for _, dep := range res.Source.Module.Dependencies {
		deps = append(deps, &moduleDependency{
			Name:        dep.Name,
			Description: dep.Description,
			ModRef:      dep.Source.AsString,
			RefPin:      dep.Source.Pin,
		})
	}

	def := &moduleDef{
		Source:       source,
		ModRef:       res.Source.AsString,
		Name:         res.Source.Module.Name,
		Description:  res.Source.Module.Initialize.Description,
		Dependencies: deps,
	}

	return def, nil
}

// loadModTypeDefs loads the objects defined by the given module in an easier to use data structure.
func (m *moduleDef) loadTypeDefs(ctx context.Context, dag *dagger.Client) (rerr error) {
	ctx, loadSpan := Tracer().Start(ctx, "loading type definitions", telemetry.Encapsulate())
	defer telemetry.End(loadSpan, func() error { return rerr })

	var res struct {
		TypeDefs []*modTypeDef
	}

	err := dag.Do(ctx, &dagger.Request{
		Query: loadTypeDefsQuery,
	}, &dagger.Response{
		Data: &res,
	})
	if err != nil {
		return fmt.Errorf("query module objects: %w", err)
	}

	name := gqlObjectName(m.Name)
	if name == "" {
		name = "Query"
	}

	for _, typeDef := range res.TypeDefs {
		switch typeDef.Kind {
		case dagger.TypeDefKindObjectKind:
			obj := typeDef.AsObject
			// FIXME: we could get the real constructor's name through the field
			// in Query which would avoid the need to convert the module name,
			// but the Query TypeDef is loaded before the module so the module
			// isn't available in its functions list.
			if name == gqlObjectName(obj.Name) {
				m.MainObject = typeDef

				// There's always a constructor, even if the SDK didn't define one.
				// Make sure one always exists to make it easier to reuse code while
				// building out Cobra.
				if obj.Constructor == nil {
					obj.Constructor = &modFunction{ReturnType: typeDef}
				}

				if name != "Query" {
					// Constructors have an empty function name in ObjectTypeDef.
					obj.Constructor.Name = gqlFieldName(obj.Name)
				}
			}
			m.Objects = append(m.Objects, typeDef)
		case dagger.TypeDefKindInterfaceKind:
			m.Interfaces = append(m.Interfaces, typeDef)
		case dagger.TypeDefKindEnumKind:
			m.Enums = append(m.Enums, typeDef)
		case dagger.TypeDefKindInputKind:
			m.Inputs = append(m.Inputs, typeDef)
		}
	}

	if m.MainObject == nil {
		return fmt.Errorf("main object not found, check that your module's name and main object match")
	}

	m.LoadFunctionTypeDefs(m.MainObject.AsObject.Constructor)

	// FIXME: the API doesn't return the module constructor in the Query object
	rootObj := m.GetObject("Query")
	if !rootObj.HasFunction(m.MainObject.AsObject.Constructor) {
		rootObj.Functions = append(rootObj.Functions, m.MainObject.AsObject.Constructor)
	}

	return nil
}

func (m *moduleDef) Long() string {
	s := m.Name
	if m.Description != "" {
		return s + "\n\n" + m.Description
	}
	return s
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

func (m *moduleDef) AsEnums() []*modEnum {
	var defs []*modEnum
	for _, typeDef := range m.Enums {
		if typeDef.AsEnum != nil {
			defs = append(defs, typeDef.AsEnum)
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

func (m *moduleDef) GetObjectFunction(objectName, functionName string) (*modFunction, error) {
	fp := m.GetFunctionProvider(objectName)
	if fp == nil {
		return nil, fmt.Errorf("module %q does not have a %q object or interface", m.Name, objectName)
	}
	return m.GetFunction(fp, functionName)
}

func (m *moduleDef) GetFunction(fp functionProvider, functionName string) (*modFunction, error) {
	// This avoids an issue with module constructors overriding core functions.
	// See https://github.com/dagger/dagger/issues/9122
	if m.HasModule() && fp.ProviderName() == "Query" && m.MainObject.AsObject.Constructor.CmdName() == functionName {
		return m.MainObject.AsObject.Constructor, nil
	}
	for _, fn := range fp.GetFunctions() {
		if fn.Name == functionName || fn.CmdName() == functionName {
			m.LoadFunctionTypeDefs(fn)
			return fn, nil
		}
	}
	return nil, fmt.Errorf("no function %q in type %q", functionName, fp.ProviderName())
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

// GetEnum retrieves a saved enum type definition from the module.
func (m *moduleDef) GetEnum(name string) *modEnum {
	for _, enum := range m.AsEnums() {
		// Normalize name in case an SDK uses a different convention for object names.
		if gqlObjectName(enum.Name) == gqlObjectName(name) {
			return enum
		}
	}
	return nil
}

// GetFunctionProvider retrieves a saved object or interface type definition from the module as a functionProvider.
func (m *moduleDef) GetFunctionProvider(name string) functionProvider {
	if obj := m.GetObject(name); obj != nil {
		return obj
	}
	if iface := m.GetInterface(name); iface != nil {
		return iface
	}
	return nil
}

// GetInput retrieves a saved input type definition from the module.
func (m *moduleDef) GetInput(name string) *modInput {
	for _, input := range m.AsInputs() {
		// Normalize name in case an SDK uses a different convention for input names.
		if gqlObjectName(input.Name) == gqlObjectName(name) {
			return input
		}
	}
	return nil
}

func (m *moduleDef) GetDependency(name string) *moduleDependency {
	for _, dep := range m.Dependencies {
		if dep.Name == name {
			return dep
		}
	}
	return nil
}

// HasModule checks if a module's definitions are loaded
func (m *moduleDef) HasModule() bool {
	return m.Name != ""
}

func (m *moduleDef) GetCoreFunctions() []*modFunction {
	all := m.GetFunctionProvider("Query").GetFunctions()
	fns := make([]*modFunction, 0, len(all))

	for _, fn := range all {
		if fn.ReturnType.AsObject != nil && !fn.ReturnType.AsObject.IsCore() || fn.Name == "" {
			continue
		}
		fns = append(fns, fn)
	}

	return fns
}

// GetCoreFunction returns a core function with the given name.
func (m *moduleDef) GetCoreFunction(name string) *modFunction {
	for _, fn := range m.GetCoreFunctions() {
		if fn.Name == name || fn.CmdName() == name {
			return fn
		}
	}
	return nil
}

// HasCoreFunction checks if there's a core function with the given name.
func (m *moduleDef) HasCoreFunction(name string) bool {
	fn := m.GetCoreFunction(name)
	return fn != nil
}

func (m *moduleDef) HasMainFunction(name string) bool {
	return m.HasFunction(m.MainObject.AsFunctionProvider(), name)
}

// HasFunction checks if an object has a function with the given name.
func (m *moduleDef) HasFunction(fp functionProvider, name string) bool {
	if fp == nil {
		return false
	}
	fn, _ := m.GetFunction(fp, name)
	return fn != nil
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
	if typeDef.AsEnum != nil {
		enum := m.GetEnum(typeDef.AsEnum.Name)
		if enum != nil {
			typeDef.AsEnum = enum
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

func (m *moduleDef) LoadFunctionTypeDefs(fn *modFunction) {
	// We need to load references to types with their type definitions because
	// the introspection doesn't recursively add them, just their names.
	m.LoadTypeDef(fn.ReturnType)
	for _, arg := range fn.Args {
		m.LoadTypeDef(arg.TypeDef)
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
	AsScalar    *modScalar
	AsEnum      *modEnum
}

func (t *modTypeDef) String() string {
	switch t.Kind {
	case dagger.TypeDefKindStringKind:
		return "string"
	case dagger.TypeDefKindIntegerKind:
		return "int"
	case dagger.TypeDefKindBooleanKind:
		return "bool"
	case dagger.TypeDefKindVoidKind:
		return "void"
	case dagger.TypeDefKindScalarKind:
		return t.AsScalar.Name
	case dagger.TypeDefKindEnumKind:
		return t.AsEnum.Name
	case dagger.TypeDefKindInputKind:
		return t.AsInput.Name
	case dagger.TypeDefKindObjectKind:
		return t.AsObject.Name
	case dagger.TypeDefKindInterfaceKind:
		return t.AsInterface.Name
	case dagger.TypeDefKindListKind:
		return "[]" + t.AsList.ElementTypeDef.String()
	default:
		// this should never happen because all values for kind are covered,
		// unless a new one is added and this code isn't updated
		return ""
	}
}

func (t *modTypeDef) KindDisplay() string {
	switch t.Kind {
	case dagger.TypeDefKindStringKind,
		dagger.TypeDefKindIntegerKind,
		dagger.TypeDefKindBooleanKind:
		return "Scalar"
	case dagger.TypeDefKindScalarKind,
		dagger.TypeDefKindVoidKind:
		return "Custom scalar"
	case dagger.TypeDefKindEnumKind:
		return "Enum"
	case dagger.TypeDefKindInputKind:
		return "Input"
	case dagger.TypeDefKindObjectKind:
		return "Object"
	case dagger.TypeDefKindInterfaceKind:
		return "Interface"
	case dagger.TypeDefKindListKind:
		return "List of " + strings.ToLower(t.AsList.ElementTypeDef.KindDisplay()) + "s"
	default:
		return ""
	}
}

func (t *modTypeDef) Description() string {
	switch t.Kind {
	case dagger.TypeDefKindStringKind,
		dagger.TypeDefKindIntegerKind,
		dagger.TypeDefKindBooleanKind:
		return "Primitive type."
	case dagger.TypeDefKindVoidKind:
		return ""
	case dagger.TypeDefKindScalarKind:
		return t.AsScalar.Description
	case dagger.TypeDefKindEnumKind:
		return t.AsEnum.Description
	case dagger.TypeDefKindInputKind:
		return t.AsInput.Description
	case dagger.TypeDefKindObjectKind:
		return t.AsObject.Description
	case dagger.TypeDefKindInterfaceKind:
		return t.AsInterface.Description
	case dagger.TypeDefKindListKind:
		return t.AsList.ElementTypeDef.Description()
	default:
		// this should never happen because all values for kind are covered,
		// unless a new one is added and this code isn't updated
		return ""
	}
}

func (t *modTypeDef) Short() string {
	s := t.String()
	if d := t.Description(); d != "" {
		return s + " - " + strings.SplitN(d, "\n", 2)[0]
	}
	return s
}

func (t *modTypeDef) Long() string {
	s := t.String()
	if d := t.Description(); d != "" {
		return s + "\n\n" + d
	}
	return s
}

type functionProvider interface {
	ProviderName() string
	GetFunctions() []*modFunction
	IsCore() bool
}

func GetSupportedFunctions(fp functionProvider) ([]*modFunction, []string) {
	allFns := fp.GetFunctions()
	fns := make([]*modFunction, 0, len(allFns))
	skipped := make([]string, 0, len(allFns))
	for _, fn := range allFns {
		if skipFunction(fp.ProviderName(), fn.Name) || fn.HasUnsupportedFlags() {
			skipped = append(skipped, fn.CmdName())
		} else {
			fns = append(fns, fn)
		}
	}
	return fns, skipped
}

func GetSupportedFunction(md *moduleDef, fp functionProvider, name string) (*modFunction, error) {
	fn, err := md.GetFunction(fp, name)
	if err != nil {
		return nil, err
	}
	_, skipped := GetSupportedFunctions(fp)
	if slices.Contains(skipped, fn.CmdName()) {
		return nil, fmt.Errorf("function %q in type %q is not supported", name, fp.ProviderName())
	}
	return fn, nil
}

func skipFunction(obj, field string) bool {
	// TODO: make this configurable in the API but may not be easy to
	// generalize because an "internal" field may still need to exist in
	// codegen, for example. Could expose if internal via the TypeDefs though.
	skip := map[string][]string{
		"Query": {
			// for SDKs only
			"builtinContainer",
			"generatedCode",
			"currentFunctionCall",
			"currentModule",
			"typeDef",
			// not useful until the CLI accepts ID inputs
			"cacheVolume",
			"setSecret",
			// for tests only
			"secret",
			// deprecated
			"pipeline",
		},
	}
	if fields, ok := skip[obj]; ok {
		return slices.Contains(fields, field)
	}
	return false
}

// skipLeaves is a map of provider names to function names that should be skipped
// when looking for leaf functions.
var skipLeaves = map[string][]string{
	"Container": {
		// imageRef should only be requested right after a `from`, and that's
		// hard to check for here.
		"imageRef",
		// stdout and stderr may be arbitrarily large and jarring to see (e.g. test suites)
		"stdout",
		"stderr",
		// avoid potential error if no previous execution
		"exitCode",
	},
	"File": {
		// This could be a binary file, so until we can tell which type of
		// file it is, best to skip it for now.
		"contents",
	},
	"Secret": {
		// Don't leak secrets.
		"plaintext",
	},
}

// GetLeafFunctions returns the leaf functions of an object or interface
//
// Leaf functions return simple values like a scalar or a list of scalars.
// If from a module, they are limited to fields. But if from a core type,
// any function without arguments is considered, excluding a few hardcoded
// ones.
//
// Functions that return an ID are excluded since the CLI can't handle them
// as input arguments yet, so they'd add noise when listing an object's leaves.
func GetLeafFunctions(fp functionProvider) []*modFunction {
	var fns []*modFunction
	// not including interfaces from modules because interfaces don't have fields
	if fp.IsCore() {
		fns = fp.GetFunctions()
	} else if obj, ok := fp.(*modObject); ok {
		fns = obj.GetFieldFunctions()
	}
	r := make([]*modFunction, 0, len(fns))

	for _, fn := range fns {
		kind := fn.ReturnType.Kind
		if kind == dagger.TypeDefKindListKind {
			kind = fn.ReturnType.AsList.ElementTypeDef.Kind
		}
		switch kind {
		case dagger.TypeDefKindObjectKind, dagger.TypeDefKindInterfaceKind, dagger.TypeDefKindVoidKind:
			continue
		case dagger.TypeDefKindScalarKind:
			// FIXME: ID types are coming from TypeDef with the wrong case ("Id")
			if fn.ReturnType.AsScalar.Name == fmt.Sprintf("%sId", fp.ProviderName()) {
				continue
			}
		}
		if names, ok := skipLeaves[fp.ProviderName()]; ok && slices.Contains(names, fn.Name) {
			continue
		}
		if fn.HasRequiredArgs() {
			continue
		}
		r = append(r, fn)
	}
	return r
}

func (t *modTypeDef) Name() string {
	if fp := t.AsFunctionProvider(); fp != nil {
		return fp.ProviderName()
	}
	return ""
}

func (t *modTypeDef) AsFunctionProvider() functionProvider {
	if t.AsList != nil {
		t = t.AsList.ElementTypeDef
	}
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
	Description      string
	Functions        []*modFunction
	Fields           []*modField
	Constructor      *modFunction
	SourceModuleName string
}

var _ functionProvider = (*modObject)(nil)

func (o *modObject) ProviderName() string {
	return o.Name
}

func (o *modObject) IsCore() bool {
	return o.SourceModuleName == ""
}

// GetFunctions returns the object's function definitions including the fields,
// which are treated as functions with no arguments.
func (o *modObject) GetFunctions() []*modFunction {
	return append(o.GetFieldFunctions(), o.Functions...)
}

func (o *modObject) GetFieldFunctions() []*modFunction {
	fns := make([]*modFunction, 0, len(o.Fields))
	for _, f := range o.Fields {
		fns = append(fns, f.AsFunction())
	}
	return fns
}

func (o *modObject) HasFunction(f *modFunction) bool {
	for _, fn := range o.Functions {
		if fn.Name == f.Name {
			return true
		}
	}
	return false
}

type modInterface struct {
	Name             string
	Description      string
	Functions        []*modFunction
	SourceModuleName string
}

var _ functionProvider = (*modInterface)(nil)

func (o *modInterface) ProviderName() string {
	return o.Name
}

func (o *modInterface) IsCore() bool {
	return o.SourceModuleName == ""
}

func (o *modInterface) GetFunctions() []*modFunction {
	return o.Functions
}

type modScalar struct {
	Name        string
	Description string
}

type modEnum struct {
	Name        string
	Description string
	Values      []*modEnumValue
}

func (e *modEnum) ValueNames() []string {
	values := make([]string, 0, len(e.Values))
	for _, v := range e.Values {
		values = append(values, v.Name)
	}
	return values
}

type modEnumValue struct {
	Name        string
	Description string
}

type modInput struct {
	Name        string
	Description string
	Fields      []*modField
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

func (f *modField) AsFunction() *modFunction {
	return &modFunction{
		Name:        f.Name,
		Description: f.Description,
		ReturnType:  f.TypeDef,
	}
}

// modFunction is a representation of dagger.Function.
type modFunction struct {
	Name        string
	Description string
	ReturnType  *modTypeDef
	Args        []*modFunctionArg
	cmdName     string
}

func (f *modFunction) CmdName() string {
	if f.cmdName == "" {
		f.cmdName = cliName(f.Name)
	}
	return f.cmdName
}

func (f *modFunction) Short() string {
	s := strings.SplitN(f.Description, "\n", 2)[0]
	if s == "" {
		s = "-"
	}
	return s
}

// GetArg returns the argument definition corresponding to the given name.
func (f *modFunction) GetArg(name string) (*modFunctionArg, error) {
	for _, a := range f.Args {
		if a.FlagName() == name {
			return a, nil
		}
	}
	return nil, fmt.Errorf("no argument %q in function %q", name, f.CmdName())
}

func (f *modFunction) HasRequiredArgs() bool {
	for _, arg := range f.Args {
		if arg.IsRequired() {
			return true
		}
	}
	return false
}

func (f *modFunction) RequiredArgs() []*modFunctionArg {
	args := make([]*modFunctionArg, 0, len(f.Args))
	for _, arg := range f.Args {
		if arg.IsRequired() {
			args = append(args, arg)
		}
	}
	return args
}

func (f *modFunction) OptionalArgs() []*modFunctionArg {
	args := make([]*modFunctionArg, 0, len(f.Args))
	for _, arg := range f.Args {
		if !arg.IsRequired() {
			args = append(args, arg)
		}
	}
	return args
}

func (f *modFunction) SupportedArgs() []*modFunctionArg {
	args := make([]*modFunctionArg, 0, len(f.Args))
	for _, arg := range f.Args {
		if !arg.IsUnsupportedFlag() {
			args = append(args, arg)
		}
	}
	return args
}

func (f *modFunction) HasUnsupportedFlags() bool {
	for _, arg := range f.Args {
		if arg.IsRequired() && arg.IsUnsupportedFlag() {
			return true
		}
	}
	return false
}

func (f *modFunction) ReturnsCoreObject() bool {
	if fp := f.ReturnType.AsFunctionProvider(); fp != nil {
		return fp.IsCore()
	}
	return false
}

// modFunctionArg is a representation of dagger.FunctionArg.
type modFunctionArg struct {
	Name         string
	Description  string
	TypeDef      *modTypeDef
	DefaultValue dagger.JSON
	DefaultPath  string
	Ignore       []string
	flagName     string
}

// FlagName returns the name of the argument using CLI naming conventions.
func (r *modFunctionArg) FlagName() string {
	if r.flagName == "" {
		r.flagName = cliName(r.Name)
	}
	return r.flagName
}

func (r *modFunctionArg) Usage() string {
	return fmt.Sprintf("--%s %s", r.FlagName(), r.TypeDef.String())
}

func (r *modFunctionArg) Short() string {
	return strings.SplitN(r.Description, "\n", 2)[0]
}

func (r *modFunctionArg) Long() string {
	sb := new(strings.Builder)
	multiline := strings.Contains(r.Description, "\n")

	if r.Description != "" {
		sb.WriteString(r.Description)
	}

	if defVal := r.defValue(); defVal != "" {
		if multiline {
			sb.WriteString("\n\n")
		} else if sb.Len() > 0 {
			sb.WriteString(" ")
		}
		sb.WriteString(fmt.Sprintf("(default: %s)", defVal))
	}

	if r.TypeDef.Kind == dagger.TypeDefKindEnumKind {
		names := strings.Join(r.TypeDef.AsEnum.ValueNames(), ", ")
		if multiline {
			sb.WriteString("\n\n")
		} else if sb.Len() > 0 {
			sb.WriteString(" ")
		}
		sb.WriteString(fmt.Sprintf("(possible values: %s)", names))
	}

	return sb.String()
}

func (r *modFunctionArg) IsRequired() bool {
	return !r.TypeDef.Optional && r.DefaultValue == ""
}

func (r *modFunctionArg) IsUnsupportedFlag() bool {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	err := r.AddFlag(flags)
	var e *UnsupportedFlagError
	return errors.As(err, &e)
}

func getDefaultValue[T any](r *modFunctionArg) (T, error) {
	var val T
	err := json.Unmarshal([]byte(r.DefaultValue), &val)
	return val, err
}

// DefValue is the default value (as text); for the usage message
func (r *modFunctionArg) defValue() string {
	if r.DefaultPath != "" {
		return fmt.Sprintf("%q", r.DefaultPath)
	}
	if r.DefaultValue == "" {
		return ""
	}
	t := r.TypeDef
	switch t.Kind {
	case dagger.TypeDefKindStringKind:
		v, err := getDefaultValue[string](r)
		if err == nil {
			return fmt.Sprintf("%q", v)
		}
	default:
		v, err := getDefaultValue[any](r)
		if err == nil {
			return fmt.Sprintf("%v", v)
		}
	}
	return ""
}

// gqlObjectName converts casing to a GraphQL object  name
func gqlObjectName(name string) string {
	return strcase.ToCamel(name)
}

// gqlFieldName converts casing to a GraphQL object field name
func gqlFieldName(name string) string {
	return strcase.ToLowerCamel(name)
}

// cliName converts casing to the CLI convention (kebab)
func cliName(name string) string {
	return strcase.ToKebab(name)
}
