package main

import (
	"bytes"
	"testing"

	"dagger.io/dagger"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestDiscoverCollectionFilterFlags(t *testing.T) {
	def := &moduleDef{
		Objects: []*modTypeDef{
			{
				Kind:         dagger.TypeDefKindObjectKind,
				AsCollection: &modCollection{},
				AsObject:     &modObject{Name: "GoTests"},
			},
			{
				Kind:         dagger.TypeDefKindObjectKind,
				AsCollection: &modCollection{},
				AsObject:     &modObject{Name: "GoModules"},
			},
			{
				Kind:     dagger.TypeDefKindObjectKind,
				AsObject: &modObject{Name: "GoTest"},
			},
		},
	}

	flags := discoverCollectionFilterFlags(def)
	require.Equal(t, []collectionFilterFlag{
		{TypeName: "GoModules", FlagName: "go-modules", ListName: "list-go-modules"},
		{TypeName: "GoTests", FlagName: "go-tests", ListName: "list-go-tests"},
	}, flags)
}

func TestActiveCollectionFilters(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	filters := []collectionFilterFlag{
		{TypeName: "GoModules", FlagName: "go-modules", ListName: "list-go-modules"},
		{TypeName: "GoTests", FlagName: "go-tests", ListName: "list-go-tests"},
	}
	addCollectionFilterFlags(cmd, filters)

	err := cmd.ParseFlags([]string{
		"--go-tests=TestFoo,TestBar",
		"--go-tests=TestBaz",
		"--go-modules=./app",
		"--list-go-tests",
	})
	require.NoError(t, err)

	active, lists, err := activeCollectionFilters(cmd, filters)
	require.NoError(t, err)
	require.Equal(t, []dagger.CollectionFilterInput{
		{TypeName: "GoModules", Values: []string{"./app"}},
		{TypeName: "GoTests", Values: []string{"TestFoo", "TestBar", "TestBaz"}},
	}, active)
	require.Equal(t, []collectionFilterFlag{
		{TypeName: "GoTests", FlagName: "go-tests", ListName: "list-go-tests"},
	}, lists)
}

func TestPrintCollectionFilterValues(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)

	printCollectionFilterValues(cmd, []collectionFilterValuesItem{
		{TypeName: "GoModules", Values: []string{"./app", "./lib"}},
		{TypeName: "GoTests", Values: []string{"TestFoo"}},
	})

	require.Equal(t, "go-modules:\n./app\n./lib\n\ngo-tests:\nTestFoo\n", buf.String())
}
