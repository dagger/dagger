package solver

import (
	"crypto/sha256"
	"fmt"
	"os"

	"dagger.io/dagger"
)

type Solver struct {
	Client *dagger.Client
}

func (s *Solver) NewSecret(plaintext string) *dagger.Secret {
	env := hashID(plaintext)
	err := os.Setenv(env, plaintext)
	if err != nil {
		panic(err)
	}

	return s.Client.Directory().WithNewFile(env, dagger.DirectoryWithNewFileOpts{Contents: plaintext}).File(env).Secret()
}

func hashID(values ...string) string {
	hash := sha256.New()
	for _, v := range values {
		if _, err := hash.Write([]byte(v)); err != nil {
			panic(err)
		}
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}
