// Package agentcontrol defines the authoritative, revisioned agent telemetry
// projection shared by producers, live consumers and archive verification.
// It contains no runtime lookup or discovery API.
package agentcontrol

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"
)

const Version = 1

// Namespace separates independent revision sequences. Restoring a handle in a
// new session creates a new incarnation; its revision is never compared with
// the revision from the source session.
type Namespace struct {
	Session     string
	Trace       string
	Incarnation string
}

type Key struct {
	Namespace
	Handle string
}

// Agent is a complete projection, not a delta. A failed capture clears Digest:
// an earlier conversation must never masquerade as the new committed tip.
type Agent struct {
	Key
	Revision         int64
	Name             string
	Parent           string
	CallDigest       string
	Digest           string
	CaptureError     string
	State            string
	WaitingOn        string
	StopReason       string
	PreTeardownState string
	Failure          string
	Activity         time.Time
}

type EdgeKey struct {
	Namespace
	Watched    string
	Subscriber string
}

// Subscription replaces the entire state filter. An empty filter is a removal
// tombstone, retained so importing old history cannot resurrect the edge.
type Subscription struct {
	EdgeKey
	Revision int64
	States   []string
}

func (ns Namespace) Validate() error {
	if ns.Session == "" || ns.Trace == "" || ns.Incarnation == "" {
		return errors.New("agent control namespace is incomplete")
	}
	return nil
}

func validState(state string) bool {
	switch state {
	case "IDLE", "RUNNING", "WAITING_INPUT", "PAUSED", "FAILED", "STOPPED":
		return true
	default:
		return false
	}
}

func (a Agent) Validate() error {
	if err := a.Namespace.Validate(); err != nil {
		return err
	}
	if a.Handle == "" || a.Revision <= 0 {
		return errors.New("agent control identity or revision is missing")
	}
	if !validState(a.State) {
		return fmt.Errorf("unknown agent state %q", a.State)
	}
	if (a.Digest == "") == (a.CaptureError == "") {
		return errors.New("agent capture requires exactly one of digest or error")
	}
	if a.State == "STOPPED" {
		switch a.StopReason {
		case "EXPLICIT":
		case "SESSION":
			if !validState(a.PreTeardownState) {
				return errors.New("session stop has no valid pre-teardown state")
			}
		default:
			return fmt.Errorf("unknown agent stop reason %q", a.StopReason)
		}
	} else if a.StopReason != "" {
		return errors.New("stop reason on non-stopped agent")
	}
	if a.Parent == a.Handle {
		return errors.New("agent is its own parent")
	}
	return nil
}

func (a Agent) RestoreState() (string, error) {
	if err := a.Validate(); err != nil {
		return "", err
	}
	if a.CaptureError != "" {
		return "", fmt.Errorf("agent %q capture failed: %s", a.Handle, a.CaptureError)
	}
	return a.lifecycleState()
}

// ClosureRoot is the committed conversation leaf whose complete recipe closure
// an archive must carry. A recorded capture failure has none (Validate makes
// that the only way Digest is empty): it is still witnessed, but restore
// carries it as unrestorable.
func (a Agent) ClosureRoot() (string, bool) {
	if a.Digest == "" {
		return "", false
	}
	return a.Digest, true
}

// lifecycleState maps the recorded lifecycle facts to the state a restore
// resumes in, independently of whether a conversation was captured.
func (a Agent) lifecycleState() (string, error) {
	state := a.State
	if state == "STOPPED" && a.StopReason == "SESSION" {
		state = a.PreTeardownState
	}
	if state == "RUNNING" || state == "WAITING_INPUT" {
		state = "IDLE"
	}
	if state == "FAILED" && a.Failure == "" {
		return "", errors.New("failed agent has no recorded failure")
	}
	return state, nil
}

func (s Subscription) Validate() error {
	if err := s.Namespace.Validate(); err != nil {
		return err
	}
	if s.Watched == "" || s.Subscriber == "" || s.Watched == s.Subscriber || s.Revision <= 0 {
		return errors.New("invalid agent subscription identity or revision")
	}
	for i, state := range s.States {
		if !validState(state) {
			return fmt.Errorf("unknown subscription state %q", state)
		}
		if slices.Contains(s.States[:i], state) {
			return fmt.Errorf("duplicate subscription state %q", state)
		}
	}
	return nil
}

// Index is a materialized view of control telemetry. Owners provide locking.
// Maps are private so every update uses the same revision and validation rules.
// Invalid and equivocal records fail closed, including during historical import.
type Index struct {
	agents        map[Key]Agent
	subscriptions map[EdgeKey]Subscription
}

func (idx *Index) ApplyAgent(a Agent) (bool, error) {
	a.Activity = a.Activity.Round(0).UTC()
	if err := a.Validate(); err != nil {
		return false, err
	}
	if old, ok := idx.agents[a.Key]; ok {
		if a.Revision < old.Revision {
			return false, nil
		}
		if a.Revision == old.Revision {
			if !reflect.DeepEqual(a, old) {
				return false, fmt.Errorf("conflicting agent revision %d for %s", a.Revision, a.Handle)
			}
			return false, nil
		}
	}
	if idx.agents == nil {
		idx.agents = map[Key]Agent{}
	}
	idx.agents[a.Key] = a
	return true, nil
}

func (idx *Index) ApplySubscription(s Subscription) (bool, error) {
	if err := s.Validate(); err != nil {
		return false, err
	}
	s.States = slices.Clone(s.States)
	if len(s.States) == 0 {
		s.States = nil
	}
	slices.Sort(s.States)
	if old, ok := idx.subscriptions[s.EdgeKey]; ok {
		if s.Revision < old.Revision {
			return false, nil
		}
		if s.Revision == old.Revision {
			if !reflect.DeepEqual(s, old) {
				return false, fmt.Errorf("conflicting subscription revision %d", s.Revision)
			}
			return false, nil
		}
	}
	if idx.subscriptions == nil {
		idx.subscriptions = map[EdgeKey]Subscription{}
	}
	idx.subscriptions[s.EdgeKey] = s
	return true, nil
}

func (idx *Index) Agents() []Agent {
	out := make([]Agent, 0, len(idx.agents))
	for _, a := range idx.agents {
		out = append(out, a)
	}
	slices.SortFunc(out, func(a, b Agent) int { return compareKey(a.Key, b.Key) })
	return out
}

func compareKey(a, b Key) int {
	return slices.Compare([]string{a.Session, a.Trace, a.Incarnation, a.Handle}, []string{b.Session, b.Trace, b.Incarnation, b.Handle})
}

func (idx *Index) Subscriptions() []Subscription {
	out := make([]Subscription, 0, len(idx.subscriptions))
	for _, s := range idx.subscriptions {
		s.States = slices.Clone(s.States)
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b Subscription) int {
		if c := compareKey(Key{a.Namespace, a.Watched}, Key{b.Namespace, b.Watched}); c != 0 {
			return c
		}
		return slices.Compare([]string{a.Subscriber}, []string{b.Subscriber})
	})
	return out
}

// Expectation is supplied by quiesced producers, never inferred from received
// records. It witnesses identities and final revisions, not a second state store.
// It must already be scoped to the archive reader's telemetry visibility.
type Expectation struct {
	Agents        map[Key]int64
	Subscriptions map[EdgeKey]int64
}

// Verify checks roster/graph completeness. Payload closure and persistence are
// separate checks: success here alone never authorizes sealing an archive.
func (idx *Index) Verify(want Expectation) error {
	if len(idx.agents) != len(want.Agents) || len(idx.subscriptions) != len(want.Subscriptions) {
		return errors.New("agent control roster or subscription count differs from producer expectation")
	}
	for key, revision := range want.Agents {
		a, ok := idx.agents[key]
		if !ok || a.Revision != revision {
			return fmt.Errorf("missing final revision %d for agent %q", revision, key.Handle)
		}
		// A recorded capture failure is a witnessed final fact, not missing
		// data: it is carried to restore as unrestorable rather than making
		// the whole roster unverifiable. Lifecycle facts remain strict.
		if _, err := a.lifecycleState(); err != nil {
			return err
		}
		if a.Parent != "" {
			if _, ok := idx.agents[Key{a.Namespace, a.Parent}]; !ok {
				return fmt.Errorf("parent %q is outside restore roster", a.Parent)
			}
		}
	}
	for key := range want.Agents {
		seen := map[Key]bool{}
		for current := key; current.Handle != ""; {
			if seen[current] {
				return fmt.Errorf("agent parent cycle at %q", current.Handle)
			}
			seen[current] = true
			a := idx.agents[current]
			current = Key{a.Namespace, a.Parent}
		}
	}
	for key, revision := range want.Subscriptions {
		s, ok := idx.subscriptions[key]
		if !ok || s.Revision != revision {
			return fmt.Errorf("missing final subscription revision %d", revision)
		}
		for _, handle := range []string{s.Watched, s.Subscriber} {
			if _, ok := idx.agents[Key{s.Namespace, handle}]; !ok {
				return fmt.Errorf("subscription endpoint %q is outside restore roster", handle)
			}
		}
	}
	return nil
}
