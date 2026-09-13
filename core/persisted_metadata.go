package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/dagger/dagger/dagql"
)

// Codecs for ordinary metadata and report values that a module or core field
// can return and that previously had no persisted representation. Plain data
// members are inline; separately attached results are references.

type persistedEnvVariablePayload struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (v EnvVariable) EncodePersistedObject(context.Context, *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	return encodePersistedObjectPayload(persistedEnvVariablePayload{Name: v.Name, Value: v.Value})
}

func (EnvVariable) DecodePersistedObject(_ context.Context, _ *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedEnvVariablePayload
	if err := unmarshalPersistedPayload(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted env variable payload: %w", err)
	}
	return EnvVariable{Name: persisted.Name, Value: persisted.Value}, nil
}

type persistedPortPayload struct {
	Port                        int             `json:"port"`
	Protocol                    NetworkProtocol `json:"protocol"`
	Description                 *string         `json:"description,omitempty"`
	ExperimentalSkipHealthcheck bool            `json:"experimentalSkipHealthcheck,omitempty"`
}

func (p Port) EncodePersistedObject(context.Context, *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	return encodePersistedObjectPayload(persistedPortPayload{
		Port:                        p.Port,
		Protocol:                    p.Protocol,
		Description:                 p.Description,
		ExperimentalSkipHealthcheck: p.ExperimentalSkipHealthcheck,
	})
}

func (Port) DecodePersistedObject(_ context.Context, _ *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedPortPayload
	if err := unmarshalPersistedPayload(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted port payload: %w", err)
	}
	return Port{
		Port:                        persisted.Port,
		Protocol:                    persisted.Protocol,
		Description:                 persisted.Description,
		ExperimentalSkipHealthcheck: persisted.ExperimentalSkipHealthcheck,
	}, nil
}

type persistedSDKConfigPayload struct {
	Source string `json:"source,omitempty"`
	Debug  bool   `json:"debug,omitempty"`
	// Config and Experimental are plain data the type itself does not
	// normalize, and a module source constructs an empty Experimental map
	// deliberately (core/schema/modulesource.go
	// moduleSourceWithoutExperimentalFeatures). Omitting them would restore
	// an empty map as nil, so they are always written.
	Config       map[string]any  `json:"config"`
	Experimental map[string]bool `json:"experimental"`
}

func (sdk *SDKConfig) EncodePersistedObject(context.Context, *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	if sdk == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted SDK config: nil SDK config")
	}
	return encodePersistedObjectPayload(persistedSDKConfigPayload{
		Source:       sdk.Source,
		Debug:        sdk.Debug,
		Config:       maps.Clone(sdk.Config),
		Experimental: maps.Clone(sdk.Experimental),
	})
}

func (*SDKConfig) DecodePersistedObject(_ context.Context, _ *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedSDKConfigPayload
	// Untyped configuration values keep exact numbers.
	if err := unmarshalPersistedPayload(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted SDK config payload: %w", err)
	}
	return &SDKConfig{
		Source:       persisted.Source,
		Debug:        persisted.Debug,
		Config:       persisted.Config,
		Experimental: persisted.Experimental,
	}, nil
}

type persistedGitBundleRefPayload struct {
	Name string `json:"name"`
	SHA  string `json:"sha"`
}

func (ref *GitBundleRef) EncodePersistedObject(context.Context, *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	if ref == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted git bundle ref: nil ref")
	}
	return encodePersistedObjectPayload(persistedGitBundleRefPayload{Name: ref.Name, SHA: ref.SHA})
}

func (*GitBundleRef) DecodePersistedObject(_ context.Context, _ *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedGitBundleRefPayload
	if err := unmarshalPersistedPayload(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted git bundle ref payload: %w", err)
	}
	return &GitBundleRef{Name: persisted.Name, SHA: persisted.SHA}, nil
}

// persistedSchemaPayload keeps the parsed introspection response as its own
// JSON, which is exactly what Schema.Contents serializes and NewSchema parses.
type persistedSchemaPayload struct {
	Introspection json.RawMessage `json:"introspection"`
}

func (s *Schema) EncodePersistedObject(context.Context, *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	if s == nil || s.Introspection == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted schema: missing introspection")
	}
	contents, err := s.Contents()
	if err != nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted schema: %w", err)
	}
	return encodePersistedObjectPayload(persistedSchemaPayload{Introspection: json.RawMessage(contents)})
}

func (*Schema) DecodePersistedObject(_ context.Context, _ *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedSchemaPayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted schema payload: %w", err)
	}
	schema, err := NewSchema(JSON(persisted.Introspection))
	if err != nil {
		return nil, fmt.Errorf("decode persisted schema: %w", err)
	}
	return schema, nil
}

type persistedCurrentModulePayload struct {
	ModuleResultID uint64 `json:"moduleResultID,omitempty"`
}

func (mod *CurrentModule) EncodePersistedObject(_ context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	if mod == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted current module: nil current module")
	}
	var payload persistedCurrentModulePayload
	if mod.Module.Self() != nil {
		moduleID, err := encodePersistedObjectRef(enc, mod.Module, "current module")
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
		payload.ModuleResultID = moduleID
	}
	return encodePersistedObjectPayload(payload)
}

// DecodePersistedObject restores the exact retained module; it never
// substitutes the caller's current module.
func (*CurrentModule) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedCurrentModulePayload
	if err := unmarshalPersistedPayload(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted current module payload: %w", err)
	}
	module, err := loadPersistedObjectResultByResultID[*Module](ctx, dec, persisted.ModuleResultID, "current module")
	if err != nil {
		return nil, err
	}
	return &CurrentModule{Module: module}, nil
}

// AttachDependencyResults retains the reflected module so its row survives as
// long as this value does.
func (mod *CurrentModule) AttachDependencyResults(
	_ context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if mod == nil || mod.Module.Self() == nil {
		return nil, nil
	}
	attached, err := attach(mod.Module)
	if err != nil {
		return nil, fmt.Errorf("attach current module: %w", err)
	}
	typed, ok := attached.(dagql.ObjectResult[*Module])
	if !ok {
		return nil, fmt.Errorf("attach current module: unexpected result %T", attached)
	}
	mod.Module = typed
	return []dagql.AnyResult{typed}, nil
}

var persistedCurrentModuleVisitor = persistedStructVisitor("", func(p *persistedCurrentModulePayload, w *persistedRefWalker) error {
	return w.child("moduleResultID", &p.ModuleResultID)
})

// Migration reports retain attached Changeset results by reference. Steps
// remain inline metadata, with their own changeset references.
type persistedWorkspaceMigrationStepPayload struct {
	Code            string   `json:"code"`
	Description     string   `json:"description,omitempty"`
	Warnings        []string `json:"warnings,omitempty"`
	ChangesResultID uint64   `json:"changesResultID,omitempty"`
}

type persistedWorkspaceMigrationPayload struct {
	ChangesResultID  uint64                                   `json:"changesResultID,omitempty"`
	ModuleCandidates []string                                 `json:"moduleCandidates,omitempty"`
	ConfigFile       string                                   `json:"configFile,omitempty"`
	Steps            []persistedWorkspaceMigrationStepPayload `json:"steps,omitempty"`
}

func encodePersistedWorkspaceMigrationStep(enc *dagql.PersistEncodeContext, step *WorkspaceMigrationStep) (persistedWorkspaceMigrationStepPayload, error) {
	if step == nil {
		return persistedWorkspaceMigrationStepPayload{}, fmt.Errorf("nil migration step")
	}
	var changes uint64
	if step.Changes.Self() != nil {
		var err error
		changes, err = encodePersistedObjectRef(enc, step.Changes, "workspace migration step changes")
		if err != nil {
			return persistedWorkspaceMigrationStepPayload{}, err
		}
	}
	return persistedWorkspaceMigrationStepPayload{
		Code:            step.Code,
		Description:     step.Description,
		Warnings:        slices.Clone(step.Warnings),
		ChangesResultID: changes,
	}, nil
}

func decodePersistedWorkspaceMigrationStep(ctx context.Context, dec *dagql.PersistDecodeContext, persisted persistedWorkspaceMigrationStepPayload) (*WorkspaceMigrationStep, error) {
	changes, err := loadPersistedObjectResultByResultID[*Changeset](ctx, dec, persisted.ChangesResultID, "workspace migration step changes")
	if err != nil {
		return nil, err
	}
	return &WorkspaceMigrationStep{
		Code:        persisted.Code,
		Description: persisted.Description,
		Warnings:    slices.Clone(persisted.Warnings),
		Changes:     changes,
	}, nil
}

func (step *WorkspaceMigrationStep) EncodePersistedObject(_ context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	payload, err := encodePersistedWorkspaceMigrationStep(enc, step)
	if err != nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted workspace migration step: %w", err)
	}
	return encodePersistedObjectPayload(payload)
}

func (*WorkspaceMigrationStep) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedWorkspaceMigrationStepPayload
	if err := unmarshalPersistedPayload(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted workspace migration step payload: %w", err)
	}
	return decodePersistedWorkspaceMigrationStep(ctx, dec, persisted)
}

func (migration *WorkspaceMigration) EncodePersistedObject(_ context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	if migration == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted workspace migration: nil migration")
	}
	var changes uint64
	if migration.Changes.Self() != nil {
		var err error
		changes, err = encodePersistedObjectRef(enc, migration.Changes, "workspace migration changes")
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
	}
	payload := persistedWorkspaceMigrationPayload{
		ChangesResultID:  changes,
		ModuleCandidates: slices.Clone(migration.ModuleCandidates),
		ConfigFile:       migration.ConfigFile,
		Steps:            make([]persistedWorkspaceMigrationStepPayload, 0, len(migration.Steps)),
	}
	for i, step := range migration.Steps {
		encoded, err := encodePersistedWorkspaceMigrationStep(enc, step)
		if err != nil {
			return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted workspace migration step %d: %w", i, err)
		}
		payload.Steps = append(payload.Steps, encoded)
	}
	return encodePersistedObjectPayload(payload)
}

func (*WorkspaceMigration) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedWorkspaceMigrationPayload
	if err := unmarshalPersistedPayload(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted workspace migration payload: %w", err)
	}
	changes, err := loadPersistedObjectResultByResultID[*Changeset](ctx, dec, persisted.ChangesResultID, "workspace migration changes")
	if err != nil {
		return nil, err
	}
	migration := &WorkspaceMigration{
		Changes:          changes,
		ModuleCandidates: slices.Clone(persisted.ModuleCandidates),
		ConfigFile:       persisted.ConfigFile,
		Steps:            make([]*WorkspaceMigrationStep, 0, len(persisted.Steps)),
	}
	for _, step := range persisted.Steps {
		decoded, err := decodePersistedWorkspaceMigrationStep(ctx, dec, step)
		if err != nil {
			return nil, err
		}
		migration.Steps = append(migration.Steps, decoded)
	}
	return migration, nil
}

var persistedWorkspaceMigrationStepVisitor = persistedStructVisitor("", func(p *persistedWorkspaceMigrationStepPayload, w *persistedRefWalker) error {
	return w.child("changesResultID", &p.ChangesResultID)
})

var persistedWorkspaceMigrationVisitor = persistedStructVisitor("", func(p *persistedWorkspaceMigrationPayload, w *persistedRefWalker) error {
	if err := w.child("changesResultID", &p.ChangesResultID); err != nil {
		return err
	}
	for i := range p.Steps {
		if err := w.at("steps").index(i).child("changesResultID", &p.Steps[i].ChangesResultID); err != nil {
			return err
		}
	}
	return nil
})

// Cloud and the legacy Terminal are empty reported values; their dynamic
// fields keep their current per-call behavior.
func (*Cloud) EncodePersistedObject(context.Context, *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	return encodePersistedObjectRawJSON(json.RawMessage(`{}`)), nil
}

func (*Cloud) DecodePersistedObject(context.Context, *dagql.PersistDecodeContext, json.RawMessage) (dagql.Typed, error) {
	return &Cloud{}, nil
}

func (*TerminalLegacy) EncodePersistedObject(context.Context, *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	return encodePersistedObjectRawJSON(json.RawMessage(`{}`)), nil
}

func (*TerminalLegacy) DecodePersistedObject(context.Context, *dagql.PersistDecodeContext, json.RawMessage) (dagql.Typed, error) {
	return &TerminalLegacy{}, nil
}

// persistedMetadataFamilies registers the ordinary metadata and report
// families alongside the existing inventory.
var persistedMetadataFamilies = []dagql.PersistedObjectFamily{
	{Name: "core.EnvVariable", Typed: EnvVariable{}, Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.Port", Typed: Port{}, Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.SDKConfig", Typed: (*SDKConfig)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.GitBundleRef", Typed: (*GitBundleRef)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.Schema", Typed: (*Schema)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.CurrentModule", Typed: (*CurrentModule)(nil), Visitor: persistedCurrentModuleVisitor},
	{Name: "core.WorkspaceMigration", Typed: (*WorkspaceMigration)(nil), Visitor: persistedWorkspaceMigrationVisitor},
	{Name: "core.WorkspaceMigrationStep", Typed: (*WorkspaceMigrationStep)(nil), Visitor: persistedWorkspaceMigrationStepVisitor},
	{Name: "core.Cloud", Typed: (*Cloud)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.TerminalLegacy", Typed: (*TerminalLegacy)(nil), Visitor: dagql.PersistedNoReferences{}},
}

func init() {
	for _, family := range persistedMetadataFamilies {
		dagql.RegisterPersistedObjectFamily(family)
	}
}
