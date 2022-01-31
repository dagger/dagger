package task

import (
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
)

type authValue struct {
	Username string
	Secret   *plancontext.Secret
}

// Decodes an auth field value
//
// Cue format:
//   auth: {
//     username: string
//     secret:   string | #Secret
//   }
func decodeAuthValue(pctx *plancontext.Context, v *compiler.Value) (*authValue, error) {
	authVal := authValue{}
	username, err := v.Lookup("username").String()
	if err != nil {
		return nil, err
	}
	authVal.Username = username

	secret, err := pctx.Secrets.FromValue(v.Lookup("secret"))
	if err != nil {
		return nil, err
	}
	authVal.Secret = secret

	return &authVal, nil
}
