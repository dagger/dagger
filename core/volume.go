package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
)

type VolumeBackendKind string

const (
	VolumeBackendKindSSHFS VolumeBackendKind = "sshfs"
)

// Volume is an opaque filesystem volume that can be mounted into containers.
type Volume struct {
	Backend VolumeBackendKind
	SSHFS   *SSHFSVolumeConfig
}

type SSHFSVolumeConfig struct {
	Endpoint                 string
	PrivateKey               dagql.ObjectResult[*Secret]
	KnownHosts               dagql.ObjectResult[*Secret]
	InsecureSkipHostKeyCheck bool
	// HostKeyAlias keeps host-key verification anchored to the user-facing
	// endpoint when the connection host is later rewritten through a service.
	HostKeyAlias string
	ServiceHost  dagql.ObjectResult[*Service]
}

func (*Volume) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Volume",
		NonNull:   true,
	}
}

func (*Volume) TypeDescription() string {
	return "A filesystem volume that can be mounted into containers."
}

// VolumeContentDigestFromCacheKey returns opt-in cache equivalence evidence.
// It is deliberately not a session-resource handle.
func VolumeContentDigestFromCacheKey(cacheKey string) digest.Digest {
	return hashutil.HashStrings(cacheKey)
}

func (vol *Volume) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if vol == nil || vol.SSHFS == nil {
		return nil, nil
	}

	var owned []dagql.AnyResult
	if vol.SSHFS.PrivateKey.Self() != nil {
		attached, err := attach(vol.SSHFS.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("attach volume private key: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*Secret])
		if !ok {
			return nil, fmt.Errorf("attach volume private key: unexpected result %T", attached)
		}
		vol.SSHFS.PrivateKey = typed
		owned = append(owned, typed)
	}
	if vol.SSHFS.KnownHosts.Self() != nil {
		attached, err := attach(vol.SSHFS.KnownHosts)
		if err != nil {
			return nil, fmt.Errorf("attach volume known hosts: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*Secret])
		if !ok {
			return nil, fmt.Errorf("attach volume known hosts: unexpected result %T", attached)
		}
		vol.SSHFS.KnownHosts = typed
		owned = append(owned, typed)
	}
	if vol.SSHFS.ServiceHost.Self() != nil {
		attached, err := attach(vol.SSHFS.ServiceHost)
		if err != nil {
			return nil, fmt.Errorf("attach volume service host: %w", err)
		}
		typed, ok := attached.(dagql.ObjectResult[*Service])
		if !ok {
			return nil, fmt.Errorf("attach volume service host: unexpected result %T", attached)
		}
		vol.SSHFS.ServiceHost = typed
		owned = append(owned, typed)
	}
	return owned, nil
}

type persistedVolumePayload struct {
	Backend VolumeBackendKind            `json:"backend"`
	SSHFS   *persistedSSHFSVolumePayload `json:"sshfs,omitempty"`
}

type persistedSSHFSVolumePayload struct {
	Endpoint                 string `json:"endpoint"`
	PrivateKeyResultID       uint64 `json:"privateKeyResultID"`
	KnownHostsResultID       uint64 `json:"knownHostsResultID,omitempty"`
	InsecureSkipHostKeyCheck bool   `json:"insecureSkipHostKeyCheck,omitempty"`
	HostKeyAlias             string `json:"hostKeyAlias,omitempty"`
	ServiceHostResultID      uint64 `json:"serviceHostResultID,omitempty"`
}

func (vol *Volume) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	if vol == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted volume: nil volume")
	}
	payload := persistedVolumePayload{
		Backend: vol.Backend,
	}
	switch vol.Backend {
	case VolumeBackendKindSSHFS:
		if vol.SSHFS == nil {
			return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted volume: missing sshfs config")
		}
		privateKeyID, err := encodePersistedObjectRef(cache, vol.SSHFS.PrivateKey, "volume private key")
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
		sshfs := &persistedSSHFSVolumePayload{
			Endpoint:                 vol.SSHFS.Endpoint,
			PrivateKeyResultID:       privateKeyID,
			InsecureSkipHostKeyCheck: vol.SSHFS.InsecureSkipHostKeyCheck,
			HostKeyAlias:             vol.SSHFS.HostKeyAlias,
		}
		if vol.SSHFS.KnownHosts.Self() != nil {
			knownHostsID, err := encodePersistedObjectRef(cache, vol.SSHFS.KnownHosts, "volume known hosts")
			if err != nil {
				return dagql.PersistedObjectEncoding{}, err
			}
			sshfs.KnownHostsResultID = knownHostsID
		}
		if vol.SSHFS.ServiceHost.Self() != nil {
			serviceID, err := encodePersistedObjectRef(cache, vol.SSHFS.ServiceHost, "volume service host")
			if err != nil {
				return dagql.PersistedObjectEncoding{}, err
			}
			sshfs.ServiceHostResultID = serviceID
		}
		payload.SSHFS = sshfs
	default:
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted volume: unsupported backend %q", vol.Backend)
	}
	return encodePersistedObjectPayload(payload)
}

func (*Volume) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedVolumePayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted volume payload: %w", err)
	}
	vol := &Volume{Backend: persisted.Backend}
	switch persisted.Backend {
	case VolumeBackendKindSSHFS:
		if persisted.SSHFS == nil {
			return nil, fmt.Errorf("decode persisted volume: missing sshfs payload")
		}
		privateKey, err := loadPersistedObjectResultByResultID[*Secret](ctx, dag, persisted.SSHFS.PrivateKeyResultID, "volume private key")
		if err != nil {
			return nil, err
		}
		var knownHosts dagql.ObjectResult[*Secret]
		if persisted.SSHFS.KnownHostsResultID != 0 {
			knownHosts, err = loadPersistedObjectResultByResultID[*Secret](ctx, dag, persisted.SSHFS.KnownHostsResultID, "volume known hosts")
			if err != nil {
				return nil, err
			}
		}
		var serviceHost dagql.ObjectResult[*Service]
		if persisted.SSHFS.ServiceHostResultID != 0 {
			serviceHost, err = loadPersistedObjectResultByResultID[*Service](ctx, dag, persisted.SSHFS.ServiceHostResultID, "volume service host")
			if err != nil {
				return nil, err
			}
		}
		vol.SSHFS = &SSHFSVolumeConfig{
			Endpoint:                 persisted.SSHFS.Endpoint,
			PrivateKey:               privateKey,
			KnownHosts:               knownHosts,
			InsecureSkipHostKeyCheck: persisted.SSHFS.InsecureSkipHostKeyCheck,
			HostKeyAlias:             persisted.SSHFS.HostKeyAlias,
			ServiceHost:              serviceHost,
		}
	default:
		return nil, fmt.Errorf("decode persisted volume: unsupported backend %q", persisted.Backend)
	}
	return vol, nil
}
