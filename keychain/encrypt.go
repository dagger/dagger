package keychain

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.mozilla.org/sops/v3"
	sopsaes "go.mozilla.org/sops/v3/aes"
	sopsage "go.mozilla.org/sops/v3/age"
	"go.mozilla.org/sops/v3/cmd/sops/common"
	sopskeys "go.mozilla.org/sops/v3/keys"
	sopsyaml "go.mozilla.org/sops/v3/stores/yaml"
	"go.mozilla.org/sops/v3/version"
)

var (
	cipher = sopsaes.NewCipher()
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
		DataKey: dataKey, Tree: &tree, Cipher: cipher,
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
		DataKey: key, Tree: &tree, Cipher: cipher,
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

	store := &sopsyaml.Store{}

	// Load SOPS file and access the data key
	tree, err := store.LoadEncryptedFile(encrypted)
	if err != nil {
		return nil, err
	}
	key, err := tree.Metadata.GetDataKey()
	if err != nil {
		if userErr, ok := err.(sops.UserError); ok {
			err = fmt.Errorf(userErr.UserError())
		}
		return nil, err
	}

	// Decrypt the tree
	mac, err := tree.Decrypt(key, cipher)
	if err != nil {
		return nil, err
	}

	// Compute the hash of the cleartext tree and compare it with
	// the one that was stored in the document. If they match,
	// integrity was preserved
	originalMac, err := cipher.Decrypt(
		tree.Metadata.MessageAuthenticationCode,
		key,
		tree.Metadata.LastModified.Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	if originalMac != mac {
		return nil, fmt.Errorf("failed to verify data integrity. expected mac %q, got %q", originalMac, mac)
	}

	return store.EmitPlainFile(tree.Branches)
}
