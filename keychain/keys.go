package keychain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"time"

	"filippo.io/age"
	"github.com/mitchellh/go-homedir"
	"github.com/rs/zerolog/log"
)

func Path() (string, error) {
	h, err := homedir.Dir()
	if err != nil {
		return "", err
	}

	return path.Join(h, ".dagger", "keys.txt"), nil
}

func Default(ctx context.Context) (string, error) {
	keys, err := List(ctx)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Generate(ctx)
		}
		return "", err
	}
	if len(keys) == 0 {
		return "", errors.New("no identities found in the keys file")
	}

	return keys[0].Recipient().String(), nil
}

func Generate(ctx context.Context) (string, error) {
	keysFile, err := Path()
	if err != nil {
		return "", err
	}

	k, err := age.GenerateX25519Identity()
	if err != nil {
		return "", fmt.Errorf("internal error: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(keysFile), 0755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(keysFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", fmt.Errorf("failed to open keys file %q: %v", keysFile, err)
	}
	defer f.Close()
	fmt.Fprintf(f, "# created: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(f, "# public key: %s\n", k.Recipient())
	fmt.Fprintf(f, "%s\n", k)

	pubkey := k.Recipient().String()

	log.Ctx(ctx).Debug().Str("publicKey", pubkey).Msg("generating keypair")

	return pubkey, nil
}

func List(ctx context.Context) ([]*age.X25519Identity, error) {
	keysFile, err := Path()
	if err != nil {
		return nil, err
	}

	f, err := os.Open(keysFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open keys file file %q: %w", keysFile, err)
	}
	ids, err := age.ParseIdentities(f)
	if err != nil {
		return nil, fmt.Errorf("failed to parse input: %w", err)
	}

	keys := make([]*age.X25519Identity, 0, len(ids))
	for _, id := range ids {
		key, ok := ids[0].(*age.X25519Identity)
		if !ok {
			return nil, fmt.Errorf("internal error: unexpected identity type: %T", id)
		}
		keys = append(keys, key)
	}

	return keys, nil
}
