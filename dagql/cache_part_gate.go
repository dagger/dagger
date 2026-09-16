package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
)

// PartGateCell is a stable row attachment. Merely binding it allocates no gate.
type PartGateCell struct {
	gate     atomic.Pointer[PartWriterGate]
	active   atomic.Bool
	hostOnce sync.Once
	host     PartHost
	server   atomic.Pointer[Server]
}

func (cell *PartGateCell) loadOrCreate() *PartWriterGate {
	if gate := cell.gate.Load(); gate != nil {
		return gate
	}
	gate := &PartWriterGate{outputs: make(map[string]partOutputState), groups: make(map[string]*partProducerState), writers: make(map[*writerTicket]*PartPermit)}
	if cell.gate.CompareAndSwap(nil, gate) {
		return gate
	}
	return cell.gate.Load()
}

type ProducerAddress struct {
	OutputPath PersistedRefPath
	Group      LazyGroupKey
}
type ProducerPhase uint8

const (
	ProducerOpen ProducerPhase = iota
	ProducerPreparing
	ProducerRunning
	ProducerConsumed
)

type PartOutputPhase uint8

const (
	PartPending PartOutputPhase = iota
	PartOutputInstalled
	PartComplete
)

type GateOutcome uint8

const (
	GateGranted GateOutcome = iota
	GateBusy
	GateAlreadyInstalled
	GateExecutionStarted
	GateReselect
)

type PartWriterGate struct {
	mu       sync.Mutex
	outputs  map[string]partOutputState
	groups   map[string]*partProducerState
	writers  map[*writerTicket]*PartPermit
	revision uint64
	managed  bool
}
type partOutputState struct {
	phase        PartOutputPhase
	task         *PartTaskToken
	installation uint64
}
type partProducerState struct {
	phase    ProducerPhase
	task     *PartTaskToken
	writeSet []PersistedPartAddress
	drain    *DrainTicket
}
type writerTicket struct{ done chan struct{} }
type PartPermit struct {
	gate     *PartWriterGate
	task     *PartTaskToken
	address  PersistedPartAddress
	ticket   *writerTicket
	decision bool
}
type DrainTicket struct {
	task     *PartTaskToken
	group    ProducerAddress
	writeSet []PersistedPartAddress
	writers  []<-chan struct{}
	gate     *PartWriterGate
}
type OriginalPermit struct {
	task     *PartTaskToken
	group    ProducerAddress
	writeSet []PersistedPartAddress
	gate     *PartWriterGate
}

// SourceCheck is private evidence; only a validated source scan can construct it.
type SourceCheck struct {
	drain          *DrainTicket
	gateRevision   uint64
	candidates     []partCandidate
	lookup         partLookup
	sessionID      string
	demand         *PartDemandState
	demandRevision uint64
	checked        bool
}

func producerAddressKey(address ProducerAddress) string {
	raw, _ := json.Marshal(address)
	return string(raw)
}
func clonePartAddress(address PersistedPartAddress) PersistedPartAddress {
	address.OutputPath = slices.Clone(address.OutputPath)
	return address
}
func containsPart(addresses []PersistedPartAddress, target PersistedPartAddress) bool {
	key, _ := partAddressKey(target)
	for _, address := range addresses {
		candidate, _ := partAddressKey(address)
		if key == candidate {
			return true
		}
	}
	return false
}
func overlapsParts(a, b []PersistedPartAddress) bool {
	for _, address := range a {
		if containsPart(b, address) {
			return true
		}
	}
	return false
}
func (c *Cache) validatePartTaskLocked(receiver AnyResult, task *PartTaskToken) (*sharedResult, error) {
	if receiver == nil || task == nil || task.generation == 0 || task.row == nil || receiver.cacheSharedResult() != task.row || c.resultsByID[task.row.id] != task.row || !task.active.Load() {
		return nil, fmt.Errorf("part writer: invalid receiver or task generation")
	}
	return task.row, nil
}
func (c *Cache) TryAcquire(ctx context.Context, receiver AnyResult, address PersistedPartAddress, task *PartTaskToken) (*PartPermit, GateOutcome, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, GateReselect, err
	}
	key, err := partAddressKey(address)
	if err != nil {
		return nil, GateReselect, err
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	row, err := c.validatePartTaskLocked(receiver, task)
	if err != nil {
		return nil, GateReselect, err
	}
	gate := row.partGate.loadOrCreate()
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.outputs[key].phase != PartPending {
		return nil, GateAlreadyInstalled, nil
	}
	for _, group := range gate.groups {
		if !containsPart(group.writeSet, address) {
			continue
		}
		switch group.phase {
		case ProducerRunning, ProducerConsumed:
			return nil, GateExecutionStarted, nil
		case ProducerPreparing:
			return nil, GateBusy, nil
		}
	}
	for _, permit := range gate.writers {
		if containsPart([]PersistedPartAddress{permit.address}, address) {
			return nil, GateBusy, nil
		}
	}
	gate.managed = true
	row.partGate.active.Store(true)
	return gate.newPermit(task, address, false), GateGranted, nil
}
func (gate *PartWriterGate) newPermit(task *PartTaskToken, address PersistedPartAddress, decision bool) *PartPermit {
	ticket := &writerTicket{done: make(chan struct{})}
	permit := &PartPermit{gate: gate, task: task, address: clonePartAddress(address), ticket: ticket, decision: decision}
	gate.writers[ticket] = permit
	gate.revision++
	return permit
}
func (permit *PartPermit) Release() {
	if permit == nil {
		return
	}
	permit.gate.mu.Lock()
	defer permit.gate.mu.Unlock()
	permit.releaseLocked()
}
func (permit *PartPermit) releaseLocked() {
	if _, ok := permit.gate.writers[permit.ticket]; !ok {
		return
	}
	delete(permit.gate.writers, permit.ticket)
	close(permit.ticket.done)
	permit.gate.revision++
}
func (c *Cache) PrepareOriginal(ctx context.Context, receiver AnyResult, group ProducerAddress, writeSet []PersistedPartAddress, task *PartTaskToken) (*DrainTicket, GateOutcome, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, GateReselect, err
	}
	if len(writeSet) == 0 {
		return nil, GateReselect, fmt.Errorf("producer: empty write set")
	}
	for _, address := range writeSet {
		if _, err := partAddressKey(address); err != nil {
			return nil, GateReselect, err
		}
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	row, err := c.validatePartTaskLocked(receiver, task)
	if err != nil {
		return nil, GateReselect, err
	}
	gate := row.partGate.loadOrCreate()
	gate.mu.Lock()
	defer gate.mu.Unlock()
	for _, current := range gate.groups {
		if current.phase != ProducerOpen && overlapsParts(current.writeSet, writeSet) {
			if current.task == task && current.phase == ProducerPreparing {
				return current.drain, GateGranted, nil
			}
			if current.phase == ProducerConsumed {
				return nil, GateExecutionStarted, nil
			}
			return nil, GateBusy, nil
		}
	}
	drain := &DrainTicket{task: task, group: ProducerAddress{OutputPath: slices.Clone(group.OutputPath), Group: group.Group}, gate: gate}
	for _, address := range writeSet {
		drain.writeSet = append(drain.writeSet, clonePartAddress(address))
	}
	for ticket, writer := range gate.writers {
		if containsPart(writeSet, writer.address) {
			drain.writers = append(drain.writers, ticket.done)
		}
	}
	gate.groups[producerAddressKey(group)] = &partProducerState{phase: ProducerPreparing, task: task, writeSet: drain.writeSet, drain: drain}
	gate.revision++
	return drain, GateGranted, nil
}
func (drain *DrainTicket) Wait(ctx context.Context) error {
	if drain == nil {
		return fmt.Errorf("producer: missing drain")
	}
	for _, done := range drain.writers {
		select {
		case <-done:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	return context.Cause(ctx)
}
func (drain *DrainTicket) currentLocked() bool {
	current := drain.gate.groups[producerAddressKey(drain.group)]
	return current != nil && current.drain == drain && current.task == drain.task && current.phase == ProducerPreparing && drain.task.active.Load()
}
func (drain *DrainTicket) drainedLocked() bool {
	for _, writer := range drain.gate.writers {
		if containsPart(drain.writeSet, writer.address) {
			return false
		}
	}
	return true
}
func (c *Cache) TryAcquireForDecision(ctx context.Context, receiver AnyResult, address PersistedPartAddress, drain *DrainTicket, task *PartTaskToken) (*PartPermit, GateOutcome, error) {
	if _, err := partAddressKey(address); err != nil {
		return nil, GateReselect, err
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	row, err := c.validatePartTaskLocked(receiver, task)
	if err != nil {
		return nil, GateReselect, err
	}
	if drain == nil || drain.task != task || drain.gate != row.partGate.gate.Load() || !containsPart(drain.writeSet, address) {
		return nil, GateReselect, fmt.Errorf("producer: invalid decision permit")
	}
	gate := drain.gate
	gate.mu.Lock()
	defer gate.mu.Unlock()
	key, _ := partAddressKey(address)
	if gate.outputs[key].phase != PartPending {
		return nil, GateAlreadyInstalled, nil
	}
	if !drain.currentLocked() || !drain.drainedLocked() {
		return nil, GateReselect, nil
	}
	return gate.newPermit(task, address, true), GateGranted, nil
}
func (c *Cache) BeginOriginal(ctx context.Context, check *SourceCheck) (*OriginalPermit, GateOutcome, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, GateReselect, err
	}
	if check == nil || check.drain == nil {
		return nil, GateReselect, fmt.Errorf("producer: missing source check")
	}
	drain := check.drain
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	if c.resultsByID[drain.task.row.id] != drain.task.row {
		return nil, GateReselect, fmt.Errorf("producer: unregistered receiver")
	}
	if !c.sourceCheckCurrentLocked(check) {
		return nil, GateReselect, nil
	}
	gate := drain.gate
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if !drain.currentLocked() || !drain.drainedLocked() || gate.revision != check.gateRevision {
		return nil, GateReselect, nil
	}
	gate.groups[producerAddressKey(drain.group)].phase = ProducerRunning
	gate.revision++
	return &OriginalPermit{task: drain.task, group: drain.group, writeSet: drain.writeSet, gate: gate}, GateGranted, nil
}

// EndBody runs only after the body and its preparations have stopped. A source
// installed by a decision reopens sibling admission even during bookkeeping.
func (c *Cache) endPartTaskBody(task *PartTaskToken, success bool) {
	if !task.active.Swap(false) {
		return
	}
	gate := task.row.partGate.gate.Load()
	if gate == nil {
		return
	}
	c.egraphMu.Lock()
	gate.mu.Lock()
	for _, group := range gate.groups {
		if group.task != task {
			continue
		}
		if success && (group.phase == ProducerRunning || group.phase == ProducerConsumed) {
			group.phase = ProducerConsumed
		} else {
			group.phase = ProducerOpen
			group.writeSet = nil
			group.drain = nil
		}
		gate.revision++
	}
	gate.mu.Unlock()
	c.egraphMu.Unlock()
}
