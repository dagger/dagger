package main

import (
	"context"

	"dagger/test/internal/dagger"
	"github.com/Khan/genqlient/graphql"
)

type Test struct{}

func (m *Test) GetDepSource(ctx context.Context, src *dagger.Directory) (*dagger.Directory, error) {
	err := src.AsModule(dagger.DirectoryAsModuleOpts{SourceRootPath: "dep"}).Serve(ctx)
	if err != nil {
		return nil, err
	}

	type DirectoryIDRes struct {
		Dep struct {
			GetSource struct {
				ID string
			}
		}
	}

	directoryIDRes := &DirectoryIDRes{}
	res := &graphql.Response{Data: directoryIDRes}

	err = dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
		Query: "{dep {getSource {id} } }",
	}, res)
	if err != nil {
		return nil, err
	}

	return dag.LoadDirectoryFromID(dagger.DirectoryID(directoryIDRes.Dep.GetSource.ID)), nil
}

func (m *Test) GetRelDepSource(ctx context.Context, src *dagger.Directory) (*dagger.Directory, error) {
	err := src.AsModule(dagger.DirectoryAsModuleOpts{SourceRootPath: "dep"}).Serve(ctx)
	if err != nil {
		return nil, err
	}

	type DirectoryIDRes struct {
		Dep struct {
			GetRelSource struct {
				ID string
			}
		}
	}

	directoryIDRes := &DirectoryIDRes{}
	res := &graphql.Response{Data: directoryIDRes}

	err = dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
		Query: "{dep {getRelSource {id} } }",
	}, res)
	if err != nil {
		return nil, err
	}

	return dag.LoadDirectoryFromID(dagger.DirectoryID(directoryIDRes.Dep.GetRelSource.ID)), nil
}
