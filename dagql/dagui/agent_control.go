package dagui

import (
	"errors"

	"github.com/dagger/dagger/engine/agentcontrol"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// AgentControl returns a copied view of the canonical control index, including
// subscription removal tombstones. It does not infer finality from the received
// records: strict restore still needs the archive's independent verified
// manifest.
// Like Agents, this method is called under the frontend's DB ownership lock.
func (db *DB) AgentControl() ([]agentcontrol.Agent, []agentcontrol.Subscription, error) {
	return db.agentControl.Agents(), db.agentControl.Subscriptions(), db.agentControlErr
}

// RestoreEntryFromControl maps a canonical agent control record to its
// restore plan entry. A record that cannot be mapped to a restore state is
// carried as unrestorable (Err) rather than failing the plan, so a restore
// skips exactly that agent. A stopped failure keeps its diagnostic in the
// record, but spawn only accepts an error when restoring FAILED.
func RestoreEntryFromControl(a agentcontrol.Agent) AgentRestore {
	state, err := a.RestoreState()
	failure := ""
	if state == "FAILED" {
		failure = a.Failure
	}
	return AgentRestore{
		Source: a.Key, ID: a.Handle, Name: a.Name, State: state, Error: failure,
		SnapshotDigest: a.Digest, ParentAgentID: a.Parent, LastActivity: a.Activity, Err: err,
	}
}

func (db *DB) ingestAgentControl(record sdklog.Record) bool {
	if !agentcontrol.IsRecord(record) {
		return false
	}
	changed, err := db.agentControl.ApplyRecord(record)
	if err != nil {
		// A malformed or equivocal version is still control data: keep it
		// out of text rendering and surface it through AgentControl.
		db.agentControlErr = errors.Join(db.agentControlErr, err)
	}
	if changed || err != nil {
		db.mutations++
	}
	return true
}
