package daggercmd

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/dagaddress"
	telemetry "github.com/dagger/otel-go"
	"github.com/spf13/cobra"
	"mvdan.cc/sh/v3/syntax"
)

// Compare canonical keys, since dimension aliases depend on the selected scope.
func listedArtifactIDs(items []listedArtifact) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		addr, _ := dagaddress.Parse(item.URI)
		keys := slices.Clone(item.DimensionKeys)
		slices.SortFunc(keys, func(a, b struct{ Dimension, Key string }) int { return strings.Compare(a.Dimension, b.Dimension) })
		encoded, _ := json.Marshal(keys)
		ids = append(ids, addr.Path+string(encoded))
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

func listedArtifactKeys(items []listedArtifact) []dagaddress.Pair {
	var keys []dagaddress.Pair
	for _, item := range items {
		for _, key := range item.DimensionKeys {
			pair := dagaddress.Pair{Dimension: key.Dimension, Key: key.Key, HasKey: true}
			if !slices.Contains(keys, pair) {
				keys = append(keys, pair)
			}
		}
	}
	return keys
}

func listArtifactSelection(ctx context.Context, dag *dagger.Client, selection *dagger.Artifacts, cmd *cobra.Command) error {
	ctx, span := Tracer().Start(ctx, "list artifacts", telemetry.Encapsulate())
	defer span.End()
	absolute, _ := cmd.Flags().GetBool("absolute")
	all, _ := cmd.Flags().GetBool("all")
	items, err := readListedArtifacts(ctx, dag, selection, absolute)
	if err != nil {
		return err
	}
	// Keep explicit input filters even if they happen to include every current key.
	inputKeys, err := artifactKeyFlags(cmd)
	if err != nil {
		return err
	}
	filtered := len(inputKeys) > 0
	addresses, err := parseArtifactAddresses(cmd.Flags().Args())
	if err != nil {
		return err
	}
	for _, address := range addresses {
		filtered = filtered || len(address.Query) > 0
	}
	var allArtifacts, targets *dagger.Artifacts
	var defs artifact.Dimensions
	if len(listedArtifactKeys(items)) > 0 {
		// Resolve discovery once. Every formatting query must use this same set,
		// since currentWorkspace has a new identity on each call.
		id, err := dag.CurrentWorkspace().Artifacts().ID(ctx)
		if err != nil {
			return err
		}
		allArtifacts = dagger.Ref[*dagger.Artifacts](dag, id)
		defs, err = artifactDimensions(ctx, dag, allArtifacts)
		if err != nil {
			return err
		}
		targets, err = commandArtifactTargets(ctx, dag, cmd, allArtifacts)
		if err != nil {
			return err
		}
	}
	candidates := map[string][]string{}
	// Read only candidate paths and keys. Do not enumerate the whole workspace
	// just to decide whether a line can omit its path.
	matches := func(path string, keys []dagaddress.Pair, want []listedArtifact) bool {
		encoded, _ := json.Marshal(keys)
		cacheKey := path + string(encoded)
		if got, ok := candidates[cacheKey]; ok {
			return slices.Equal(got, listedArtifactIDs(want))
		}
		address, err := dagaddress.Parse(path)
		if err != nil {
			return false
		}
		address.Absolute = false
		address.Query = keys
		candidate := targets.FilterURI(address.String())
		got, err := readListedArtifacts(ctx, dag, candidate, false)
		if err != nil {
			return false
		}
		candidates[cacheKey] = listedArtifactIDs(got)
		return slices.Equal(candidates[cacheKey], listedArtifactIDs(want))
	}
	var paths []string
	groups := map[string][]listedArtifact{}
	for _, item := range items {
		addr, err := dagaddress.Parse(item.URI)
		if err != nil {
			return err
		}
		addr.Query = nil
		path := strings.TrimPrefix(addr.String(), "dag://")
		if _, exists := groups[path]; !exists {
			paths = append(paths, path)
		}
		groups[path] = append(groups[path], item)
	}
	slices.Sort(paths)
	var lines []commandListItem
	grouped := false
	for _, path := range paths {
		group := groups[path]
		rows := make([][]listedArtifact, 0, len(group))
		keys := listedArtifactKeys(group)
		var pathDefs artifact.Dimensions
		if len(keys) > 0 {
			addr, _ := dagaddress.Parse(path)
			pathDefs, err = artifactDimensions(ctx, dag, allArtifacts.FilterURI(addr.Path))
			if err != nil {
				return err
			}
		}
		if !all && len(group) > 1 && (!filtered && matches(path, nil, group) || matches(path, keys, group)) {
			rows = append(rows, group)
			grouped = true
		} else {
			for _, item := range group {
				rows = append(rows, []listedArtifact{item})
			}
		}
		// If the whole key set identifies this path, each complete key tuple does too.
		omitPath := !absolute && len(keys) > 0 && matches("", keys, group)
		for _, row := range rows {
			keys := listedArtifactKeys(row)
			if !all && !filtered && len(keys) > 0 && matches(path, nil, row) {
				keys = nil
			}
			rowPath, rowDefs := path, pathDefs
			if !absolute && len(keys) > 0 && (omitPath || matches("", keys, row)) {
				rowPath, rowDefs = "", defs
			}
			args, err := artifactListArguments(cmd, rowPath, keys, rowDefs)
			if err != nil {
				return err
			}
			lines = append(lines, commandListItem{Name: args, Comment: firstDescriptionLine(row[0].Description)})
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := writeCommandList(cmd.OutOrStdout(), lines); err != nil {
		return err
	}
	if grouped {
		_, err = fmt.Fprintln(cmd.ErrOrStderr(), "# Use --all to list each key combination.")
	}
	return err
}

func artifactListArguments(cmd *cobra.Command, path string, keys []dagaddress.Pair, defs artifact.Dimensions) (string, error) {
	var args []string
	if path != "" {
		quoted, err := quoteArtifactArgument(path)
		if err != nil {
			return "", err
		}
		args = append(args, quoted)
	}
	for _, key := range keys {
		name := key.Dimension
		for _, def := range defs {
			if def.Identifier == name {
				name = defs.DisplayName(def)
				break
			}
		}
		prefix := "--" + name + "="
		if flag := cmd.Flag(name); flag != nil && len(flag.Annotations[artifactDimensionFlag]) == 0 {
			prefix = "--dimension-key=" + name + "="
		}
		value, err := quoteArtifactArgument(key.Key)
		if err != nil {
			return "", err
		}
		args = append(args, prefix+value)
	}
	// Keep explicit check policy options when the line is copied into a new command.
	if cmd.Name() == "check" {
		for _, name := range []string{"generate", "no-generate"} {
			if cmd.Flags().Changed(name) {
				args = append(args, "--"+name+"="+cmd.Flag(name).Value.String())
			}
		}
		skip, _ := cmd.Flags().GetStringArray("skip")
		for _, pattern := range skip {
			quoted, err := quoteArtifactArgument(pattern)
			if err != nil {
				return "", err
			}
			args = append(args, "--skip="+quoted)
		}
	}
	return strings.Join(args, " "), nil
}

func quoteArtifactArgument(value string) (string, error) {
	// Quote also handles newlines, so every item stays on one physical line.
	quoted, err := syntax.Quote(value, syntax.LangBash)
	if err != nil {
		return "", err
	}
	// Protect interactive shell history expansion too.
	if strings.Contains(value, "!") && !strings.HasPrefix(quoted, "$'") {
		quoted = "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
	}
	return quoted, nil
}
