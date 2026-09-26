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
