package core

import (
	"context"
	"crypto/hmac"
	"encoding/hex"
	"errors"

	"github.com/opencontainers/go-digest"
)

func GetClientResourceAccessor(ctx context.Context, parent *Query, externalName string) (string, error) {
	m, err := parent.CurrentModule(ctx)
	if err != nil && !errors.Is(err, ErrNoCurrentModule) {
		return "", err
	}

	var scopeDigest digest.Digest
	if m != nil {
		scopeDigest = m.Source.ID().Digest()
	}

	// Use an HMAC, which allows us to keep the externalName un-inferrable.
	// This also protects from length-extension attacks (where if we had
	// access to secret FOO in scope X, we could derive access to FOOBAR).
	h := hmac.New(digest.SHA256.Hash, []byte(scopeDigest))
	dt := h.Sum([]byte(externalName))
	return hex.EncodeToString(dt), nil
}
