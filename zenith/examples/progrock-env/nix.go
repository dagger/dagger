package main

import (
	"bytes"
	_ "embed"
	"strings"
	"text/template"

	"dagger.io/dagger"
)

func Nixpkgs(ctx dagger.Context, flake *dagger.Directory, packages ...string) *dagger.Container {
	imageRef := "nixpkgs/" + strings.Join(packages, "/")
	drv := NixDerivation(ctx, "/flake", imageRef, packages...)

	build :=
		NixBase(ctx).
			WithExec([]string{"nix", "profile", "install", "nixpkgs#skopeo"}).
			WithMountedDirectory("/src", drv).
			WithMountedDirectory("/flake", flake).
			WithMountedTemp("/tmp").
			// TODO: --option filter-syscalls false to let Apple Silicon
			// cross-compile to Intel
			WithExec([]string{"nix", "build", "-f", "/src/image.nix"}).
			WithExec([]string{
				"skopeo", "--insecure-policy",
				"copy", "docker-archive:./result", "oci:./layout:latest",
			})

	return ctx.Client().Container().
		ImportDir(build.Directory("./layout")).
		WithMountedTemp("/tmp")
}

func NixBase(ctx dagger.Context) *dagger.Container {
	c := ctx.Client()

	base := c.Container().From("nixos/nix")

	return base.
		WithMountedCache(
			"/nix",
			c.CacheVolume("nix"),
			dagger.ContainerWithMountedCacheOpts{
				Source: base.Directory("/nix"),
			}).
		WithExec([]string{"sh", "-c", "echo accept-flake-config = true >> /etc/nix/nix.conf"}).
		WithExec([]string{"sh", "-c", "echo experimental-features = nix-command flakes >> /etc/nix/nix.conf"})
}

func nixResult(ctx dagger.Context, ctr *dagger.Container) *dagger.File {
	return ctr.
		WithExec([]string{"cp", "-aL", "./result", "./exported"}).
		File("./exported")
}

//go:embed image.nix.tmpl
var imageNixSrc string

var imageNixTmpl *template.Template

func init() {
	imageNixTmpl = template.Must(template.New("image.nix.tmpl").Parse(imageNixSrc))
}

func NixDerivation(ctx dagger.Context, flakeRef, name string, packages ...string) *dagger.Directory {
	w := new(bytes.Buffer)
	err := imageNixTmpl.Execute(w, struct {
		FlakeRef string
		Name     string
		Packages []string
	}{
		FlakeRef: flakeRef,
		Name:     name,
		Packages: packages,
	})
	if err != nil {
		panic(err)
	}

	return ctx.Client().Directory().WithNewFile("image.nix", w.String())
}
