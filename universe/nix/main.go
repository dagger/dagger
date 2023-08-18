package main

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"strings"
)

func main() {
	dag.Environment().
		WithFunction(Pkgs).
		WithCheck(PkgsTest).
		Serve()
}

type PkgsOpts struct {
	NixpkgsRef string `doc:"Nixpkgs git ref to use for building packages"`
}

func Pkgs(packages []string, opts PkgsOpts) *Container {
	imageRef := "nixpkgs/" + strings.Join(packages, "/")

	ref := opts.NixpkgsRef
	if ref == "" {
		// NB: strong opinion, loosely held: default to unstable,
		// which is more likely what I want for simple hacking.
		// if unstable is too unstable for you, set a ref!
		ref = "nixos-unstable"
	}

	drv := nixDerivation(ref, imageRef, packages...)
	return dag.Container().Import(
		nixBase().
			WithMountedDirectory("/src", drv).
			WithMountedTemp("/tmp").
			// TODO: --option filter-syscalls false to let Apple Silicon
			// cross-compile to Intel
			WithExec([]string{"nix", "build", "-f", "/src/image.nix"}).
			// TODO: Container.file/Directory.file should follow symlinks
			WithExec([]string{
				"cp", "-L", "./result", "./layout.tar",
			}).
			File("./layout.tar"),
	)
}

func PkgsTest(ctx context.Context) error {
	pkgs := Pkgs([]string{"go_1_20"}, PkgsOpts{
		NixpkgsRef: "23.05",
	})

	out, err := pkgs.WithExec([]string{"go", "version"}).Stdout(ctx)
	if err != nil {
		return err
	}

	const expectedVersion = "1.20.4" // 23.05 version
	if !strings.Contains(out, "go version go"+expectedVersion) {
		return fmt.Errorf("expected go version %s, got %s", expectedVersion, out)
	}

	return nil
}

func nixBase() *Container {
	base := dag.Container().From("nixos/nix")
	return base.
		WithMountedCache("/nix", dag.CacheVolume("nix"), ContainerWithMountedCacheOpts{
			Source: base.Directory("/nix"),
		}).
		WithExec([]string{"sh", "-c", "echo accept-flake-config = true >> /etc/nix/nix.conf"}).
		WithExec([]string{"sh", "-c", "echo experimental-features = nix-command flakes >> /etc/nix/nix.conf"})
}

//go:embed image.nix.tmpl
var imageNixSrc string

var imageNixTmpl *template.Template

func init() {
	imageNixTmpl = template.Must(template.New("image.nix.tmpl").Parse(imageNixSrc))
}

func nixDerivation(flakeRef, name string, packages ...string) *Directory {
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

	return dag.Directory().WithNewFile("image.nix", w.String())
}
