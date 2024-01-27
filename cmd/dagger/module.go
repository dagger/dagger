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
	"github.com/juju/ansiterm/tabwriter"
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

	installName string

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
	moduleInitCmd.PersistentFlags().StringVar(&moduleName, "name", "", "Name of the new module")
	moduleInitCmd.PersistentFlags().StringVar(&licenseID, "license", "", "License identifier to generate - see https://spdx.org/licenses/")

	modulePublishCmd.PersistentFlags().BoolVarP(&force, "force", "f", false, "Force publish even if the git repository is not clean.")

	moduleInstallCmd.PersistentFlags().StringVarP(&installName, "name", "n", "", "Name to use for the dependency in the module. Defaults to the name of the module being installed.")

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
			modConf, err := getDefaultModuleConfiguration(ctx, engineClient.Dagger())
			if err != nil {
				return fmt.Errorf("failed to load module: %w", err)
			}
			mod := modConf.Mod

			name, err := mod.Name(ctx)
			if err != nil {
				return fmt.Errorf("failed to get module name: %w", err)
			}
			sdk, err := mod.SDK(ctx)
			if err != nil {
				return fmt.Errorf("failed to get module SDK: %w", err)
			}
			srcPath, err := mod.Source().Subpath(ctx)
			if err != nil {
				return fmt.Errorf("failed to get module source directory: %w", err)
			}
			depMods, err := mod.Dependencies(ctx)
			if err != nil {
				return fmt.Errorf("failed to get module dependencies: %w", err)
			}
			var depModNames []string
			for _, depMod := range depMods {
				depModName, err := depMod.Name(ctx)
				if err != nil {
					return fmt.Errorf("failed to get module name: %w", err)
				}
				depModNames = append(depModNames, depModName)
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
				"Source:",
				srcPath,
			)
			fmt.Fprintf(tw, "%s\t%s\n",
				"Dependencies:",
				strings.Join(depModNames, ", "),
			)

			return tw.Flush()
		})
	},
}

var moduleInitCmd = &cobra.Command{
	Use:    "init",
	Short:  "Initialize a new dagger module in a local directory.",
	Hidden: false,
	RunE: func(cmd *cobra.Command, _ []string) (rerr error) {
		if xor(sdk == "", moduleName == "") {
			return fmt.Errorf("must specify both --sdk and --name or neither")
		}
		ctx := cmd.Context()

		return withEngineAndTUI(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()
			modConf, err := getDefaultModuleConfiguration(ctx, dag)
			if err != nil {
				return fmt.Errorf("failed to get configured module: %w", err)
			}

			if modConf.SourceKind != dagger.LocalSource {
				return fmt.Errorf("module must be local")
			}
			if modConf.ModuleSourceConfigExists {
				return fmt.Errorf("module already exists")
			}

			_, err = modConf.Mod.
				WithName(moduleName).
				WithSDK(sdk).
				GeneratedSourceDirectory().
				Export(ctx, modConf.LocalRootPath)
			if err != nil {
				return fmt.Errorf("failed to generate code: %w", err)
			}

			if err := findOrCreateLicense(ctx, modConf.LocalSourcePath); err != nil {
				return err
			}

			return nil
		})
	},
}

func xor(a, b bool) bool {
	return (a || b) && !(a && b)
}

var moduleInstallCmd = &cobra.Command{
	Use:     "install",
	Aliases: []string{"use"},
	Short:   "Add a new dependency to a dagger module",
	Hidden:  false,
	RunE: func(cmd *cobra.Command, extraArgs []string) (rerr error) {
		ctx := cmd.Context()
		if len(extraArgs) != 1 {
			return fmt.Errorf("expected exactly one argument for the dependency to install")
		}
		return withEngineAndTUI(ctx, client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
			dag := engineClient.Dagger()
			modConf, err := getDefaultModuleConfiguration(ctx, dag)
			if err != nil {
				return fmt.Errorf("failed to get configured module: %w", err)
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
					return fmt.Errorf("failed to get absolute path for %s: %w", depRefStr, err)
				}
				depRelPath, err := filepath.Rel(modConf.LocalSourcePath, depAbsPath)
				if err != nil {
					return fmt.Errorf("failed to get relative path: %w", err)
				}
				depSrc = dag.ModuleSource(depRelPath)
			}
			dep := dag.ModuleDependency(depSrc, dagger.ModuleDependencyOpts{
				Name: installName,
			})

			_, err = modConf.Mod.
				WithDependencies([]*dagger.ModuleDependency{dep}).
				GeneratedSourceDirectory().
				Export(ctx, modConf.LocalRootPath)
			if err != nil {
				return fmt.Errorf("failed to generate code: %w", err)
			}
			return nil
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
			modConf, err := getDefaultModuleConfiguration(ctx, dag)
			if err != nil {
				return fmt.Errorf("failed to get configured module: %w", err)
			}
			_, err = modConf.Mod.GeneratedSourceDirectory().Export(ctx, modConf.LocalRootPath)
			if err != nil {
				return fmt.Errorf("failed to generate code: %w", err)
			}

			return nil
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
			modConf, err := getDefaultModuleConfiguration(ctx, dag)
			if err != nil {
				return fmt.Errorf("failed to get configured module: %w", err)
			}
			repo, err := git.PlainOpenWithOptions(modConf.LocalSourcePath, &git.PlainOpenOptions{
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
			pathFromRoot, err := filepath.Rel(gitRoot, modConf.LocalSourcePath)
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

type configuredModule struct {
	Mod        *dagger.Module
	SourceKind dagger.ModuleSourceKind

	LocalRootPath   string
	LocalSourcePath string

	// whether the dagger.json in the module root dir exists yet
	ModuleRootConfigExists bool
	// whether the dagger.json in the module source dir exists yet
	ModuleSourceConfigExists bool
}

func getDefaultModuleConfiguration(ctx context.Context, dag *dagger.Client) (*configuredModule, error) {
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

	return getModuleConfigurationForSourceRef(ctx, dag, srcRefStr)
}

func getModuleConfigurationForSourceRef(ctx context.Context, dag *dagger.Client, srcRefStr string) (*configuredModule, error) {
	conf := &configuredModule{}

	// first check if this is a named module from the default find-up dagger.json
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
		if err := modCfg.Validate(); err != nil {
			return nil, fmt.Errorf("error validating %s: %s", configPath, err)
		}
		if namedDep, ok := modCfg.DependencyByName(srcRefStr); ok {
			srcRefStr = namedDep.Source
		}
	}

	src := dag.ModuleSource(srcRefStr)
	conf.SourceKind, err = src.Kind(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get module ref kind: %w", err)
	}

	if conf.SourceKind == dagger.LocalSource {
		conf.LocalSourcePath, err = filepath.Abs(srcRefStr)
		if err != nil {
			return nil, fmt.Errorf("failed to get absolute path for %s: %w", srcRefStr, err)
		}

		conf.LocalRootPath, conf.ModuleRootConfigExists, err = findUpRootFor(conf.LocalSourcePath, conf.LocalSourcePath)
		if err != nil {
			return nil, fmt.Errorf("error trying to find config path for %s: %s", conf.LocalSourcePath, err)
		}
		if !conf.ModuleRootConfigExists {
			// default to current workdir
			conf.LocalRootPath = cwd
		}

		src, conf.ModuleSourceConfigExists, err = loadLocalModuleSource(dag, conf.LocalRootPath, conf.LocalSourcePath)
		if err != nil {
			return nil, fmt.Errorf("error loading local module: %s", err)
		}
		conf.Mod = src.AsModule()
	} else {
		conf.Mod = src.AsModule()
		conf.ModuleSourceConfigExists = true
	}

	return conf, nil
}

// loadLocalModuleSource loads the module specified by the given paths.
func loadLocalModuleSource(
	dag *dagger.Client,
	sourceRootAbsPath string,
	sourceSubdirAbsPath string,
) (*dagger.ModuleSource, bool, error) {
	// reposition sourceSubdirAbsPath to be relative to configDirPath
	sourceSubdirRelPath, err := filepath.Rel(sourceRootAbsPath, sourceSubdirAbsPath)
	if err != nil {
		return nil, false, err
	}

	var include []string
	var exclude []string
	var exists bool
	configPath := filepath.Join(sourceSubdirAbsPath, modules.Filename)
	configBytes, err := os.ReadFile(configPath)
	switch {
	case err == nil:
		var modCfg modules.ModuleConfig
		if err := json.Unmarshal(configBytes, &modCfg); err != nil {
			return nil, false, fmt.Errorf("error unmarshaling %s: %s", configPath, err)
		}
		if err := modCfg.Validate(); err != nil {
			return nil, false, fmt.Errorf("error validating %s: %s", configPath, err)
		}

		// make sure this is the actual module's config and not an initialized config
		// containing only root-for entries
		if modCfg.Name != "" {
			include = modCfg.Include
			exclude = modCfg.Exclude
			exists = true
		}

	case os.IsNotExist(err):
		// config doesn't exist yet, load with no include/exclude

	default:
		return nil, false, fmt.Errorf("error reading config %s: %s", configPath, err)
	}

	return dag.ModuleSource(sourceSubdirRelPath, dagger.ModuleSourceOpts{
		RootDirectory: dag.Host().Directory(sourceRootAbsPath, dagger.HostDirectoryOpts{
			Include: include,
			Exclude: exclude,
		}),
	}), exists, nil
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

func findUpRootFor(curDirPath string, sourceSubpath string) (string, bool, error) {
	foundDir, found, err := findUp(curDirPath)
	if err != nil {
		return "", false, err
	}
	if !found {
		return "", false, nil
	}

	configPath := filepath.Join(foundDir, modules.Filename)
	contents, err := os.ReadFile(configPath)
	if err != nil {
		return "", false, fmt.Errorf("failed to read %s: %s", configPath, err)
	}
	var modCfg modules.ModuleConfig
	if err := json.Unmarshal(contents, &modCfg); err != nil {
		return "", false, fmt.Errorf("failed to unmarshal %s: %s", configPath, err)
	}
	if err := modCfg.Validate(); err != nil {
		return "", false, fmt.Errorf("error validating %s: %s", configPath, err)
	}

	sourceRelSubpath, err := filepath.Rel(curDirPath, sourceSubpath)
	if err != nil {
		return "", false, fmt.Errorf("failed to get relative path: %s", err)
	}
	if modCfg.IsRootFor(sourceRelSubpath) {
		return curDirPath, true, nil
	}
	return findUpRootFor(filepath.Dir(foundDir), sourceSubpath)
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

			modConf, err := getDefaultModuleConfiguration(ctx, engineClient.Dagger())
			if err != nil {
				return fmt.Errorf("failed to get configured module: %w", err)
			}
			var loadedMod *dagger.Module
			if modConf.ModuleSourceConfigExists {
				loadedMod = modConf.Mod.Initialize()
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
