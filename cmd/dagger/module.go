package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/iancoleman/strcase"
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

	listenCmd.PersistentFlags().AddFlagSet(moduleFlags)
	queryCmd.PersistentFlags().AddFlagSet(moduleFlags)
	funcCmds.AddFlagSet(moduleFlags)
	configCmd.PersistentFlags().AddFlagSet(moduleFlags)

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
}

var moduleInitCmd = &cobra.Command{
	Use:   "init [options] [path]",
	Short: "Initialize a new Dagger module",
	Long: `Initialize a new Dagger module in a local directory.
By default, create a new dagger.json configuration in the current working directory. If the positional argument PATH is provided, create the module in that directory instead.

The configuration will default the name of the module to the parent directory name, unless specified with --name.

Any module can be installed to via "dagger install".

A module can only be called once it has been initialized with an SDK though. The "--sdk" flag can be provided to init here, but if it's not the configuration can be updated later via "dagger develop".

The "--source" flag allows controlling the directory in which the actual module source code is stored. By default, it will be stored in a directory named "dagger".
`,
	Example: "dagger init --name=hello --sdk=python --source=some/subdir",
	GroupID: moduleGroup.ID,
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()

		return withEngine(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()

			// default the module source root to the current working directory if it doesn't exist yet
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

			modConf, err := getModuleConfigurationForSourceRef(ctx, dag, srcRootPath, false, false)
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

			if sdk != "" {
				// If we're generating code by setting a SDK, we should also generate a license
				// if it doesn't already exists.
				if err := findOrCreateLicense(ctx, modConf.LocalRootSourcePath); err != nil {
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
	Short:   "Add a new dependency to a Dagger module",
	Long:    "Add a Dagger module as a dependency of a local module.",
	// TODO: use example from a reference module, using a tag instead of commit
	Example: "dagger install github.com/shykes/daggerverse/ttlsh@16e40ec244966e55e36a13cb6e1ff8023e1e1473",
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
	Use:   "develop [options]",
	Short: "Setup or update all the resources needed to develop on a module locally",
	Long: `Setup or update all the resources needed to develop on a module locally.

This command regenerates the module's code based on dependencies
and the current state of the module's source code.

If --sdk is set, the config file and generated code will be updated with those values reflected. It currently can only be used to set the SDK of a module that does not have one already.

--source allows controlling the directory in which the actual module source code is stored. By default, it will be stored in a directory named "dagger".

:::note
If not updating source or SDK, this is only required for IDE auto-completion/LSP purposes.
:::
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

			_, err = src.ResolveFromCaller().
				AsModule().
				GeneratedContextDiff().
				Export(ctx, modConf.LocalContextPath)
			if err != nil {
				return fmt.Errorf("failed to generate code: %w", err)
			}

			// If no license has been created yet, we should create one.
			if err := findOrCreateLicense(ctx, modConf.LocalRootSourcePath); err != nil {
				return err
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
			if modConf.SourceKind != dagger.LocalSource {
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
) (*configuredModule, error) {
	conf := &configuredModule{}

	conf.Source = dag.ModuleSource(srcRefStr)
	var err error
	conf.SourceKind, err = conf.Source.Kind(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get module ref kind: %w", err)
	}

	if conf.SourceKind == dagger.GitSource {
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
				depSrc := dag.ModuleSource(namedDep.Source)
				depKind, err := depSrc.Kind(ctx)
				if err != nil {
					return nil, err
				}
				depSrcRef := namedDep.Source
				if depKind == dagger.LocalSource {
					depSrcRef = filepath.Join(defaultFindupConfigDir, namedDep.Source)
				}
				return getModuleConfigurationForSourceRef(ctx, dag, depSrcRef, false, resolveFromCaller)
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

	conf.LocalRootSourcePath, err = filepath.Abs(srcRefStr)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for %s: %w", srcRefStr, err)
	}
	if filepath.IsAbs(srcRefStr) {
		cwd, err := os.Getwd()
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

func findUp(curDirPath string) (string, bool, error) {
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
	curDirAbsPath, err := filepath.Abs(curDirPath)
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
				_, err := loadedMod.Serve(ctx)
				if err != nil {
					return fmt.Errorf("failed to serve module: %w", err)
				}
			}
			return fn(ctx, engineClient, loadedMod, cmd, cmdArgs)
		})
	}
}

// moduleDef is a representation of a dagger module.
type moduleDef struct {
	Name       string
	MainObject *modTypeDef
	Objects    []*modTypeDef
	Interfaces []*modTypeDef
	Enums      []*modTypeDef
	Inputs     []*modTypeDef

	// the ModuleSource definition for the module, needed by some arg types
	// applying module-specific configs to the arg value.
	Source *dagger.ModuleSource
}

// loadModTypeDefs loads the objects defined by the given module in an easier to use data structure.
func (m *moduleDef) loadTypeDefs(ctx context.Context, dag *dagger.Client) error {
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
	asScalar {
		name
	}
	asEnum {
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
			asScalar {
				name
			}
			asEnum {
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

query TypeDefs {
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
		asScalar {
			name
		}
		asEnum {
			name
			values {
				name
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
	}, &dagger.Response{
		Data: &res,
	})
	if err != nil {
		return fmt.Errorf("query module objects: %w", err)
	}

	name := gqlObjectName(m.Name)

	for _, typeDef := range res.TypeDefs {
		switch typeDef.Kind {
		case dagger.ObjectKind:
			obj := typeDef.AsObject
			if name == gqlObjectName(obj.Name) {
				m.MainObject = typeDef

				// There's always a constructor, even if the SDK didn't define one.
				// Make sure one always exists to make it easier to reuse code while
				// building out Cobra.
				if obj.Constructor == nil {
					obj.Constructor = &modFunction{ReturnType: typeDef}
				}

				// Constructors have an empty function name in ObjectTypeDef.
				obj.Constructor.Name = gqlFieldName(obj.Name)
			}
			m.Objects = append(m.Objects, typeDef)
		case dagger.InterfaceKind:
			m.Interfaces = append(m.Interfaces, typeDef)
		case dagger.EnumKind:
			m.Enums = append(m.Enums, typeDef)
		case dagger.InputKind:
			m.Inputs = append(m.Inputs, typeDef)
		}
	}

	return nil
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

type functionProvider interface {
	ProviderName() string
	GetFunctions() []*modFunction
	IsCore() bool
}

func GetFunction(o functionProvider, name string) (*modFunction, error) {
	for _, fn := range o.GetFunctions() {
		if fn.Name == name || fn.CmdName() == name {
			return fn, nil
		}
	}
	return nil, fmt.Errorf("no function %q in type %q", name, o.ProviderName())
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

func (o *modObject) IsCore() bool {
	return o.SourceModuleName == ""
}

// GetStateFunctions returns the object's fields as function definitions.
func (o *modObject) GetStateFunctions() []*modFunction {
	fns := make([]*modFunction, 0, len(o.Fields))
	for _, f := range o.Fields {
		fns = append(fns, &modFunction{
			Name:        f.Name,
			Description: f.Description,
			ReturnType:  f.TypeDef,
		})
	}
	return fns
}

// GetFunctions returns the object's function definitions including the fields,
// which are treated as functions with no arguments.
func (o *modObject) GetFunctions() []*modFunction {
	fns := make([]*modFunction, 0, len(o.Fields)+len(o.Functions))
	fns = append(fns, o.GetStateFunctions()...)
	return append(fns, o.Functions...)
}

type modInterface struct {
	Name             string
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
	Name string
}

type modEnum struct {
	Name   string
	Values []*modEnumValue
}

type modEnumValue struct {
	Name string
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
	cmdName     string
}

func (f *modFunction) CmdName() string {
	if f.cmdName == "" {
		f.cmdName = cliName(f.Name)
	}
	return f.cmdName
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

func (f *modFunction) IsUnsupported() bool {
	for _, arg := range f.Args {
		if arg.IsRequired() && arg.IsUnsupportedFlag() {
			return true
		}
	}
	return false
}

func (f *modFunction) ReturnsCoreObject() bool {
	t := f.ReturnType
	if t.AsList != nil {
		t = t.AsList.ElementTypeDef
	}
	fp := t.AsFunctionProvider()
	if fp != nil {
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
	flagName     string
}

// FlagName returns the name of the argument using CLI naming conventions.
func (r *modFunctionArg) FlagName() string {
	if r.flagName == "" {
		r.flagName = cliName(r.Name)
	}
	return r.flagName
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
