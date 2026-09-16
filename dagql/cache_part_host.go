package dagql

import (
	"context"
	"fmt"
)

// HasPartHost binds a typed adapter to the stable owning row. Binding never
// creates a gate and decoding another representation keeps the same host.
type HasPartHost interface{ BindPartHost(*PartHost) }
type PartHost struct {
	cache *Cache
	row   *sharedResult
}

func (c *Cache) bindPartHost(row *sharedResult, result AnyResult) {
	if value, ok := UnwrapAs[HasPartHost](result); ok {
		row.partGate.hostOnce.Do(func() { row.partGate.host = PartHost{cache: c, row: row} })
		value.BindPartHost(&row.partGate.host)
	}
}
func (host *PartHost) Evaluate(ctx context.Context, parts ...PartKey) error {
	return host.cache.EvaluateParts(ctx, Result[Typed]{shared: host.row}, parts...)
}
func (host *PartHost) Admitted(ctx context.Context) bool {
	token := PartTaskFromContext(ctx)
	return token != nil && token.row == host.row && token.active.Load()
}

// RunNative enters the gate before the body takes any core latch. The raw
// callback remains the only owner of native completion and consumption.
func (host *PartHost) RunNative(ctx context.Context, group LazyGroupKey, parts []PartKey, body func(context.Context) error) error {
	if !host.Admitted(ctx) {
		return host.Evaluate(ctx, parts...)
	}
	token := PartTaskFromContext(ctx)
	c := host.cache
	c.egraphMu.Lock()
	gate := host.row.partGate.loadOrCreate()
	gate.mu.Lock()
	groupAddress := ProducerAddress{Group: group}
	key := producerAddressKey(groupAddress)
	if gate.managed {
		gate.mu.Unlock()
		c.egraphMu.Unlock()
		return fmt.Errorf("native body on acquisition-managed result")
	}
	writes := make([]PersistedPartAddress, len(parts))
	for i, part := range parts {
		writes[i] = PersistedPartAddress{Part: part}
	}
	for _, writer := range gate.writers {
		if containsPart(writes, writer.address) {
			gate.mu.Unlock()
			c.egraphMu.Unlock()
			return ErrLazyTaskBusy
		}
	}
	gate.groups[key] = &partProducerState{phase: ProducerRunning, task: token, writeSet: writes}
	gate.revision++
	gate.mu.Unlock()
	c.egraphMu.Unlock()
	err := body(ctx)
	// Native core callbacks have released their latches before this publication.
	c.egraphMu.Lock()
	gate.mu.Lock()
	current := gate.groups[key]
	if current.task == token {
		if err == nil {
			current.phase = ProducerConsumed
			installed := &InstalledOutputs{}
			for _, address := range writes {
				output, _ := partAddressKey(address)
				gate.outputs[output] = partOutputState{phase: PartOutputInstalled, task: token, installation: token.generation}
				installed.outputs = append(installed.outputs, installedPartOutput{address: clonePartAddress(address), installation: token.generation})
			}
			token.installed.Store(installed)
		} else {
			current.phase = ProducerOpen
		}
		gate.revision++
	}
	gate.mu.Unlock()
	c.egraphMu.Unlock()
	return err
}

// Only the owning native attempt may advance installed output to complete,
// after owner sync and operation-lease cleanup have both succeeded.
func (c *Cache) completeNativePartTask(task *PartTaskToken) {
	installed := task.installed.Load()
	if installed == nil {
		return
	}
	gate := task.row.partGate.gate.Load()
	if gate == nil {
		return
	}
	c.egraphMu.Lock()
	gate.mu.Lock()
	for _, output := range installed.outputs {
		key, _ := partAddressKey(output.address)
		state := gate.outputs[key]
		if state.task == task && state.installation == output.installation && state.phase == PartOutputInstalled {
			state.phase = PartComplete
			gate.outputs[key] = state
			gate.revision++
		}
	}
	gate.mu.Unlock()
	c.egraphMu.Unlock()
}
