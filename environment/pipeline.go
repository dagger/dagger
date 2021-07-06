package environment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"strings"

	"cuelang.org/go/cue"
	"github.com/docker/distribution/reference"
	bk "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	dockerfilebuilder "github.com/moby/buildkit/frontend/dockerfile/builder"
	"github.com/moby/buildkit/frontend/dockerfile/dockerfile2llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	bkpb "github.com/moby/buildkit/solver/pb"
	digest "github.com/opencontainers/go-digest"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"

	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/solver"
)

// An execution pipeline
type Pipeline struct {
	code     *compiler.Value
	name     string
	s        solver.Solver
	state    llb.State
	result   bkgw.Reference
	image    dockerfile2llb.Image
	computed *compiler.Value
}

func NewPipeline(code *compiler.Value, s solver.Solver) *Pipeline {
	return &Pipeline{
		code:     code,
		name:     code.Path().String(),
		s:        s,
		state:    llb.Scratch(),
		computed: compiler.NewValue(),
	}
}

func (p *Pipeline) WithCustomName(name string) *Pipeline {
	p.name = name
	return p
}

func (p *Pipeline) State() llb.State {
	return p.state
}

func (p *Pipeline) Result() (llb.State, error) {
	if p.result == nil {
		return llb.Scratch(), nil
	}
	return p.result.ToState()
}

func (p *Pipeline) FS() fs.FS {
	return solver.NewBuildkitFS(p.result)
}

func (p *Pipeline) ImageConfig() dockerfile2llb.Image {
	return p.image
}

func (p *Pipeline) Computed() *compiler.Value {
	return p.computed
}

func isComponent(v *compiler.Value) bool {
	return v.Lookup("#up").Exists()
}

func ops(code *compiler.Value) ([]*compiler.Value, error) {
	ops := []*compiler.Value{}
	// 1. attachment array
	if isComponent(code) {
		xops, err := code.Lookup("#up").List()
		if err != nil {
			return nil, err
		}
		// 'from' has an executable attached
		ops = append(ops, xops...)
		// 2. individual op
	} else if _, err := code.Lookup("do").String(); err == nil {
		ops = append(ops, code)
		// 3. op array
	} else if xops, err := code.List(); err == nil {
		ops = append(ops, xops...)
	} else {
		// 4. error
		source, err := code.Source()
		if err != nil {
			panic(err)
		}
		return nil, fmt.Errorf("not executable: %s", source)
	}
	return ops, nil
}

func Analyze(fn func(*compiler.Value) error, code *compiler.Value) error {
	ops, err := ops(code)
	if err != nil {
		return err
	}
	for _, op := range ops {
		if err := analyzeOp(fn, op); err != nil {
			return err
		}
	}
	return nil
}

func analyzeOp(fn func(*compiler.Value) error, op *compiler.Value) error {
	if err := fn(op); err != nil {
		return err
	}
	do, err := op.Lookup("do").String()
	if err != nil {
		return err
	}
	switch do {
	case "load", "copy":
		return Analyze(fn, op.Lookup("from"))
	case "exec":
		fields, err := op.Lookup("mount").Fields()
		if err != nil {
			return err
		}
		for _, mnt := range fields {
			if from := mnt.Value.Lookup("from"); from.Exists() {
				return Analyze(fn, from)
			}
		}
	}
	return nil
}

func (p *Pipeline) Run(ctx context.Context) error {
	ops, err := ops(p.code)
	if err != nil {
		return err
	}

	// Execute each operation in sequence
	for idx, op := range ops {
		// If op not concrete, interrupt without error.
		// This allows gradual resolution:
		//    compute what you can compute.. leave the rest incomplete.
		if err := op.IsConcreteR(); err != nil {
			log.
				Ctx(ctx).
				Warn().
				Str("original_cue_error", err.Error()).
				Int("op", idx).
				Msg("pipeline was partially executed because of missing inputs")
			return nil
		}
		p.state, err = p.doOp(ctx, op, p.state)
		if err != nil {
			return err
		}
		// Force a buildkit solve request at each operation,
		// so that errors map to the correct cue path.
		// FIXME: might as well change FS to make every operation
		// synchronous.
		p.result, err = p.s.Solve(ctx, p.state)
		if err != nil {
			return err
		}
	}

	return nil
}

func (p *Pipeline) doOp(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	do, err := op.Lookup("do").String()
	if err != nil {
		return st, err
	}
	// FIXME: make this more readable then promote to INFO
	//   we need a readable trace of what operations are executed.
	log.
		Ctx(ctx).
		Debug().
		Str("pipeline", p.name).
		Str("do", do).
		Msg("executing operation")
	switch do {
	case "copy":
		return p.Copy(ctx, op, st)
	case "exec":
		return p.Exec(ctx, op, st)
	case "export":
		return p.Export(ctx, op, st)
	case "docker-login":
		return p.DockerLogin(ctx, op, st)
	case "fetch-container":
		return p.FetchContainer(ctx, op, st)
	case "push-container":
		return p.PushContainer(ctx, op, st)
	case "fetch-git":
		return p.FetchGit(ctx, op, st)
	case "fetch-http":
		return p.FetchHTTP(ctx, op, st)
	case "local":
		return p.Local(ctx, op, st)
	case "load":
		return p.Load(ctx, op, st)
	case "workdir":
		return p.Workdir(ctx, op, st)
	case "subdir":
		return p.Subdir(ctx, op, st)
	case "docker-build":
		return p.DockerBuild(ctx, op, st)
	case "write-file":
		return p.WriteFile(ctx, op, st)
	case "mkdir":
		return p.Mkdir(ctx, op, st)
	default:
		return st, fmt.Errorf("invalid operation: %s", op.JSON())
	}
}

func (p *Pipeline) vertexNamef(format string, a ...interface{}) string {
	prefix := fmt.Sprintf("@%s@", p.name)
	name := fmt.Sprintf(format, a...)
	return prefix + " " + name
}

func (p *Pipeline) Workdir(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	path, err := op.Lookup("path").String()
	if err != nil {
		return st, err
	}
	return st.Dir(path), nil
}

func (p *Pipeline) Subdir(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	// FIXME: this could be more optimized by carrying subdir path as metadata,
	//  and using it in copy, load or mount.

	dir, err := op.Lookup("dir").String()
	if err != nil {
		return st, err
	}
	return llb.Scratch().File(
		llb.Copy(
			st,
			dir,
			"/",
			&llb.CopyInfo{
				CopyDirContentsOnly: true,
			},
		),
		llb.WithCustomName(p.vertexNamef("Subdir %s", dir)),
	), nil
}

func (p *Pipeline) Copy(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	// Decode copy options
	src, err := op.Lookup("src").String()
	if err != nil {
		return st, err
	}
	dest, err := op.Lookup("dest").String()
	if err != nil {
		return st, err
	}
	// Execute 'from' in a tmp pipeline, and use the resulting fs
	from := NewPipeline(op.Lookup("from"), p.s)
	if err := from.Run(ctx); err != nil {
		return st, err
	}
	return st.File(
		llb.Copy(
			from.State(),
			src,
			dest,
			// FIXME: allow more configurable llb options
			// For now we define the following convenience presets:
			&llb.CopyInfo{
				CopyDirContentsOnly: true,
				CreateDestPath:      true,
				AllowWildcard:       true,
			},
		),
		llb.WithCustomName(p.vertexNamef("Copy %s %s", src, dest)),
	), nil
}

func (p *Pipeline) Local(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	dir, err := op.Lookup("dir").String()
	if err != nil {
		return st, err
	}

	opts := []llb.LocalOption{
		llb.WithCustomName(p.vertexNamef("Local %s", dir)),
		// Without hint, multiple `llb.Local` operations on the
		// same path get a different digest.
		llb.SessionID(p.s.SessionID()),
		llb.SharedKeyHint(dir),
	}

	includes, err := op.Lookup("include").List()
	if err != nil {
		return st, err
	}
	if len(includes) > 0 {
		includePatterns := []string{}
		for _, i := range includes {
			pattern, err := i.String()
			if err != nil {
				return st, err
			}
			includePatterns = append(includePatterns, pattern)
		}
		opts = append(opts, llb.IncludePatterns(includePatterns))
	}

	excludes, err := op.Lookup("exclude").List()
	if err != nil {
		return st, err
	}

	// Excludes .dagger directory by default
	excludePatterns := []string{"**/.dagger/"}
	if len(excludes) > 0 {
		for _, i := range excludes {
			pattern, err := i.String()
			if err != nil {
				return st, err
			}
			excludePatterns = append(excludePatterns, pattern)
		}
	}
	opts = append(opts, llb.ExcludePatterns(excludePatterns))

	// FIXME: Remove the `Copy` and use `Local` directly.
	//
	// Copy'ing is a costly operation which should be unnecessary.
	// However, using llb.Local directly breaks caching sometimes for unknown reasons.
	return st.File(
		llb.Copy(
			llb.Local(
				dir,
				opts...,
			),
			"/",
			"/",
		),
		llb.WithCustomName(p.vertexNamef("Local %s [copy]", dir)),
	), nil
}

func (p *Pipeline) Exec(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	opts := []llb.RunOption{}
	var cmd struct {
		Args   []string
		Dir    string
		Always bool
	}

	if err := op.Decode(&cmd); err != nil {
		return st, err
	}
	// args
	opts = append(opts, llb.Args(cmd.Args))
	// dir
	opts = append(opts, llb.Dir(cmd.Dir))

	// env
	if env := op.Lookup("env"); env.Exists() {
		envs, err := op.Lookup("env").Fields()
		if err != nil {
			return st, err
		}
		for _, env := range envs {
			v, err := env.Value.String()
			if err != nil {
				return st, err
			}
			opts = append(opts, llb.AddEnv(env.Label(), v))
		}
	}

	// always?
	if cmd.Always {
		// FIXME: also disables persistent cache directories
		// There's an ongoing proposal that would fix this: https://github.com/moby/buildkit/issues/1213
		opts = append(opts, llb.IgnoreCache)
	}

	if hosts := op.Lookup("hosts"); hosts.Exists() {
		fields, err := hosts.Fields()
		if err != nil {
			return st, err
		}
		for _, host := range fields {
			s, err := host.Value.String()
			if err != nil {
				return st, err
			}

			if err != nil {
				return st, err
			}
			opts = append(opts, llb.AddExtraHost(host.Label(), net.ParseIP(s)))
		}
	}

	if user := op.Lookup("user"); user.Exists() {
		u, err := user.String()
		if err != nil {
			return st, err
		}
		opts = append(opts, llb.User(u))
	}

	// mounts
	if mounts := op.Lookup("mount"); mounts.Exists() {
		mntOpts, err := p.mountAll(ctx, mounts)
		if err != nil {
			return st, err
		}
		opts = append(opts, mntOpts...)
	}

	// marker for status events
	// FIXME
	args := make([]string, 0, len(cmd.Args))
	for _, a := range cmd.Args {
		args = append(args, fmt.Sprintf("%q", a))
	}
	opts = append(opts, llb.WithCustomName(p.vertexNamef("Exec [%s]", strings.Join(args, ", "))))

	// --> Execute
	return st.Run(opts...).Root(), nil
}

func (p *Pipeline) mountAll(ctx context.Context, mounts *compiler.Value) ([]llb.RunOption, error) {
	opts := []llb.RunOption{}
	fields, err := mounts.Fields()
	if err != nil {
		return nil, err
	}
	for _, mnt := range fields {
		o, err := p.mount(ctx, mnt.Label(), mnt.Value)
		if err != nil {
			return nil, err
		}
		opts = append(opts, o)
	}
	return opts, err
}

func (p *Pipeline) mount(ctx context.Context, dest string, mnt *compiler.Value) (llb.RunOption, error) {
	if s, err := mnt.String(); err == nil {
		// eg. mount: "/foo": "cache"
		switch s {
		case "cache":
			return llb.AddMount(
				dest,
				llb.Scratch().File(
					llb.Mkdir("/cache", fs.FileMode(0755)),
					llb.WithCustomName(p.vertexNamef("Mkdir /cache (cache mount %s)", dest)),
				),
				// FIXME: disabled persistent cache mount (gh issue #495)
				// llb.Scratch(),
				// llb.AsPersistentCacheDir(
				// 	p.canonicalPath(mnt),
				// 	llb.CacheMountShared,
				// ),
			), nil
		case "tmpfs":
			return llb.AddMount(
				dest,
				llb.Scratch(),
				llb.Tmpfs(),
			), nil
		default:
			return nil, fmt.Errorf("invalid mount source: %q", s)
		}
	}
	// eg. mount: "/foo": secret: mysecret
	if secret := mnt.Lookup("secret"); secret.Exists() {
		if !secret.HasAttr("secret") {
			return nil, fmt.Errorf("invalid secret %q: not a secret", secret.Path().String())
		}
		idValue := secret.Lookup("id")
		if !idValue.Exists() {
			return nil, fmt.Errorf("invalid secret %q: no id field", secret.Path().String())
		}
		id, err := idValue.String()
		if err != nil {
			return nil, fmt.Errorf("invalid secret id: %w", err)
		}
		return llb.AddSecret(dest,
			llb.SecretID(id),
			llb.SecretFileOpt(0, 0, 0400), // uid, gid, mask)
		), nil
	}

	// eg. mount: "/foo": { from: www.source }
	from := NewPipeline(mnt.Lookup("from"), p.s)
	if err := from.Run(ctx); err != nil {
		return nil, err
	}
	// possibly construct mount options for LLB from
	var mo []llb.MountOption
	// handle "path" option
	if mp := mnt.Lookup("path"); mp.Exists() {
		mps, err := mp.String()
		if err != nil {
			return nil, err
		}
		mo = append(mo, llb.SourcePath(mps))
	}
	return llb.AddMount(dest, from.State(), mo...), nil
}

// canonicalPath returns the canonical path of `v`
// If the pipeline is a reference to another pipeline, `canonicalPath()` will
// return the path of the reference of `v`.
// FIXME: this doesn't work with references of references.
func (p *Pipeline) canonicalPath(v *compiler.Value) string {
	// value path
	vPath := v.Path().Selectors()

	// pipeline path
	pipelinePath := p.code.Path().Selectors()

	// check if the pipeline is a reference
	_, ref := p.code.ReferencePath()
	if len(ref.Selectors()) == 0 {
		return v.Path().String()
	}
	canonicalPipelinePath := ref.Selectors()

	// replace the pipeline path with the canonical pipeline path
	// 1. strip the pipeline path from the value path
	vPath = vPath[len(pipelinePath):]
	// 2. inject the canonical pipeline path
	vPath = append(canonicalPipelinePath, vPath...)

	return cue.MakePath(vPath...).String()
}

func (p *Pipeline) Export(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	source, err := op.Lookup("source").String()
	if err != nil {
		return st, err
	}
	format, err := op.Lookup("format").String()
	if err != nil {
		return st, err
	}
	contents, err := fs.ReadFile(p.FS(), source)
	if err != nil {
		return st, fmt.Errorf("export %s: %w", source, err)
	}
	switch format {
	case "string":
		log.
			Ctx(ctx).
			Debug().
			Bytes("contents", contents).
			Msg("exporting string")

		if err := p.computed.FillPath(cue.MakePath(), string(contents)); err != nil {
			return st, err
		}
	case "json":
		var o interface{}
		o, err := unmarshalAnything(contents, json.Unmarshal)
		if err != nil {
			return st, err
		}

		log.
			Ctx(ctx).
			Debug().
			Interface("contents", o).
			Msg("exporting json")

		if err := p.computed.FillPath(cue.MakePath(), o); err != nil {
			return st, err
		}
	case "yaml":
		var o interface{}
		o, err := unmarshalAnything(contents, yaml.Unmarshal)
		if err != nil {
			return st, err
		}

		log.
			Ctx(ctx).
			Debug().
			Interface("contents", o).
			Msg("exporting yaml")

		if err := p.computed.FillPath(cue.MakePath(), o); err != nil {
			return st, err
		}
	default:
		return st, fmt.Errorf("unsupported export format: %q", format)
	}
	return st, nil
}

type unmarshaller func([]byte, interface{}) error

func unmarshalAnything(data []byte, fn unmarshaller) (interface{}, error) {
	// unmarshalling a map into interface{} yields an error:
	// "unsupported Go type for map key (interface {})"
	// we want to attempt to unmarshal to a map[string]interface{} first
	var oMap map[string]interface{}
	if err := fn(data, &oMap); err == nil {
		return oMap, nil
	}

	// If the previous attempt didn't work, we might be facing a scalar (e.g.
	// bool).
	// Try to unmarshal to interface{} directly.
	var o interface{}
	err := fn(data, &o)
	return o, err
}

func (p *Pipeline) Load(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	// Execute 'from' in a tmp pipeline, and use the resulting fs
	from := NewPipeline(op.Lookup("from"), p.s)
	if err := from.Run(ctx); err != nil {
		return st, err
	}
	p.image = from.ImageConfig()
	return from.State(), nil
}

func (p *Pipeline) DockerLogin(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	username, err := op.Lookup("username").String()
	if err != nil {
		return st, err
	}

	secret, err := op.Lookup("secret").String()
	if err != nil {
		return st, err
	}

	target, err := op.Lookup("target").String()
	if err != nil {
		return st, err
	}

	p.s.AddCredentials(target, username, secret)
	log.
		Ctx(ctx).
		Debug().
		Str("target", target).
		Msg("docker login to registry")

	return st, nil
}

func (p *Pipeline) FetchContainer(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	rawRef, err := op.Lookup("ref").String()
	if err != nil {
		return st, err
	}

	ref, err := reference.ParseNormalizedNamed(rawRef)
	if err != nil {
		return st, fmt.Errorf("failed to parse ref %s: %w", rawRef, err)
	}
	// Add the default tag "latest" to a reference if it only has a repo name.
	ref = reference.TagNameOnly(ref)

	st = llb.Image(
		ref.String(),
		llb.WithCustomName(p.vertexNamef("FetchContainer %s", rawRef)),
	)

	// Load image metadata and convert to to LLB.
	p.image, err = p.s.ResolveImageConfig(ctx, ref.String(), llb.ResolveImageConfigOpt{
		LogName: p.vertexNamef("load metadata for %s", ref.String()),
	})
	if err != nil {
		return st, err
	}

	return applyImageToState(p.image, st), nil
}

// applyImageToState converts an image config into LLB instructions
func applyImageToState(image dockerfile2llb.Image, st llb.State) llb.State {
	// FIXME: there are unhandled sections of the image config
	for _, env := range image.Config.Env {
		k, v := parseKeyValue(env)
		st = st.AddEnv(k, v)
	}
	if image.Config.WorkingDir != "" {
		st = st.Dir(image.Config.WorkingDir)
	}
	if image.Config.User != "" {
		st = st.User(image.Config.User)
	}
	return st
}

func parseKeyValue(env string) (string, string) {
	parts := strings.SplitN(env, "=", 2)
	v := ""
	if len(parts) > 1 {
		v = parts[1]
	}

	return parts[0], v
}

func (p *Pipeline) PushContainer(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	rawRef, err := op.Lookup("ref").String()
	if err != nil {
		return st, err
	}

	ref, err := reference.ParseNormalizedNamed(rawRef)
	if err != nil {
		return st, fmt.Errorf("failed to parse ref %s: %w", rawRef, err)
	}
	// Add the default tag "latest" to a reference if it only has a repo name.
	ref = reference.TagNameOnly(ref)

	resp, err := p.s.Export(ctx, p.State(), &p.image, bk.ExportEntry{
		Type: bk.ExporterImage,
		Attrs: map[string]string{
			"name": ref.String(),
			"push": "true",
		},
	})

	if err != nil {
		return st, err
	}

	if digest, ok := resp.ExporterResponse["containerimage.digest"]; ok {
		imageRef := fmt.Sprintf(
			"%s@%s",
			resp.ExporterResponse["image.name"],
			digest,
		)

		return st.File(
			llb.Mkdir("/dagger", fs.FileMode(0755)),
			llb.WithCustomName(p.vertexNamef("Mkdir /dagger")),
		).File(
			llb.Mkfile("/dagger/image_digest", fs.FileMode(0644), []byte(digest)),
			llb.WithCustomName(p.vertexNamef("Storing image digest to /dagger/image_digest")),
		).File(
			llb.Mkfile("/dagger/image_ref", fs.FileMode(0644), []byte(imageRef)),
			llb.WithCustomName(p.vertexNamef("Storing image ref to /dagger/image_ref")),
		), nil
	}

	return st, err
}

func (p *Pipeline) FetchGit(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	remote, err := op.Lookup("remote").String()
	if err != nil {
		return st, err
	}
	ref, err := op.Lookup("ref").String()
	if err != nil {
		return st, err
	}

	remoteRedacted := remote
	if u, err := url.Parse(remote); err == nil {
		remoteRedacted = u.Redacted()
	}

	gitOpts := []llb.GitOption{}
	var opts struct {
		AuthTokenSecret  string
		AuthHeaderSecret string
		KeepGitDir       bool
	}

	if err := op.Decode(&opts); err != nil {
		return st, err
	}

	if opts.KeepGitDir {
		gitOpts = append(gitOpts, llb.KeepGitDir())
	}
	if opts.AuthTokenSecret != "" {
		gitOpts = append(gitOpts, llb.AuthTokenSecret(opts.AuthTokenSecret))
	}
	if opts.AuthHeaderSecret != "" {
		gitOpts = append(gitOpts, llb.AuthTokenSecret(opts.AuthHeaderSecret))
	}

	gitOpts = append(gitOpts, llb.WithCustomName(p.vertexNamef("FetchGit %s@%s", remoteRedacted, ref)))

	return llb.Git(
		remote,
		ref,
		gitOpts...,
	), nil
}

func (p *Pipeline) FetchHTTP(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	link, err := op.Lookup("url").String()
	if err != nil {
		return st, err
	}

	linkRedacted := link
	if u, err := url.Parse(link); err == nil {
		linkRedacted = u.Redacted()
	}

	httpOpts := []llb.HTTPOption{}
	var opts struct {
		Checksum string
		Filename string
		Mode     int64
		UID      int
		GID      int
	}

	if err := op.Decode(&opts); err != nil {
		return st, err
	}

	if opts.Checksum != "" {
		dgst, err := digest.Parse(opts.Checksum)
		if err != nil {
			return st, err
		}
		httpOpts = append(httpOpts, llb.Checksum(dgst))
	}
	if opts.Filename != "" {
		httpOpts = append(httpOpts, llb.Filename(opts.Filename))
	}
	if opts.Mode != 0 {
		httpOpts = append(httpOpts, llb.Chmod(fs.FileMode(opts.Mode)))
	}
	if opts.UID != 0 && opts.GID != 0 {
		httpOpts = append(httpOpts, llb.Chown(opts.UID, opts.GID))
	}

	httpOpts = append(httpOpts, llb.WithCustomName(p.vertexNamef("FetchHTTP %s", linkRedacted)))

	return llb.HTTP(
		link,
		httpOpts...,
	), nil
}

func (p *Pipeline) DockerBuild(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	var (
		dockerContext = op.Lookup("context")
		dockerfile    = op.Lookup("dockerfile")

		contextDef    *bkpb.Definition
		dockerfileDef *bkpb.Definition

		err error
	)

	if !dockerContext.Exists() && !dockerfile.Exists() {
		return st, errors.New("context or dockerfile required")
	}

	// docker build context. This can come from another component, so we need to
	// compute it first.
	if dockerContext.Exists() {
		from := NewPipeline(op.Lookup("context"), p.s)
		if err := from.Run(ctx); err != nil {
			return st, err
		}
		contextDef, err = p.s.Marshal(ctx, from.State())
		if err != nil {
			return st, err
		}
		dockerfileDef = contextDef
	}

	// Inlined dockerfile: need to be converted to LLB
	if dockerfile.Exists() {
		content, err := dockerfile.String()
		if err != nil {
			return st, err
		}
		dockerfileDef, err = p.s.Marshal(ctx,
			llb.Scratch().File(
				llb.Mkfile("/Dockerfile", 0644, []byte(content)),
			),
		)
		if err != nil {
			return st, err
		}
		if contextDef == nil {
			contextDef = dockerfileDef
		}
	}

	opts, err := dockerBuildOpts(op)
	if err != nil {
		return st, err
	}

	if p.s.NoCache() {
		opts["no-cache"] = ""
	}

	req := bkgw.SolveRequest{
		Frontend:    "dockerfile.v0",
		FrontendOpt: opts,
		FrontendInputs: map[string]*bkpb.Definition{
			dockerfilebuilder.DefaultLocalNameContext:    contextDef,
			dockerfilebuilder.DefaultLocalNameDockerfile: dockerfileDef,
		},
	}

	res, err := p.s.SolveRequest(ctx, req)
	if err != nil {
		return st, err
	}
	if meta, ok := res.Metadata[exptypes.ExporterImageConfigKey]; ok {
		if err := json.Unmarshal(meta, &p.image); err != nil {
			return st, fmt.Errorf("failed to unmarshal image config: %w", err)
		}
	}

	ref, err := res.SingleRef()
	if err != nil {
		return st, err
	}
	st, err = ref.ToState()
	if err != nil {
		return st, err
	}
	return applyImageToState(p.image, st), nil
}

func dockerBuildOpts(op *compiler.Value) (map[string]string, error) {
	opts := map[string]string{}

	if dockerfilePath := op.Lookup("dockerfilePath"); dockerfilePath.Exists() {
		filename, err := dockerfilePath.String()
		if err != nil {
			return nil, err
		}
		opts["filename"] = filename
	}

	if target := op.Lookup("target"); target.Exists() {
		tgr, err := target.String()
		if err != nil {
			return nil, err
		}
		opts["target"] = tgr
	}

	if hosts := op.Lookup("hosts"); hosts.Exists() {
		p := []string{}
		fields, err := hosts.Fields()
		if err != nil {
			return nil, err
		}
		for _, host := range fields {
			s, err := host.Value.String()
			if err != nil {
				return nil, err
			}
			p = append(p, host.Label()+"="+s)
		}
		if len(p) > 0 {
			opts["add-hosts"] = strings.Join(p, ",")
		}
	}

	if buildArgs := op.Lookup("buildArg"); buildArgs.Exists() {
		fields, err := buildArgs.Fields()
		if err != nil {
			return nil, err
		}
		for _, buildArg := range fields {
			v, err := buildArg.Value.String()
			if err != nil {
				return nil, err
			}
			opts["build-arg:"+buildArg.Label()] = v
		}
	}

	if labels := op.Lookup("label"); labels.Exists() {
		fields, err := labels.Fields()
		if err != nil {
			return nil, err
		}
		for _, label := range fields {
			s, err := label.Value.String()
			if err != nil {
				return nil, err
			}
			opts["label:"+label.Label()] = s
		}
	}

	if platforms := op.Lookup("platforms"); platforms.Exists() {
		p := []string{}
		list, err := platforms.List()
		if err != nil {
			return nil, err
		}

		for _, platform := range list {
			s, err := platform.String()
			if err != nil {
				return nil, err
			}
			p = append(p, s)
		}

		if len(p) > 0 {
			opts["platform"] = strings.Join(p, ",")
		}
		if len(p) > 1 {
			opts["multi-platform"] = "true"
		}
	}

	return opts, nil
}

func (p *Pipeline) WriteFile(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	var content []byte
	var err error

	switch kind := op.Lookup("content").Kind(); kind {
	case cue.BytesKind:
		content, err = op.Lookup("content").Bytes()
	case cue.StringKind:
		var str string
		str, err = op.Lookup("content").String()
		if err == nil {
			content = []byte(str)
		}
	case cue.BottomKind:
		err = fmt.Errorf("%s: WriteFile content is not set", p.canonicalPath(op))
	default:
		err = fmt.Errorf("%s: unhandled data type in WriteFile: %s", p.canonicalPath(op), kind)
	}
	if err != nil {
		return st, err
	}

	dest, err := op.Lookup("dest").String()
	if err != nil {
		return st, err
	}

	mode, err := op.Lookup("mode").Int64()
	if err != nil {
		return st, err
	}

	return st.File(
		llb.Mkfile(dest, fs.FileMode(mode), content),
		llb.WithCustomName(p.vertexNamef("WriteFile %s", dest)),
	), nil
}

func (p *Pipeline) Mkdir(ctx context.Context, op *compiler.Value, st llb.State) (llb.State, error) {
	pathString, err := op.Lookup("path").String()
	if err != nil {
		return st, err
	}

	dir, err := op.Lookup("dir").String()
	if err != nil {
		return st, err
	}

	mode, err := op.Lookup("mode").Int64()
	if err != nil {
		return st, err
	}

	return st.Dir(dir).File(
		llb.Mkdir(pathString, fs.FileMode(mode)),
		llb.WithCustomName(p.vertexNamef("Mkdir %s", pathString)),
	), nil
}
