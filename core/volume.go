package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
)

type VolumeBackendKind string

const (
	VolumeBackendKindSSHFS  VolumeBackendKind = "sshfs"
	VolumeBackendKindEngine VolumeBackendKind = "engine"

	EngineVolumeLayoutVersion = 1
	engineVolumeMaxPathLength = 4096
	engineVolumeMaxNameLength = 255
)

// Volume is an opaque filesystem volume that can be mounted into containers.
type Volume struct {
	Backend VolumeBackendKind
	SSHFS   *SSHFSVolumeConfig
	Engine  *EngineVolumeConfig
}

// EngineVolumeConfig identifies an operator-managed directory below the
// configured engine state root. It deliberately contains no resolved host
// path: resolution happens lazily when an exec mounts the volume.
type EngineVolumeConfig struct {
	Name          string
	Subdir        string
	LayoutVersion int
}

// EngineVolumeState is the engine-local state needed to resolve and mount an
// engine volume. It is supplied by core.Server and is not persisted in a
// Volume.
type EngineVolumeState struct {
	RootDir                    string
	RecursiveReadOnlySupported bool
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

func ValidateEngineVolumeName(name string) error {
	if name == "" {
		return fmt.Errorf("engine volume name must not be empty")
	}
	if len(name) > engineVolumeMaxPathLength {
		return fmt.Errorf("engine volume name must not exceed %d bytes", engineVolumeMaxPathLength)
	}
	for _, component := range strings.Split(name, "/") {
		if component == "" {
			return fmt.Errorf("engine volume name must use non-empty slash-separated components")
		}
		if len(component) > engineVolumeMaxNameLength {
			return fmt.Errorf("engine volume name component %q must not exceed %d bytes", component, engineVolumeMaxNameLength)
		}
		if component == "fs" {
			return fmt.Errorf("engine volume name component %q is reserved", component)
		}
		for i := range len(component) {
			char := component[i]
			valid := char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9'
			if i > 0 {
				valid = valid || char == '.' || char == '_' || char == '-'
			}
			if !valid {
				return fmt.Errorf("engine volume name component %q must match [A-Za-z0-9][A-Za-z0-9._-]*", component)
			}
		}
	}
	return nil
}

func ValidateEngineVolumeSubdir(subdir string) error {
	if subdir == "" {
		return fmt.Errorf("engine volume subdir must not be empty when specified")
	}
	if len(subdir) > engineVolumeMaxPathLength {
		return fmt.Errorf("engine volume subdir must not exceed %d bytes", engineVolumeMaxPathLength)
	}
	if strings.HasPrefix(subdir, "/") || path.Clean(subdir) != subdir {
		return fmt.Errorf("engine volume subdir %q must be a canonical relative path", subdir)
	}
	for _, component := range strings.Split(subdir, "/") {
		if component == "" || component == "." || component == ".." {
			return fmt.Errorf("engine volume subdir %q must use canonical non-traversing components", subdir)
		}
		if len(component) > engineVolumeMaxNameLength {
			return fmt.Errorf("engine volume subdir component %q must not exceed %d bytes", component, engineVolumeMaxNameLength)
		}
		if strings.IndexByte(component, 0) >= 0 {
			return fmt.Errorf("engine volume subdir contains a NUL byte")
		}
	}
	return nil
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
	Backend VolumeBackendKind             `json:"backend"`
	SSHFS   *persistedSSHFSVolumePayload  `json:"sshfs,omitempty"`
	Engine  *persistedEngineVolumePayload `json:"engine,omitempty"`
}

type persistedEngineVolumePayload struct {
	Name          string `json:"name"`
	Subdir        string `json:"subdir,omitempty"`
	LayoutVersion int    `json:"layoutVersion"`
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
	case VolumeBackendKindEngine:
		if vol.Engine == nil {
			return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted volume: missing engine config")
		}
		if err := validateEngineVolumeConfig(vol.Engine); err != nil {
			return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted volume: %w", err)
		}
		payload.Engine = &persistedEngineVolumePayload{
			Name:          vol.Engine.Name,
			Subdir:        vol.Engine.Subdir,
			LayoutVersion: vol.Engine.LayoutVersion,
		}
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
	case VolumeBackendKindEngine:
		if persisted.Engine == nil {
			return nil, fmt.Errorf("decode persisted volume: missing engine payload")
		}
		vol.Engine = &EngineVolumeConfig{
			Name:          persisted.Engine.Name,
			Subdir:        persisted.Engine.Subdir,
			LayoutVersion: persisted.Engine.LayoutVersion,
		}
		if err := validateEngineVolumeConfig(vol.Engine); err != nil {
			return nil, fmt.Errorf("decode persisted volume: %w", err)
		}
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

func validateEngineVolumeConfig(cfg *EngineVolumeConfig) error {
	if cfg.LayoutVersion != EngineVolumeLayoutVersion {
		return fmt.Errorf("unsupported engine volume layout version %d", cfg.LayoutVersion)
	}
	if err := ValidateEngineVolumeName(cfg.Name); err != nil {
		return err
	}
	if cfg.Subdir != "" {
		if err := ValidateEngineVolumeSubdir(cfg.Subdir); err != nil {
			return err
		}
	}
	return nil
}
