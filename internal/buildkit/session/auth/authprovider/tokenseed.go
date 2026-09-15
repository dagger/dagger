package authprovider

import (
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/gofrs/flock"
	"github.com/pkg/errors"
)

type tokenSeeds struct {
	mu  sync.Mutex
	dir string
	m   map[string]seed
}

type seed struct {
	Seed []byte
}

func (ts *tokenSeeds) getSeed(host string) ([]byte, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if err := os.MkdirAll(ts.dir, 0755); err != nil {
		return nil, err
	}

	if ts.m == nil {
		ts.m = map[string]seed{}
	}

	l := flock.New(filepath.Join(ts.dir, ".token_seed.lock"))
	if err := l.Lock(); err != nil {
		if !errors.Is(err, syscall.EROFS) && !errors.Is(err, os.ErrPermission) {
			return nil, err
		}
	} else {
		defer l.Unlock()
	}

	fp := filepath.Join(ts.dir, ".token_seed")

	// we include client side randomness to avoid chosen plaintext attack from the daemon side
	dt, err := os.ReadFile(fp)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) && !errors.Is(err, os.ErrPermission) {
			return nil, err
		}
	} else {
		// ignore error in case of crash during previous marshal
		_ = json.Unmarshal(dt, &ts.m)
	}
	v, ok := ts.m[host]
	if !ok {
		v = seed{Seed: newSeed()}
	}

	ts.m[host] = v

	dt, err = json.MarshalIndent(ts.m, "", "  ")
	if err != nil {
		return nil, err
	}

	if err := os.WriteFile(fp, dt, 0600); err != nil {
		if !errors.Is(err, syscall.EROFS) && !errors.Is(err, os.ErrPermission) {
			return nil, err
		}
	}
	return v.Seed, nil
}

func newSeed() []byte {
	b := make([]byte, 16)
	rand.Read(b)
	return b
}
