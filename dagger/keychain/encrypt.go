package keychain

import (
	"context"
	"fmt"
	"os"

	"go.mozilla.org/sops/v3"
	"go.mozilla.org/sops/v3/aes"
	sopsage "go.mozilla.org/sops/v3/age"
	"go.mozilla.org/sops/v3/cmd/sops/common"
	"go.mozilla.org/sops/v3/cmd/sops/formats"
	sopsdecrypt "go.mozilla.org/sops/v3/decrypt"
	sopskeys "go.mozilla.org/sops/v3/keys"
	sopsyaml "go.mozilla.org/sops/v3/stores/yaml"
	"go.mozilla.org/sops/v3/version"
)

// setupEnv: hack to inject a SOPS env var for age
func setupEnv() error {
	p, err := Path()
	if err != nil {
		return err
	}
	return os.Setenv("SOPS_AGE_KEY_FILE", p)
}

// Encrypt data using SOPS with the AGE backend, using the provided public key
func Encrypt(ctx context.Context, path string, plaintext []byte, key string) ([]byte, error) {
	if err := setupEnv(); err != nil {
		return nil, err
	}

	store := &sopsyaml.Store{}
	branches, err := store.LoadPlainFile(plaintext)
	if err != nil {
		return nil, err
	}

	ageKeys, err := sopsage.MasterKeysFromRecipients(key)
	if err != nil {
		return nil, err
	}
	ageMasterKeys := make([]sopskeys.MasterKey, 0, len(ageKeys))
	for _, k := range ageKeys {
		ageMasterKeys = append(ageMasterKeys, k)
	}
	var group sops.KeyGroup
	group = append(group, ageMasterKeys...)

	tree := sops.Tree{
		Branches: branches,
		Metadata: sops.Metadata{
			KeyGroups:       []sops.KeyGroup{group},
			EncryptedSuffix: "secret",
			Version:         version.Version,
		},
		FilePath: path,
	}

	// Generate a data key
	dataKey, errs := tree.GenerateDataKey()
	if len(errs) > 0 {
		return nil, fmt.Errorf("error encrypting the data key with one or more master keys: %v", errs)
	}

	err = common.EncryptTree(common.EncryptTreeOpts{
		DataKey: dataKey, Tree: &tree, Cipher: aes.NewCipher(),
	})
	if err != nil {
		return nil, err
	}
	return store.EmitEncryptedFile(tree)
}

// Reencrypt a file with new content using the same keys
func Reencrypt(_ context.Context, path string, plaintext []byte) ([]byte, error) {
	if err := setupEnv(); err != nil {
		return nil, err
	}

	current, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Load the encrypted file
	store := &sopsyaml.Store{}
	tree, err := store.LoadEncryptedFile(current)
	if err != nil {
		return nil, err
	}

	// Update the file with the new data
	newBranches, err := store.LoadPlainFile(plaintext)
	if err != nil {
		return nil, err
	}
	tree.Branches = newBranches

	// Re-encrypt the file
	key, err := tree.Metadata.GetDataKey()
	if err != nil {
		return nil, err
	}
	err = common.EncryptTree(common.EncryptTreeOpts{
		DataKey: key, Tree: &tree, Cipher: aes.NewCipher(),
	})
	if err != nil {
		return nil, err
	}

	return store.EmitEncryptedFile(tree)
}

// Decrypt data using sops
func Decrypt(_ context.Context, encrypted []byte) ([]byte, error) {
	if err := setupEnv(); err != nil {
		return nil, err
	}

	return sopsdecrypt.DataWithFormat(encrypted, formats.Yaml)
}
