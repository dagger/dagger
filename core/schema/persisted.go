package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/dagger/dagger/dagql"
)

// Label and HealthcheckConfig are declared by this package and returned from
// Container labels and healthcheck reads, so their persisted representation
// and reference visitor are registered here, at initialization, independently
// of which schema a session installs.

type persistedLabelPayload struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

var (
	_ dagql.PersistedObject        = Label{}
	_ dagql.PersistedObjectDecoder = Label{}
	_ dagql.PersistedObject        = HealthcheckConfig{}
	_ dagql.PersistedObjectDecoder = HealthcheckConfig{}
)

func (label Label) EncodePersistedObject(context.Context, *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	raw, err := json.Marshal(persistedLabelPayload{Name: label.Name, Value: label.Value})
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	return dagql.PersistedObjectEncoding{JSON: raw}, nil
}

func (Label) DecodePersistedObject(_ context.Context, _ *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedLabelPayload
	if err := dagql.UnmarshalLosslessJSON(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted label payload: %w", err)
	}
	return Label{Name: persisted.Name, Value: persisted.Value}, nil
}

type persistedHealthcheckConfigPayload struct {
	Args          []string `json:"args,omitempty"`
	Shell         bool     `json:"shell,omitempty"`
	Timeout       string   `json:"timeout,omitempty"`
	Interval      string   `json:"interval,omitempty"`
	StartPeriod   string   `json:"startPeriod,omitempty"`
	StartInterval string   `json:"startInterval,omitempty"`
	Retries       int      `json:"retries,omitempty"`
}

func (cfg HealthcheckConfig) EncodePersistedObject(context.Context, *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	raw, err := json.Marshal(persistedHealthcheckConfigPayload{
		Args:          slices.Clone(cfg.Args),
		Shell:         cfg.Shell,
		Timeout:       cfg.Timeout,
		Interval:      cfg.Interval,
		StartPeriod:   cfg.StartPeriod,
		StartInterval: cfg.StartInterval,
		Retries:       cfg.Retries,
	})
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	return dagql.PersistedObjectEncoding{JSON: raw}, nil
}

func (HealthcheckConfig) DecodePersistedObject(_ context.Context, _ *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedHealthcheckConfigPayload
	if err := dagql.UnmarshalLosslessJSON(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted healthcheck config payload: %w", err)
	}
	return HealthcheckConfig{
		Args:          slices.Clone(persisted.Args),
		Shell:         persisted.Shell,
		Timeout:       persisted.Timeout,
		Interval:      persisted.Interval,
		StartPeriod:   persisted.StartPeriod,
		StartInterval: persisted.StartInterval,
		Retries:       persisted.Retries,
	}, nil
}

func init() {
	dagql.RegisterPersistedObjectFamily(dagql.PersistedObjectFamily{Name: "schema.Label", Typed: Label{}, Visitor: dagql.PersistedNoReferences{}})
	dagql.RegisterPersistedObjectFamily(dagql.PersistedObjectFamily{Name: "schema.HealthcheckConfig", Typed: HealthcheckConfig{}, Visitor: dagql.PersistedNoReferences{}})
}
