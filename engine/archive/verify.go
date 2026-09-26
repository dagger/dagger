package archive

import (
	"fmt"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/agentcontrol"
)

// Completion is the final roster the producer witnessed when the archive was
// sealed: the agents and subscriptions a restore installs from the bootstrap.
type Completion struct {
	Agents        []AgentRevision        `json:"agents"`
	Subscriptions []SubscriptionRevision `json:"subscriptions"`
}
type AgentRevision struct {
	Key      agentcontrol.Key
	Revision int64
}
type SubscriptionRevision struct {
	Key      agentcontrol.EdgeKey
	Revision int64
}

func Witness(want agentcontrol.Expectation) Completion {
	out := Completion{}
	for k, r := range want.Agents {
		out.Agents = append(out.Agents, AgentRevision{k, r})
	}
	for k, r := range want.Subscriptions {
		out.Subscriptions = append(out.Subscriptions, SubscriptionRevision{k, r})
	}
	return out
}

// VerifyClosure loads the recipe closure of the anchors without evaluating any
// recipe, failing on a missing or cyclic dependency. The returned payloads are
// exactly the dependency closure of the anchors.
func VerifyClosure(roots []string, load func(string) (*callpbv1.Call, error)) (map[string]*callpbv1.Call, error) {
	calls := map[string]*callpbv1.Call{}
	visiting := map[string]bool{}
	var visit func(string) error
	visit = func(d string) error {
		if visiting[d] {
			return fmt.Errorf("cyclic call recipe %s", d)
		}
		if calls[d] != nil {
			return nil
		}
		c, err := load(d)
		if err != nil {
			return err
		}
		refs, err := call.RecipeReferences(c)
		if err != nil {
			return fmt.Errorf("call %s: %w", d, err)
		}
		visiting[d] = true
		for _, ref := range refs {
			if err := visit(ref); err != nil {
				return err
			}
		}
		delete(visiting, d)
		calls[d] = c
		return nil
	}
	for _, root := range roots {
		if err := visit(root); err != nil {
			return nil, err
		}
	}
	return calls, nil
}
