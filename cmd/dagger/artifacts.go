package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// artifactTypeDimension mirrors core.ArtifactTypeDimension. cmd/dagger must
// build for non-Linux targets and therefore cannot import the engine.
const artifactTypeDimension = "type"

var artifactListCmd = &cobra.Command{
	Use:                "list <dimension> [--<dimension>=<value>...]",
	Short:              "List workspace artifact dimension values",
	DisableFlagParsing: true,
	Long: `List values for an artifact dimension.

Use dimension filters (e.g. --go-module=./app) to narrow the artifact scope
before listing values. The built-in "types" dimension is an alias for "type".`,
	Example: "dagger list types\n  dagger list go-test --go-module=./app",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Flag parsing is disabled to accept dynamic dimension filters, so
		// collect every flag this command would normally understand and
		// resolve those by hand before treating flags as filters.
		knownFlags := pflag.NewFlagSet(cmd.Name(), pflag.ContinueOnError)
		knownFlags.AddFlagSet(cmd.Flags())
		knownFlags.AddFlagSet(cmd.InheritedFlags())
		dimension, filters, help, err := parseArtifactListArgs(args, knownFlags)
		if err != nil {
			return err
		}
		if help {
			return cmd.Help()
		}
		// Dimension names and type values are statically derivable from
		// typedefs: don't run module code for them.
		static := dimension == "" ||
			(normalizeArtifactDimensionName(dimension) == artifactTypeDimension && len(filters) == 0)
		return withEngine(
			cmd.Context(),
			client.Params{LoadWorkspaceModules: true},
			func(ctx context.Context, engineClient *client.Client) error {
				scope, err := loadArtifactScope(ctx, engineClient.Dagger(), filters, !static)
				if err != nil {
					return err
				}
				if dimension == "" {
					names := make([]string, 0, len(scope.Dimensions))
					for _, dim := range scope.Dimensions {
						names = append(names, displayArtifactDimensionName(dim.Name))
					}
					return writeArtifactDimensionValues(cmd.OutOrStdout(), names)
				}
				values, err := artifactDimensionValues(scope, dimension)
				if err != nil {
					return err
				}
				if static {
					// Without item rows, collection item types are still
					// statically known: every collection dimension is also a
					// type value.
					values = appendArtifactCollectionTypes(values, scope.Dimensions)
				}
				return writeArtifactDimensionValues(cmd.OutOrStdout(), values)
			},
		)
	},
}

type artifactListScope struct {
	Dimensions []artifactListDimension `json:"dimensions"`
	Items      []artifactListItem      `json:"items"`
}

type artifactListQueryArtifacts struct {
	Dimensions []artifactListDimension     `json:"dimensions"`
	Items      []artifactListItem          `json:"items"`
	Selected   *artifactListQueryArtifacts `json:"selected"`
}

type artifactListDimension struct {
	Name string `json:"name"`
}

type artifactListItem struct {
	Coordinates []*string `json:"coordinates"`
}

type artifactListFilter struct {
	Dimension string
	Values    []string
}

func parseArtifactListArgs(args []string, knownFlags *pflag.FlagSet) (string, []artifactListFilter, bool, error) {
	positionals, filters, help, err := parseDynamicFilterArgs(args, knownFlags)
	if err != nil || help {
		return "", nil, help, err
	}
	var dimension string
	for _, arg := range positionals {
		if dimension != "" {
			return "", nil, false, fmt.Errorf("expected exactly one artifact dimension, got %q and %q", dimension, arg)
		}
		dimension = arg
	}
	return dimension, filters, false, nil
}

// parseDynamicFilterArgs parses arguments for a command that disables cobra
// flag parsing in order to accept dynamic artifact dimension filters.
// Registered flags (own and inherited, including shorthands like -W and -m)
// are applied; any other --name[=value] flag is collected as a dimension
// filter; bare arguments are returned as positionals.
//
// Disabling cobra parsing also disables cobra behavior that commands must
// reinstate by hand. Known so far — a command adopting this parser must:
//   - handle -h/--help itself (returned via the help result)
//   - call cmd.ValidateFlagGroups() after parsing, or MarkFlagsMutuallyExclusive
//     and friends silently stop being enforced
//   - rely on applyKnownFlag's NoOptDefVal handling so bool and count flags
//     (-v) never consume the next argument
//
// Both of the latter were found as CI failures, not review.
func parseDynamicFilterArgs(args []string, knownFlags *pflag.FlagSet) ([]string, []artifactListFilter, bool, error) {
	var positionals []string
	filtersByDimension := map[string][]string{}
	filterOrder := []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--help" || arg == "-h":
			return nil, nil, true, nil
		case arg == "--":
			return nil, nil, false, fmt.Errorf("unexpected argument %q", arg)
		case strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--"):
			// Shorthands can only belong to registered flags: dimension
			// filters always use long form.
			name, value, hasValue := strings.Cut(strings.TrimPrefix(arg, "-"), "=")
			if knownFlags != nil && len(name) == 1 {
				if flag := knownFlags.ShorthandLookup(name); flag != nil {
					var err error
					i, err = applyKnownFlag(knownFlags, flag, value, hasValue, args, i)
					if err != nil {
						return nil, nil, false, err
					}
					continue
				}
			}
			return nil, nil, false, fmt.Errorf("unknown shorthand flag: %q", arg)
		case strings.HasPrefix(arg, "--"):
			name, value, hasValue := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
			if name == "" {
				return nil, nil, false, fmt.Errorf("empty artifact filter flag")
			}
			// Registered flags (own and inherited, e.g. --mod, --progress)
			// are applied; anything else is a dimension filter.
			if knownFlags != nil {
				if flag := knownFlags.Lookup(name); flag != nil {
					var err error
					i, err = applyKnownFlag(knownFlags, flag, value, hasValue, args, i)
					if err != nil {
						return nil, nil, false, err
					}
					continue
				}
			}
			if !hasValue {
				if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
					return nil, nil, false, fmt.Errorf("artifact filter --%s requires a value", name)
				}
				i++
				value = args[i]
			}
			name = normalizeArtifactDimensionName(name)
			if _, ok := filtersByDimension[name]; !ok {
				filterOrder = append(filterOrder, name)
			}
			filtersByDimension[name] = append(filtersByDimension[name], value)
		default:
			positionals = append(positionals, arg)
		}
	}
	filters := make([]artifactListFilter, 0, len(filterOrder))
	for _, name := range filterOrder {
		filters = append(filters, artifactListFilter{
			Dimension: name,
			Values:    filtersByDimension[name],
		})
	}
	return positionals, filters, false, nil
}

// applyKnownFlag applies one occurrence of a registered flag, consuming the
// next argument as its value when needed, and returns the updated index.
// Flags with a no-option default (bools "true", counts "+1") never consume
// the next argument.
func applyKnownFlag(flags *pflag.FlagSet, flag *pflag.Flag, value string, hasValue bool, args []string, i int) (int, error) {
	if !hasValue {
		if flag.NoOptDefVal != "" {
			value = flag.NoOptDefVal
		} else {
			if i+1 >= len(args) {
				return i, fmt.Errorf("flag --%s requires a value", flag.Name)
			}
			i++
			value = args[i]
		}
	}
	return i, flags.Set(flag.Name, value)
}

func appendArtifactCollectionTypes(values []string, dimensions []artifactListDimension) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, dim := range dimensions {
		if dim.Name == artifactTypeDimension {
			continue
		}
		if _, ok := seen[dim.Name]; ok {
			continue
		}
		seen[dim.Name] = struct{}{}
		values = append(values, dim.Name)
	}
	return values
}

func loadArtifactScope(ctx context.Context, dag *dagger.Client, filters []artifactListFilter, enumerate bool) (artifactListScope, error) {
	const scopeSelection = "dimensions { name } items { coordinates }"

	body := scopeSelection
	varDefParts := []string{"$enumerate: Boolean!"}
	vars := map[string]any{"enumerate": enumerate}
	for i := len(filters) - 1; i >= 0; i-- {
		varName := fmt.Sprintf("values%d", i)
		varDefParts = append(varDefParts, fmt.Sprintf("$%s: [String!]!", varName))
		vars[varName] = filters[i].Values
		body = fmt.Sprintf(
			"selected: filterCoordinates(dimension: %q, values: $%s) { %s }",
			normalizeArtifactDimensionName(filters[i].Dimension),
			varName,
			body,
		)
	}
	varDefs := "(" + strings.Join(varDefParts, ", ") + ")"

	query := fmt.Sprintf(`query ArtifactList%s {
  currentWorkspace {
    artifacts(enumerate: $enumerate) {
      %s
    }
  }
}`, varDefs, body)

	var res struct {
		CurrentWorkspace struct {
			Artifacts artifactListQueryArtifacts `json:"artifacts"`
		} `json:"currentWorkspace"`
	}
	if err := dag.Do(ctx, &dagger.Request{
		Query:     query,
		Variables: vars,
	}, &dagger.Response{Data: &res}); err != nil {
		return artifactListScope{}, err
	}

	return artifactListFinalScope(res.CurrentWorkspace.Artifacts), nil
}

func artifactListFinalScope(artifacts artifactListQueryArtifacts) artifactListScope {
	for artifacts.Selected != nil {
		artifacts = *artifacts.Selected
	}
	return artifactListScope{
		Dimensions: artifacts.Dimensions,
		Items:      artifacts.Items,
	}
}

func artifactDimensionValues(scope artifactListScope, dimension string) ([]string, error) {
	dimName := normalizeArtifactDimensionName(dimension)
	dimIdx := -1
	for i, dim := range scope.Dimensions {
		if dim.Name == dimName {
			dimIdx = i
			break
		}
	}
	if dimIdx == -1 {
		return nil, fmt.Errorf("unknown artifact dimension %q (available: %s)", dimension, availableArtifactDimensions(scope.Dimensions))
	}

	seen := map[string]struct{}{}
	values := make([]string, 0, len(scope.Items))
	for _, item := range scope.Items {
		if dimIdx >= len(item.Coordinates) || item.Coordinates[dimIdx] == nil {
			continue
		}
		value := *item.Coordinates[dimIdx]
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values, nil
}

func normalizeArtifactDimensionName(name string) string {
	if name == "types" {
		return artifactTypeDimension
	}
	return name
}

func displayArtifactDimensionName(name string) string {
	if name == artifactTypeDimension {
		return "types"
	}
	return name
}

func availableArtifactDimensions(dimensions []artifactListDimension) string {
	if len(dimensions) == 0 {
		return "none"
	}
	names := make([]string, 0, len(dimensions))
	for _, dim := range dimensions {
		names = append(names, displayArtifactDimensionName(dim.Name))
	}
	return strings.Join(names, ", ")
}

func writeArtifactDimensionValues(w io.Writer, values []string) error {
	for _, value := range values {
		if _, err := fmt.Fprintln(w, value); err != nil {
			return err
		}
	}
	return nil
}
