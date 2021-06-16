package keychain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"filippo.io/age"
	"github.com/mitchellh/go-homedir"
	"github.com/rs/zerolog/log"
)

func Path() (string, error) {
	keysFile, err := homedir.Expand("~/.config/dagger/keys.txt")
	if err != nil {
		return "", err
	}

	// if the keys file doesn't exist, attempt a migration
	if _, err := os.Stat(keysFile); errors.Is(err, os.ErrNotExist) {
		migrateKeys(keysFile)
	}

	return keysFile, nil
}

// migrateKeys attempts a migration from `~/.dagger/keys.txt` to `~/.config/dagger/keys.txt`
func migrateKeys(keysFile string) error {
	oldKeysFile, err := homedir.Expand("~/.dagger/keys.txt")
	if err != nil {
		return err
	}

	if _, err := os.Stat(oldKeysFile); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(keysFile), 0700); err != nil {
		return err
	}

	return os.Rename(oldKeysFile, keysFile)
}

func Default(ctx context.Context) (string, error) {
	keys, err := List(ctx)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			k, err := Generate(ctx)
			if err != nil {
				return "", err
			}
			return k.Recipient().String(), nil
		}
		return "", err
	}
	if len(keys) == 0 {
		return "", errors.New("no identities found in the keys file")
	}

	return keys[0].Recipient().String(), nil
}

func addToKeychain(k *age.X25519Identity) error {
	keysFile, err := Path()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(keysFile), 0700); err != nil {
		return err
	}

	firstKey := true
	if _, err := os.Stat(keysFile); err == nil {
		firstKey = false
	}

	f, err := os.OpenFile(keysFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("failed to open keys file %q: %v", keysFile, err)
	}
	defer f.Close()
	if !firstKey {
		fmt.Fprintf(f, "\n")
	}
	fmt.Fprintf(f, "# created: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(f, "# public key: %s\n", k.Recipient())
	fmt.Fprintf(f, "%s\n", k)

	return nil
}

func Generate(ctx context.Context) (*age.X25519Identity, error) {
	k, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, fmt.Errorf("internal error: %v", err)
	}
	log.Ctx(ctx).Debug().Str("publicKey", k.Recipient().String()).Msg("generating keypair")

	return k, addToKeychain(k)
}

func Import(ctx context.Context, privateKey string) (*age.X25519Identity, error) {
	// Ensure there is a `Default` key before importing a new key.
	if _, err := Default(ctx); err != nil {
		return nil, err
	}

	k, err := age.ParseX25519Identity(privateKey)
	if err != nil {
		return nil, err
	}

	return k, addToKeychain(k)
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
		key, ok := id.(*age.X25519Identity)
		if !ok {
			return nil, fmt.Errorf("internal error: unexpected identity type: %T", id)
		}
		keys = append(keys, key)
	}

	return keys, nil
}

func Get(ctx context.Context, publicKey string) (*age.X25519Identity, error) {
	keys, err := List(ctx)
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		if k.Recipient().String() == publicKey {
			return k, nil
		}
	}
	return nil, fmt.Errorf("key %q not found", publicKey)
}
