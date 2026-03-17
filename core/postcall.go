package core

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/server/resource"
	"github.com/dagger/dagger/engine/slog"
	"github.com/opencontainers/go-digest"
)

//nolint:gocyclo
func ResourceTransferPostCall(
	ctx context.Context,
	query *Query,
	sourceClientID string,
	ids ...*resource.ID,
) (func(context.Context) error, bool, error) {
	secretsByDgst := map[digest.Digest]dagql.ID[*Secret]{}
	socketsByDgst := map[digest.Digest]dagql.ID[*Socket]{}
	for _, id := range ids {
		walked, err := dagql.WalkID(&id.ID, false)
		if err != nil {
			return nil, false, fmt.Errorf("failed to walk ID: %w", err)
		}
		secretIDs := dagql.WalkedIDs[*Secret](walked)
		for _, secretID := range secretIDs {
			secretsByDgst[SecretIDDigest(secretID.ID())] = secretID
		}
		socketIDs := dagql.WalkedIDs[*Socket](walked)
		for _, socketID := range socketIDs {
			socketsByDgst[SocketIDDigest(socketID.ID())] = socketID
		}
	}
	if len(secretsByDgst) == 0 && len(socketsByDgst) == 0 {
		return nil, false, nil
	}

	var secretIDs []dagql.ID[*Secret]
	for _, secretID := range secretsByDgst {
		secretIDs = append(secretIDs, secretID)
	}
	// just in case order matters for caching somehow someday
	slices.SortFunc(secretIDs, func(a, b dagql.ID[*Secret]) int {
		return strings.Compare(SecretIDDigest(a.ID()).String(), SecretIDDigest(b.ID()).String())
	})
	var socketIDs []dagql.ID[*Socket]
	for _, socketID := range socketsByDgst {
		socketIDs = append(socketIDs, socketID)
	}
	// just in case order matters for caching somehow someday
	slices.SortFunc(socketIDs, func(a, b dagql.ID[*Socket]) int {
		return strings.Compare(SocketIDDigest(a.ID()).String(), SocketIDDigest(b.ID()).String())
	})

	// ensure that when we load secrets, we are doing so from the source client
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("failed to get source client metadata: %w", err)
	}
	srcClientCtx := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  sourceClientID,
		SessionID: clientMetadata.SessionID,
	})

	srcSecretStore, err := query.Secrets(srcClientCtx)
	if err != nil {
		// If we can't find the source client, we must have called a function that is persistently cached
		// *on the buildkit cache* (as opposed to dagql cache). Currently this is just internal SDK calls
		// like ModuleRuntime. In this case, the only secrets involved are any related to pulling the module
		// source (like a git auth token). These secrets are already known by the caller and the secret transfer
		// is thus not needed.
		return nil, false, nil //nolint:nilerr
	}
	srcSocketStore, err := query.Sockets(srcClientCtx)
	if err != nil {
		// see rationale above for secrets lookup; socket transfer can be skipped for
		// this same source-client-missing case.
		return nil, false, nil //nolint:nilerr
	}
	srcDag, err := query.Server.Server(srcClientCtx)
	if err != nil {
		return nil, false, fmt.Errorf("failed to get source client dagql server: %w", err)
	}

	secrets, err := dagql.LoadIDResults(srcClientCtx, srcDag, secretIDs)
	if err != nil {
		return nil, false, fmt.Errorf("failed to load secret instances: %w", err)
	}

	type secretWithPlaintext struct {
		inst      dagql.ObjectResult[*Secret]
		plaintext []byte
	}
	var namedSecrets []secretWithPlaintext
	for _, secret := range secrets {
		isNamed := secret.Self().Name != ""
		if !isNamed {
			continue
		}
		secretDigest := SecretDigest(secret)
		plaintext, err := srcSecretStore.GetSecretPlaintext(ctx, secretDigest)
		if err != nil {
			// It's possible to hit secrets not found in the store when there's a cross-session cache hit
			// on content-hashed values (like git tree directories). The value returned from cache may be
			// from a client that used some other secret (e.g. a git auth token) to access the content, even
			// though the final content is all the same.
			// In this case, skipping the transfer of the secret is fine since there's already a cache hit
			// on the content and thus no need to load the secret.
			// Log this for now though in case it ever arises in unexpected cases. If that happens, the error
			// will just be deferred and can be traced back to this log.
			slog.Warn("failed to get secret plaintext",
				"secret", secretDigest,
				"err", err,
				"sourceClientID", sourceClientID,
			)
			continue
		}
		namedSecrets = append(namedSecrets, secretWithPlaintext{inst: secret, plaintext: plaintext})
	}

	var sockets []dagql.ObjectResult[*Socket]
	if len(socketIDs) > 0 {
		socketResults, err := dagql.LoadIDResults(srcClientCtx, srcDag, socketIDs)
		if err != nil {
			return nil, false, fmt.Errorf("failed to load socket instances: %w", err)
		}
		for _, socket := range socketResults {
			if socket.Self() == nil {
				continue
			}
			sockets = append(sockets, socket)
		}
	}

	hasNamedSecrets := len(namedSecrets) > 0
	if len(namedSecrets) == 0 && len(sockets) == 0 {
		return nil, false, nil
	}

	callerClientMemo := sync.Map{}
	var postCall func(context.Context) error
	postCall = func(ctx context.Context) error {
		callerClientMD, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return fmt.Errorf("failed to get client metadata: %w", err)
		}
		if callerClientMD.ClientID == sourceClientID {
			// no need to transfer to ourself
			return nil
		}
		if _, alreadyRan := callerClientMemo.LoadOrStore(callerClientMD.ClientID, struct{}{}); alreadyRan {
			// only run this once per calling client, no need to re-add resources
			return nil
		}

		destDag, err := query.Server.Server(ctx)
		if err != nil {
			return fmt.Errorf("failed to get destination client dagql server: %w", err)
		}
		if len(namedSecrets) > 0 {
			destClientSecretStore, err := query.Secrets(ctx)
			if err != nil {
				return fmt.Errorf("failed to get destination client secret store: %w", err)
			}
			for _, secret := range namedSecrets {
				if err := destClientSecretStore.AddSecret(secret.inst); err != nil {
					return fmt.Errorf("failed to add secret: %w", err)
				}
				// Ensure this secret is in the cache. This is necessary for now because of a corner case like:
				// 1. Client A does a new function call, returns some type that references a SetSecret
				// 2. Client B does the same function call, gets a cache hit
				// 3. Client A disconnects *before Client B has reached this PostCall*
				// 4. Client B tries to access the secret, but it's not in the cache
				// The longer term fix for this type of issue is to have more dagql awareness of edges between
				// cache results such that a function call return value result inherently results in any referenced
				// secrets also staying in cache.
				secretDigest := SecretDigest(secret.inst)
				cacheKeyID := secret.inst.ID().
					WithDigest(secretDigest).
					With(call.WithContentDigest(secretDigest))
				_, err = destDag.Cache.GetOrInitCall(ctx, dagql.CacheKey{ID: cacheKeyID}, func(context.Context) (dagql.AnyResult, error) {
					return secret.inst.WithContentDigest(secretDigest).ObjectResultWithPostCall(postCall), nil
				})
				if err != nil {
					return fmt.Errorf("failed to cache secret: %w", err)
				}
			}
		}
		if len(sockets) > 0 {
			destClientSocketStore, err := query.Sockets(ctx)
			if err != nil {
				return fmt.Errorf("failed to get destination client socket store: %w", err)
			}
			for _, socket := range sockets {
				socketDigest := socket.Self().IDDigest
				if socketDigest == "" {
					socketDigest = SocketIDDigest(socket.ID())
				}
				if socketDigest == "" {
					slog.Warn("skipping socket transfer with empty digest",
						"sourceClientID", sourceClientID,
					)
					continue
				}
				if destClientSocketStore.HasSocket(socketDigest) {
					// Keep destination-local mapping when present; avoid replacing a
					// potentially fresher local socket with one imported from another client.
					continue
				}
				if err := destClientSocketStore.AddSocketFromOtherStore(socket.Self(), srcSocketStore); err != nil {
					slog.Warn("failed to add socket from other store",
						"socket", socketDigest,
						"err", err,
						"sourceClientID", sourceClientID,
					)
					continue
				}
			}
		}

		return nil
	}

	return postCall, hasNamedSecrets, nil
}
