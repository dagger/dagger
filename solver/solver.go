package solver

import (
	"crypto/sha256"
	"fmt"

	"dagger.io/dagger"
)

type Solver struct {
	Client *dagger.Client
}

func (s *Solver) NewSecret(plaintext string) *dagger.Secret {
	secretid := hashID(plaintext)

	return s.Client.Directory().WithNewFile(secretid, plaintext).File(secretid).Secret()
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
