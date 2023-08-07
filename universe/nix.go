package universe

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"strings"
	"text/template"

	"dagger.io/dagger"
)

func Nixpkgs(ctx Context, nixpkgs *dagger.Directory, packages ...string) *dagger.Container {
	imageRef := "nixpkgs/" + strings.Join(packages, "/")
	drv := nixDerivation(ctx, "/flake", imageRef, packages...)

	build :=
		nixBase(ctx).
			WithExec([]string{"nix", "profile", "install", "nixpkgs#skopeo"}).
			WithMountedDirectory("/src", drv).
			WithMountedDirectory("/flake", nixpkgs).
			WithMountedTemp("/tmp").
			WithFocus().
			// TODO: --option filter-syscalls false to let Apple Silicon
			// cross-compile to Intel
			WithExec([]string{"nix", "build", "-f", "/src/image.nix"}).
			WithExec([]string{
				"skopeo", "--insecure-policy",
				"copy", "docker-archive:./result", "oci:./layout:latest",
			}).
			WithoutFocus()

	return ctx.Client().Container().
		ImportDir(build.Directory("./layout")).
		WithMountedTemp("/tmp")
}

type Artifact struct {
	Name     string
	Platform string
	Build    any
}

type Environment struct {
	// Actions []dagger.Action
	// Shells     []dagger.Shell
	Artifacts []Artifact
	// Tests      []dagger.Test
	// Services   []dagger.Service
	// Sidecars   []dagger.Sidecar
	// Extensions []dagger.Extension
	// Resolvers  []dagger.Resolver
}

type flakeShow struct {
	// Apps maps platforms to named apps.
	Apps map[string]map[string]nameType `json:"apps"`

	//
	DevShells map[string]map[string]nameType `json:"devShells"`

	Packages map[string]map[string]nameType `json:"packages"`
}

type nameType struct {
	Type string `json:"type"`
	Name string `json:"app"`
}

func FlakeRef(ctx Context, ref string) (Environment, error) {
	base := nixBase(ctx)

	showJSON, err := base.
		WithExec([]string{"nix", "flake", "show", ref, "--json"}).
		Stdout(ctx)
	if err != nil {
		return Environment{}, err
	}

	var show flakeShow
	if err := json.Unmarshal([]byte(showJSON), &show); err != nil {
		return Environment{}, err
	}

	env := Environment{}
	for platform, pkgs := range show.Packages {
		for name, meta := range pkgs {
			if meta.Type != "derivation" {
				panic("TODO: what else is there?")
			}

			env.Artifacts = append(env.Artifacts, Artifact{
				Name:     name,
				Platform: platform,
				Build: func(ctx Context) (*dagger.Directory, error) {
					return base.
						WithExec([]string{"nix", "build", ref + "#" + name}).
						Directory("result"), nil
				},
			})
		}
	}

	return Environment{}, nil
}

func nixBase(ctx Context) *dagger.Container {
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

//go:embed image.nix.tmpl
var imageNixSrc string

var imageNixTmpl *template.Template

func init() {
	imageNixTmpl = template.Must(template.New("image.nix.tmpl").Parse(imageNixSrc))
}

func nixDerivation(ctx Context, flakeRef, name string, packages ...string) *dagger.Directory {
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
