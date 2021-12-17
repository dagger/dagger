package task

import (
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
)

type authValue struct {
	Target   string
	Username string
	Secret   *plancontext.Secret
}

// Decodes an auth field value
//
// Cue format:
//   auth: [...{
//     target:   string
//     username: string
//     secret:   string | #Secret
//   }]
func decodeAuthValue(pctx *plancontext.Context, v *compiler.Value) ([]*authValue, error) {
	vals, err := v.List()
	if err != nil {
		return nil, err
	}

	authVals := []*authValue{}
	for _, val := range vals {
		authVal := authValue{}

		target, err := val.Lookup("target").String()
		if err != nil {
			return nil, err
		}
		authVal.Target = target

		username, err := val.Lookup("username").String()
		if err != nil {
			return nil, err
		}
		authVal.Username = username

		secret, err := pctx.Secrets.FromValue(val.Lookup("secret"))
		if err != nil {
			return nil, err
		}
		authVal.Secret = secret

		authVals = append(authVals, &authVal)
	}

	return authVals, nil
}
