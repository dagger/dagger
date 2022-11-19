package task

import (
	"context"
	"strings"

	"dagger.io/dagger"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/engine/utils"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("NewSecret", func() Task { return &newSecretTask{} })
}

type newSecretTask struct {
}

func (t *newSecretTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, dgr *dagger.Client, v *compiler.Value) (*compiler.Value, error) {
	path, err := v.Lookup("path").String()
	if err != nil {
		return nil, err
	}

	fsid, err := utils.GetFSId(v.Lookup("input"))

	if err != nil {
		return nil, err
	}

	secret := dgr.Directory(dagger.DirectoryOpts{ID: dagger.DirectoryID(fsid)}).File(path).Secret()

	var secretid dagger.SecretID
	trimSpace, err := v.Lookup("trimSpace").Bool()
	if err != nil {
		return nil, err
	}
	if trimSpace {
		plaintext, err := secret.Plaintext(ctx)
		if err != nil {
			return nil, err
		}
		newsecret := s.NewSecret(strings.TrimSpace(plaintext))
		secretid, err = newsecret.ID(ctx)
		if err != nil {
			return nil, err
		}
	} else {
		secretid, err = secret.ID(ctx)
		if err != nil {
			return nil, err
		}
	}

	return compiler.NewValue().FillFields(map[string]interface{}{
		"output": utils.NewSecretFromId(secretid),
	})
}
