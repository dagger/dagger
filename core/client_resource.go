package core

import (
	"context"
	"crypto/hmac"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/opencontainers/go-digest"
)

func GetClientResourceAccessor(ctx context.Context, parent *Query, externalName string) (string, error) {
	m, err := parent.CurrentModule(ctx)
	if err != nil && !errors.Is(err, ErrNoCurrentModule) {
		return "", err
	}

	var scopeDigest digest.Digest
	if m != nil {
		id, err := m.SourceContentScopedID(ctx)
		if err != nil {
			return "", err
		}
		scopeDigest = id.ContentDigest()
	}

	// Use an HMAC, which allows us to keep the externalName un-inferrable.
	// This also protects from length-extension attacks (where if we had
	// access to secret FOO in scope X, we could derive access to FOOBAR).
	h := hmac.New(digest.SHA256.Hash, []byte(scopeDigest))
	dt := h.Sum([]byte(externalName))
	return hex.EncodeToString(dt), nil
}

// CachePerCallerModule scopes a call ID per caller module (using the module's source content digest). If the caller is not in a module, the input is just an empty string.
var CachePerCallerModule = dagql.ImplicitInput{
	Name: "cachePerCallerModule",
	Resolver: func(ctx context.Context, _ map[string]dagql.Input) (dagql.Input, error) {
		q, err := CurrentQuery(ctx)
		if err != nil {
			return nil, fmt.Errorf("current query: %w", err)
		}
		m, err := q.CurrentModule(ctx)
		if errors.Is(err, ErrNoCurrentModule) {
			return dagql.NewString("mainClient"), nil
		}
		if err != nil {
			return dagql.NewString(""), fmt.Errorf("failed to get current module: %w", err)
		}
		if m == nil {
			return dagql.NewString("mainClient"), nil
		}

		id, err := m.SourceContentScopedID(ctx)
		if err != nil {
			return nil, err
		}
		scopeDigest := id.ContentDigest()

		return dagql.NewString(scopeDigest.String()), nil
	},
}
