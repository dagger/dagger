package task

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"cuelang.org/go/cue"
	"dagger.io/dagger"
	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/engine/utils"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("ClientCommand", func() Task { return &clientCommandTask{} })
}

type clientCommandTask struct {
}

func (t clientCommandTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, c *dagger.Client, v *compiler.Value) (*compiler.Value, error) {
	var opts struct {
		Name string
		Args []string
	}

	if err := v.Decode(&opts); err != nil {
		return nil, err
	}

	flags, err := v.Lookup("flags").Fields()
	if err != nil {
		return nil, err
	}

	var flagArgs []string
	for _, flag := range flags {
		switch flag.Value.Kind() {
		case cue.BoolKind:
			if b, _ := flag.Value.Bool(); b {
				flagArgs = append(flagArgs, flag.Label())
			}
		case cue.StringKind:
			if s, _ := flag.Value.String(); s != "" {
				flagArgs = append(flagArgs, flag.Label(), s)
			}
		}
	}
	opts.Args = append(flagArgs, opts.Args...)

	envs, err := v.Lookup("env").Fields()
	if err != nil {
		return nil, err
	}

	env := make([]string, len(envs))
	for _, envvar := range envs {
		s, err := t.getString(ctx, pctx, s, envvar.Value)
		if err != nil {
			return nil, err
		}
		env = append(env, fmt.Sprintf("%s=%s", envvar.Label(), s))
	}

	lg := log.Ctx(ctx)
	lg.Debug().Str("name", opts.Name).Str("args", strings.Join(opts.Args, " ")).Msg("running client command")

	cmd := exec.CommandContext(ctx, opts.Name, opts.Args...) //#nosec G204
	cmd.Env = append(os.Environ(), env...)

	if i := v.Lookup("stdin"); i.Exists() {
		val, err := t.getString(ctx, pctx, s, i)
		if err != nil {
			return nil, err
		}

		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}

		go func() {
			defer stdin.Close()
			io.WriteString(stdin, val)
		}()
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	stdoutVal, err := t.readPipe(&stdout, ctx, pctx, s, v.Lookup("stdout"))
	if err != nil {
		return nil, err
	}

	stderrVal, err := t.readPipe(&stderr, ctx, pctx, s, v.Lookup("stderr"))
	if err != nil {
		return nil, err
	}

	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// FIXME: stderr may be requested as a secret
			lg.Err(err).Msg(string(exitErr.Stderr))
		}
		return nil, err
	}

	return compiler.NewValue().FillFields(map[string]interface{}{
		"stdout": stdoutVal,
		"stderr": stderrVal,
	})
}

func (t clientCommandTask) getString(ctx context.Context, pctx *plancontext.Context, solver *solver.Solver, v *compiler.Value) (string, error) {
	if utils.IsSecretValue(v) {

		secretid, err := utils.GetSecretId(v)
		if err != nil {
			return "", err
		}
		plaintext, err := solver.Client.Secret(secretid).Plaintext(ctx)
		return plaintext, nil
	}

	s, err := v.String()
	if err != nil {
		return "", err
	}

	return s, nil
}

func (t clientCommandTask) readPipe(pipe *io.ReadCloser, ctx context.Context, pctx *plancontext.Context, solver *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	slurp, err := io.ReadAll(*pipe)
	if err != nil {
		return nil, err
	}

	read := string(slurp)
	val, _ := v.Default()
	out := compiler.NewValue()

	if utils.IsSecretValue(val) {
		secretid, err := solver.NewSecret(read).ID(ctx)
		if err != nil {
			return nil, err
		}
		return out.Fill(utils.NewSecretFromId(secretid))
	}

	return out.Fill(read)
}
