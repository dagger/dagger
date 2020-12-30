package dagger

import (
	"context"
	"encoding/json"
	"fmt"

	"cuelang.org/go/cue"
	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

type Script struct {
	v   cue.Value
	job Job

	// current state
	state *State
}

func (job Job) newScript(v cue.Value) (*Script, error) {
	s := &Script{
		v:     v,
		job:   job,
		state: NewState(job),
	}
	if err := s.Validate(); err != nil {
		return nil, s.err(err, "invalid script")
	}
	return s, nil
}

type Action func(context.Context, cue.Value, Fillable) error

func (s *Script) Run(ctx context.Context, out Fillable) error {
	op, err := s.Cue().List()
	if err != nil {
		return s.err(err, "run")
	}
	i := 0
	for op.Next() {
		// If op is not concrete, interrupt execution without error.
		// This allows gradual resolution: compute what you can compute.. leave the rest incomplete.
		if !cueIsConcrete(op.Value()) {
			debugf("%s: non-concrete op. Leaving script unfinished", op.Value().Path().String())
			return nil
		}
		if err := s.Do(ctx, op.Value(), out); err != nil {
			return s.err(err, "run op %d", i+1)
		}
		i += 1
	}
	return nil
}

func (s *Script) Do(ctx context.Context, op cue.Value, out Fillable) error {
	// Skip no-ops without error (allows more flexible use of if())
	// FIXME: maybe not needed once a clear pattern is established for
	// how to use if() in a script?
	if cueIsEmptyStruct(op) {
		return nil
	}
	actions := map[string]Action{
		// "#Copy": s.copy,
		"#Exec":           s.exec,
		"#Export":         s.export,
		"#FetchContainer": s.fetchContainer,
		"#FetchGit":       s.fetchGit,
		"#Load":           s.load,
		"#Copy":           s.copy,
	}
	for def, action := range actions {
		if s.matchSpec(op, def) {
			debugf("OP MATCH: %s: %s: %v", def, op.Path().String(), op)
			return action(ctx, op, out)
		}
	}
	return fmt.Errorf("[%s] invalid operation: %s", s.v.Path().String(), cueToJSON(op))
}

func (s *Script) copy(ctx context.Context, v cue.Value, out Fillable) error {
	// Decode copy options
	var op struct {
		Src  string
		Dest string
	}
	if err := v.Decode(&op); err != nil {
		return err
	}
	from := v.Lookup("from")
	if isComponent, err := s.job.r.isComponent(from); err != nil {
		return err
	} else if isComponent {
		return s.copyComponent(ctx, from, op.Src, op.Dest)
	}
	if s.matchSpec(from, "#Script") {
		return s.copyScript(ctx, from, op.Src, op.Dest)
	}
	return fmt.Errorf("copy: invalid source")
}

func (s *Script) copyScript(ctx context.Context, from cue.Value, src, dest string) error {
	// Load source script
	fromScript, err := s.job.newScript(from)
	if err != nil {
		return err
	}
	// Execute source script
	if err := fromScript.Run(ctx, Discard()); err != nil {
		return err
	}
	return s.State().Change(ctx, func(st llb.State) llb.State {
		return st.File(llb.Copy(
			fromScript.State().LLB(),
			src,
			dest,
			// FIXME: allow more configurable llb options
			// For now we define the following convenience presets:
			&llb.CopyInfo{
				CopyDirContentsOnly: true,
				CreateDestPath:      true,
				AllowWildcard:       true,
			},
		))
	})
}

func (s *Script) copyComponent(ctx context.Context, from cue.Value, src, dest string) error {
	return s.copyScript(ctx, from.LookupPath(cue.ParsePath("#dagger.compute")), src, dest)
}

func (s *Script) load(ctx context.Context, op cue.Value, out Fillable) error {
	from := op.Lookup("from")
	isComponent, err := s.job.r.isComponent(from)
	if err != nil {
		return err
	}
	if isComponent {
		debugf("LOAD: from is a component")
		return s.loadScript(ctx, from.LookupPath(cue.ParsePath("#dagger.compute")))
	}
	if s.matchSpec(from, "#Script") {
		return s.loadScript(ctx, from)
	}
	return fmt.Errorf("load: invalid source")
}

func (s *Script) loadScript(ctx context.Context, v cue.Value) error {
	from, err := s.job.newScript(v)
	if err != nil {
		return errors.Wrap(err, "load")
	}
	// NOTE we discard cue outputs from running the loaded script.
	// This means we load the LLB state but NOT the cue exports.
	// In other words: cue exports are always private to their original location.
	if err := from.Run(ctx, Discard()); err != nil {
		return errors.Wrap(err, "load/execute")
	}
	// overwrite buildkit state from loaded from
	s.state = from.state
	return nil
}

func (s *Script) exec(ctx context.Context, v cue.Value, out Fillable) error {
	var opts []llb.RunOption
	var cmd struct {
		Args   []string
		Env    map[string]string
		Dir    string
		Always bool
	}
	v.Decode(&cmd)
	// marker for status events
	opts = append(opts, llb.WithCustomName(v.Path().String()))
	// args
	opts = append(opts, llb.Args(cmd.Args))
	// dir
	dir := cmd.Dir
	if dir == "" {
		dir = "/"
	}
	// env
	for k, v := range cmd.Env {
		opts = append(opts, llb.AddEnv(k, v))
	}
	// always?
	if cmd.Always {
		cacheBuster, err := randomID(8)
		if err != nil {
			return err
		}
		opts = append(opts, llb.AddEnv("DAGGER_CACHEBUSTER", cacheBuster))
	}
	// mounts
	mnt, _ := v.Lookup("mount").Fields()
	for mnt.Next() {
		opt, err := s.mount(ctx, mnt.Label(), mnt.Value())
		if err != nil {
			return err
		}
		opts = append(opts, opt)
	}
	// --> Execute
	return s.State().Change(ctx, func(st llb.State) llb.State {
		return st.Run(opts...).Root()
	})
}

func (s *Script) mount(ctx context.Context, dest string, source cue.Value) (llb.RunOption, error) {
	if s.matchSpec(source, "#MountTmp") {
		return llb.AddMount(dest, llb.Scratch(), llb.Tmpfs()), nil
	}
	if s.matchSpec(source, "#MountCache") {
		// FIXME: cache mount
		return nil, fmt.Errorf("FIXME: cache mount not yet implemented")
	}
	if s.matchSpec(source, "#MountScript") {
		return s.mountScript(ctx, dest, source)
	}
	if s.matchSpec(source, "#MountComponent") {
		return s.mountComponent(ctx, dest, source)
	}
	return nil, fmt.Errorf("mount %s to %s: invalid source", source.Path().String(), dest)
}

// mount when the input is a script (see mountComponent, mountTmpfs, mountCache)
func (s *Script) mountScript(ctx context.Context, dest string, source cue.Value) (llb.RunOption, error) {
	script, err := s.job.newScript(source)
	if err != nil {
		return nil, err
	}
	// FIXME: this is where we re-run everything,
	// and rely on solver cache / dedup
	if err := script.Run(ctx, Discard()); err != nil {
		return nil, err
	}
	return llb.AddMount(dest, script.State().LLB()), nil
}

func (s *Script) mountComponent(ctx context.Context, dest string, source cue.Value) (llb.RunOption, error) {
	return s.mountScript(ctx, dest, source.LookupPath(cue.ParsePath("from.#dagger.compute")))
}

func (s *Script) fetchContainer(ctx context.Context, v cue.Value, out Fillable) error {
	var op struct {
		Ref string
	}
	if err := v.Decode(&op); err != nil {
		return errors.Wrap(err, "decode fetch-container")
	}
	return s.State().Change(ctx, llb.Image(op.Ref))
}

func (s *Script) fetchGit(ctx context.Context, v cue.Value, out Fillable) error {
	// See #FetchGit in spec.cue
	var op struct {
		Remote string
		Ref    string
	}
	if err := v.Decode(&op); err != nil {
		return errors.Wrap(err, "decode fetch-git")
	}
	return s.State().Change(ctx, llb.Git(op.Remote, op.Ref))
}

func (s *Script) export(ctx context.Context, v cue.Value, out Fillable) error {
	// See #Export in spec.cue
	var op struct {
		Source string
		// FIXME: target
		// Target string
		Format string
	}
	v.Decode(&op)
	b, err := s.State().ReadFile(ctx, op.Source)
	if err != nil {
		return err
	}
	switch op.Format {
	case "string":
		return out.Fill(string(b))
	case "json":
		var o interface{}
		if err := json.Unmarshal(b, &o); err != nil {
			return err
		}
		return out.Fill(o)
	default:
		return fmt.Errorf("unsupported export format: %q", op.Format)
	}
}

func (s *Script) Cue() cue.Value {
	return s.v
}

func (s *Script) Location() string {
	return s.Cue().Path().String()
}

func (s *Script) err(e error, msg string, args ...interface{}) error {
	return errors.Wrapf(e, s.Location()+": "+msg, args...)
}

func (s *Script) Validate() error {
	return s.job.r.validateSpec(s.Cue(), "#Script")
}

func (s *Script) State() *State {
	return s.state
}

func (s *Script) matchSpec(v cue.Value, def string) bool {
	// FIXME: we manually filter out empty structs to avoid false positives
	// This is necessary because Runtime.ValidateSpec has a bug
	// where an empty struct matches everything.
	// see https://github.com/cuelang/cue/issues/566#issuecomment-749735878
	// Once the bug is fixed, the manual check can be removed.
	if st, err := v.Struct(); err == nil {
		if st.Len() == 0 {
			debugf("FIXME: manually filtering out empty struct from spec match")
			return false
		}
	}
	return s.job.r.matchSpec(v, def)
}
