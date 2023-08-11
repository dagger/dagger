// Code generated by dagger. DO NOT EDIT.

package main

import (
	"context"

	. "dagger.io/dagger"
	"dagger.io/dagger/querybuilder"
	"github.com/Khan/genqlient/graphql"
)

func DaggerClient() *daggerClient {
	return &daggerClient{DefaultContext().Client()}
}

type daggerClient struct {
	*Client
}

type Democlient struct {
	Q *querybuilder.Selection
	C graphql.Client

	publish *string
}

func (r *Democlient) Publish(ctx context.Context, version string) (string, error) {
	if r.publish != nil {
		return *r.publish, nil
	}
	q := r.Q.Select("publish")
	q = q.Arg("version", version)

	var response string

	q = q.Bind(&response)
	return response, q.Execute(ctx, r.C)
}

func (r *Democlient) UnitTest() *EnvironmentCheck {
	q := r.Q.Select("unitTest")

	return &EnvironmentCheck{
		Q: q,
		C: r.C,
	}
}

type Demoserver struct {
	Q *querybuilder.Selection
	C graphql.Client

	publish *string
}

func (r *Demoserver) Container() *Container {
	q := r.Q.Select("container")

	return &Container{
		Q: q,
		C: r.C,
	}
}

func (r *Demoserver) Publish(ctx context.Context, version string) (string, error) {
	if r.publish != nil {
		return *r.publish, nil
	}
	q := r.Q.Select("publish")
	q = q.Arg("version", version)

	var response string

	q = q.Bind(&response)
	return response, q.Execute(ctx, r.C)
}

func (r *Demoserver) UnitTest() *EnvironmentCheck {
	q := r.Q.Select("unitTest")

	return &EnvironmentCheck{
		Q: q,
		C: r.C,
	}
}

func (r *daggerClient) Democlient() *Democlient {
	q := r.Q.Select("democlient")

	return &Democlient{
		Q: q,
		C: r.C,
	}
}

func (r *daggerClient) Demoserver() *Demoserver {
	q := r.Q.Select("demoserver")

	return &Demoserver{
		Q: q,
		C: r.C,
	}
}
