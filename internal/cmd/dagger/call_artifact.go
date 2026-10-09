package daggercmd

import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger/core"

	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Selecting a collection item to call functions on.
//
// `dagger check` and its peers name an artifact with one flag per dimension
// (`--go-module=./app`). `call` builds a function pipeline instead, so it had
// no way to say which item it meant: reaching one meant walking the
// projection by hand, `projects get --key=./api suites get --key=unit.test.ts`.
// The same dimension flags now root the pipeline at the selected item:
//
//	dagger call --runner-project=./api --runner-suite=unit.test.ts file
//	=> { node(id: "<item>") { ... on RunnerSuite { file } } }
//
// Selectors go before the first function, where the root command's own flags
// go: the command tree past that point is built from the item's type, which
// is not known until the selection resolves.

// artifactRootSelection is the item a call pipeline starts from. Its fn is a
// synthetic root whose return type carries the item's functions; nothing is
// selected for it, since the node(id:) selection is already in place.
type artifactRootSelection struct {
	fn  *modFunction
	uri string
}

// rootFlagNames is every long flag the root command ends up accepting. Its own
// are registered already; a module's constructor arguments and `with`'s are
// not, since cobraBuilder adds them while building the tree. They have to be
// counted anyway, or `dagger call --source=. build` would look like a
// selection and make an ordinary call pay for a dimension load.
func (fc *FuncCommand) rootFlagNames(cmd *cobra.Command) map[string]bool {
	names := map[string]bool{}
	cmd.Flags().VisitAll(func(flag *pflag.Flag) { names[cliName(flag.Name)] = true })
	add := func(fn *modFunction) {
		if fn == nil {
			return
		}
		for _, arg := range fn.SupportedArgs() {
			names[cliName(arg.FlagName())] = true
		}
	}
	root := fc.mod.MainObject.AsObject.Constructor
	add(root)
	// `with` lives on the type the root command selects from, which is the
	// constructor's return type, or the main object when there is no
	// constructor to go through.
	rootType := fc.mod.MainObject
	if root != nil && root.ReturnType != nil {
		rootType = root.ReturnType
	}
	if fp := rootType.AsFunctionProvider(); fp != nil {
		for _, fn := range fp.GetFunctions() {
			if fn.Name == "with" {
				add(fn)
				break
			}
		}
	}
	return names
}

// hasUnknownRootFlag reports whether args carry a long flag the root command
// will not accept, before the first function name. Dimensions cost a round
// trip and expand every collection, so an ordinary call must not pay for them.
func (fc *FuncCommand) hasUnknownRootFlag(cmd *cobra.Command, args []string) bool {
	var known map[string]bool
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			return false
		case strings.HasPrefix(arg, "--"):
			name, _, hasValue := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
			if known == nil {
				known = fc.rootFlagNames(cmd)
			}
			if !known[cliName(name)] {
				return true
			}
			if flag := cmd.Flags().Lookup(name); !hasValue && (flag == nil || flag.NoOptDefVal == "") {
				i++
			}
		case strings.HasPrefix(arg, "-") && arg != "-":
			// A shorthand run such as -vv, or one taking a value: -m ./mod.
			shorthands := strings.TrimPrefix(arg, "-")
			last := cmd.Flags().ShorthandLookup(shorthands[len(shorthands)-1:])
			if last != nil && last.NoOptDefVal == "" && !strings.Contains(arg, "=") {
				i++
			}
		default:
			// The function chain starts here; later flags are its own.
			return false
		}
	}
	return false
}

// selectArtifactRoot roots the pipeline at the collection item named by
// dimension flags, if the command line names one.
func (fc *FuncCommand) selectArtifactRoot(ctx context.Context, cmd *cobra.Command, args []string) error {
	if fc.DisableModuleLoad || moduleNoURL || isCoreModuleSelected() {
		return nil
	}
	if !fc.hasUnknownRootFlag(cmd, args) {
		return nil
	}

	dag := fc.c.Dagger()
	ws := core.NewQuery(dag).CurrentWorkspace()
	all := ws.Artifacts()
	// A flag this command does not define is usually a mistake, not a
	// selection, so a workspace that cannot report dimensions must not turn
	// that mistake into an artifact error: leave it to the regular parse,
	// which names the flag.
	dimensions, err := artifactDimensions(ctx, dag, all)
	if err != nil {
		return nil //nolint:nilerr // deliberate: an unselectable workspace leaves the flag to the regular parse
	}
	selectors := artifactSelectorDimensions(dimensions)
	if len(selectors) == 0 {
		return nil
	}
	registerArtifactDimensionHelp(cmd, dimensions, selectors)
	if err := parseArtifactSelectorFlags(cmd, args); err != nil {
		return err
	}
	if len(artifactKeyFlags(cmd)) == 0 {
		// The unknown flag was not a selector. Leave it to the regular parse,
		// which reports it against the command the user was actually naming.
		return nil
	}

	target := artifactSelectionType(artifactKeyFlags(cmd), dimensions)
	if target == "" {
		// Only static dimensions were given (a module, say). Those narrow a
		// listing; they do not name an item to call functions on.
		return nil
	}

	// A type-keyed selector resolves through the shared artifact helpers, which
	// ask the command which types it selects.
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[artifactCallTarget] = target

	artifacts, err := commandArtifactsWithFlags(ctx, dag, ws, cmd, nil, false)
	if err != nil {
		return err
	}
	// Selectors also match what hangs off the item, such as a check defined on
	// it. The item named by the deepest selector is the one being called.
	artifacts = artifacts.FilterTypes([]string{target})
	// Count before evaluating: an ambiguous selection would otherwise run the
	// module code behind every candidate just to report that it is ambiguous.
	items, err := readListedArtifacts(ctx, dag, artifacts, false, true)
	if err != nil {
		return err
	}
	if len(items) != 1 {
		return artifactSelectionError(target, items)
	}

	results, err := evaluateArtifacts(ctx, dag, artifacts, true, 0)
	if err != nil {
		return err
	}
	if err := artifactResultErrors(results); err != nil {
		return err
	}
	if len(results) != 1 || results[0].Value == nil {
		return fmt.Errorf("artifact %s has no value to call functions on", items[0].URI)
	}
	value := results[0].Value

	typeDef := fc.mod.GetTypeDef(value.Type)
	if typeDef == nil {
		return fmt.Errorf("type %q of artifact %s is not in the loaded schema", value.Type, items[0].URI)
	}
	fc.q = fc.q.Select("node").Arg("id", value.ID).InlineFragment(value.Type)
	fc.artifactRoot = &artifactRootSelection{
		fn:  &modFunction{ReturnType: typeDef},
		uri: items[0].URI,
	}
	return nil
}

// parseArtifactSelectorFlags fills in the selector values now that the flags
// exist. It has to parse on the command's own set: artifactKeyFlags reads them
// back through Visit, which only reports flags set on the set that parsed them.
// Unknown flags are tolerated because the functions past the selection have not
// contributed theirs yet; PreRunE already turned interspersed parsing off, so
// this stops at the first function name.
func parseArtifactSelectorFlags(cmd *cobra.Command, args []string) error {
	unknown := cmd.FParseErrWhitelist.UnknownFlags
	cmd.FParseErrWhitelist.UnknownFlags = true
	defer func() { cmd.FParseErrWhitelist.UnknownFlags = unknown }()
	return cmd.ParseFlags(args)
}

// artifactSelectionType is the item type the selectors name: the one from the
// deepest collection dimension, since a nested selector narrows the outer one.
func artifactSelectionType(keys []dagaddress.Pair, dimensions artifact.Dimensions) string {
	target, depth := "", -1
	for _, key := range keys {
		for _, dim := range dimensions {
			if dim.Identifier != key.Dimension || dim.ItemType == "" {
				continue
			}
			if d := strings.Count(dim.Identifier, "/"); d > depth {
				target, depth = dim.ItemType, d
			}
		}
	}
	return target
}

func artifactSelectionError(target string, items []listedArtifact) error {
	if len(items) == 0 {
		return fmt.Errorf("no %s matches the given selectors; run 'dagger list' to see what is available", target)
	}
	uris := make([]string, 0, len(items))
	for _, item := range items {
		uris = append(uris, "  "+item.URI)
	}
	return fmt.Errorf("the given selectors match %d artifacts; add a selector to pick one of:\n%s",
		len(items), strings.Join(uris, "\n"))
}
