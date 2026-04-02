package main

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"dagger.io/dagger"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type collectionFilterFlag struct {
	TypeName string
	FlagName string
	ListName string
}

type collectionFilterValuesItem struct {
	TypeName string
	Values   []string
}

func discoverCollectionFilterFlags(def *moduleDef) []collectionFilterFlag {
	flags := make([]collectionFilterFlag, 0, len(def.Objects))
	for _, typeDef := range def.Objects {
		if typeDef.AsCollection == nil || typeDef.AsObject == nil {
			continue
		}
		flagName := cliName(typeDef.AsObject.Name)
		flags = append(flags, collectionFilterFlag{
			TypeName: typeDef.AsObject.Name,
			FlagName: flagName,
			ListName: "list-" + flagName,
		})
	}
	sort.Slice(flags, func(i, j int) bool {
		return flags[i].FlagName < flags[j].FlagName
	})
	return flags
}

func addCollectionFilterFlags(cmd *cobra.Command, filters []collectionFilterFlag) {
	for _, filter := range filters {
		if cmd.Flags().Lookup(filter.FlagName) == nil {
			cmd.Flags().StringSlice(filter.FlagName, nil, fmt.Sprintf("Restrict %s collections to the specified keys", filter.TypeName))
		}
		if cmd.Flags().Lookup(filter.ListName) == nil {
			cmd.Flags().Bool(filter.ListName, false, fmt.Sprintf("List available %s filter values within the current scope", filter.TypeName))
		}
	}
}

func activeCollectionFilters(cmd *cobra.Command, filters []collectionFilterFlag) ([]dagger.CollectionFilterInput, []collectionFilterFlag, error) {
	active := make([]dagger.CollectionFilterInput, 0, len(filters))
	lists := make([]collectionFilterFlag, 0, len(filters))

	for _, filter := range filters {
		if cmd.Flags().Changed(filter.FlagName) {
			values, err := cmd.Flags().GetStringSlice(filter.FlagName)
			if err != nil {
				return nil, nil, err
			}
			active = append(active, dagger.CollectionFilterInput{
				TypeName: filter.TypeName,
				Values:   values,
			})
		}
		listEnabled, err := cmd.Flags().GetBool(filter.ListName)
		if err != nil {
			return nil, nil, err
		}
		if listEnabled {
			lists = append(lists, filter)
		}
	}

	return active, lists, nil
}

func firstActiveCollectionFilterFlag(cmd *cobra.Command, filters []collectionFilterFlag) (string, bool) {
	for _, filter := range filters {
		if cmd.Flags().Changed(filter.FlagName) {
			return filter.FlagName, true
		}
	}
	return "", false
}

func collectionFilterTypeNames(filters []collectionFilterFlag) []string {
	typeNames := make([]string, 0, len(filters))
	for _, filter := range filters {
		typeNames = append(typeNames, filter.TypeName)
	}
	return typeNames
}

func printCollectionFilterValues(cmd *cobra.Command, values []collectionFilterValuesItem) {
	for i, valueSet := range values {
		if i > 0 {
			fmt.Fprintln(cmd.OutOrStdout())
		}
		if len(values) > 1 {
			fmt.Fprintf(cmd.OutOrStdout(), "%s:\n", cliName(valueSet.TypeName))
		}
		for _, value := range valueSet.Values {
			fmt.Fprintln(cmd.OutOrStdout(), value)
		}
	}
}

func loadCheckCollectionFilterValues(ctx context.Context, dag *dagger.Client, checkGroup *dagger.CheckGroup, typeNames []string) ([]collectionFilterValuesItem, error) {
	id, err := checkGroup.ID(ctx)
	if err != nil {
		return nil, err
	}

	var res struct {
		Group struct {
			CollectionFilterValues []collectionFilterValuesItem
		}
	}

	err = dag.Do(ctx, &dagger.Request{
		Query:  loadChecksQuery,
		OpName: "CheckGroupCollectionFilterValues",
		Variables: map[string]any{
			"id":        id,
			"typeNames": typeNames,
		},
	}, &dagger.Response{
		Data: &res,
	})
	if err != nil {
		return nil, err
	}

	return res.Group.CollectionFilterValues, nil
}

func loadGeneratorCollectionFilterValues(ctx context.Context, dag *dagger.Client, generatorGroup *dagger.GeneratorGroup, typeNames []string) ([]collectionFilterValuesItem, error) {
	id, err := generatorGroup.ID(ctx)
	if err != nil {
		return nil, err
	}

	var res struct {
		Group struct {
			CollectionFilterValues []collectionFilterValuesItem
		}
	}

	err = dag.Do(ctx, &dagger.Request{
		Query:  loadGeneratorsQuery,
		OpName: "GeneratorGroupCollectionFilterValues",
		Variables: map[string]any{
			"id":        id,
			"typeNames": typeNames,
		},
	}, &dagger.Response{
		Data: &res,
	})
	if err != nil {
		return nil, err
	}

	return res.Group.CollectionFilterValues, nil
}

func preparseDynamicFlags(cmd *cobra.Command, args []string, needsHelp *bool) error {
	cmd.DisableFlagParsing = false

	help := pflag.NewFlagSet("help", pflag.ContinueOnError)
	if helpFlag := cmd.Flags().Lookup("help"); helpFlag != nil {
		help.AddFlag(helpFlag)
	}
	help.ParseErrorsAllowlist.UnknownFlags = true
	help.ParseAll(args, func(flag *pflag.Flag, value string) error {
		*needsHelp = value == flag.NoOptDefVal
		return nil
	})

	cmd.FParseErrWhitelist.UnknownFlags = true
	if err := cmd.ParseFlags(args); err != nil && !errors.Is(err, pflag.ErrHelp) {
		return cmd.FlagErrorFunc()(cmd, err)
	}
	cmd.FParseErrWhitelist.UnknownFlags = false
	return nil
}
