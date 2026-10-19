// A module used to check same-session secret isolation: a module running as a
// nested client must only resolve secrets and sockets it was explicitly passed,
// never ones it merely holds (or forged) the result ID of.
package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct{}

// StealFromID models the attack: the module is handed only a secret's ID (as a
// plain string, e.g. obtained by guessing or enumerating engine result IDs) and
// tries to resolve its plaintext. The module was never passed the secret, so
// this must fail.
func (*Test) StealFromID(ctx context.Context, id string) (string, error) {
	return dag.LoadSecretFromID(dagger.SecretID(id)).Plaintext(ctx)
}

// UseSecret is the legitimate control: a secret explicitly passed to the module
// as a Secret argument resolves normally.
func (*Test) UseSecret(ctx context.Context, s *dagger.Secret) (string, error) {
	return s.Plaintext(ctx)
}

// UseContainer is the nested control: a secret carried inside a Container
// argument (as a secret env var) must resolve when the module runs the
// container, since the Container argument conveys the secret to the module.
func (*Test) UseContainer(ctx context.Context, ctr *dagger.Container) (string, error) {
	return ctr.
		WithExec([]string{"sh", "-c", "test -n \"$STOLEN\" && echo resolved"}).
		Stdout(ctx)
}

// MakeSecretReturn returns a secret the module itself creates. The caller that
// receives it may resolve it.
func (*Test) MakeSecretReturn(plaintext string) *dagger.Secret {
	return dag.SetSecret("made-in-module", plaintext)
}

// StealSocketFromID is the socket form of the attack: the module is handed only
// a socket's ID and tries to use it. Mounting and running the socket forces the
// engine to resolve the session resource under the module's client, which it
// was never granted.
func (*Test) StealSocketFromID(ctx context.Context, id string) (string, error) {
	sock := dag.LoadSocketFromID(dagger.SocketID(id))
	return dag.Container().From("alpine:3.20").
		WithUnixSocket("/tmp/s.sock", sock).
		WithExec([]string{"true"}).
		Stdout(ctx)
}
