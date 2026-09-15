package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct{}

func (z *Test) Fn(
	ctx context.Context,
	rand string,
	// +defaultPath="/"
	// +ignore=["**", "!analytics"]
	a *dagger.Directory,
	// +defaultPath="/"
	// +ignore=["**", "!auth"]
	b *dagger.Directory,
	// +defaultPath="/"
	// +ignore=["**", "!bin"]
	c *dagger.Directory,
	// +defaultPath="/"
	// +ignore=["**", "!cmd"]
	d *dagger.Directory,
	// +defaultPath="/"
	// +ignore=["**", "!core"]
	e *dagger.Directory,
	// +defaultPath="/"
	// +ignore=["**", "!dagql"]
	f *dagger.Directory,
	// +defaultPath="/"
	// +ignore=["**", "!docs"]
	g *dagger.Directory,
	// +defaultPath="/"
	// +ignore=["**", "!engine"]
	h *dagger.Directory,
	// +defaultPath="/"
	// +ignore=["**", "!evals"]
	i *dagger.Directory,
	// +defaultPath="/"
	// +ignore=["**", "!hack"]
	j *dagger.Directory,
	// +defaultPath="/"
	// +ignore=["**", "!helm"]
	k *dagger.Directory,
	// +defaultPath="/"
	// +ignore=["**", "!internal"]
	l *dagger.Directory,
	// +defaultPath="/"
	// +ignore=["**", "!modules"]
	m *dagger.Directory,
	// +defaultPath="/"
	// +ignore=["**", "!network"]
	n *dagger.Directory,
	// +defaultPath="/"
	// +ignore=["**", "!sdk"]
	o *dagger.Directory,
	// +defaultPath="/"
	// +ignore=["**", "!toolchains"]
	p *dagger.Directory,
	// +defaultPath="/"
	// +ignore=["**", "!util"]
	q *dagger.Directory,
) (string, error) {
	for _, dir := range []*dagger.Directory{a, b, c, d, e, f, g, h, i, j, k, l, m, n, o, p, q} {
		if _, err := dir.Entries(ctx); err != nil {
			return "", err
		}
	}
	return "woo", nil
}
