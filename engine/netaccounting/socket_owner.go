package netaccounting

import (
	"errors"
	"sync/atomic"
	"syscall"
)

// Owner identifies the trusted source of a network connection.
type Owner uint8

const (
	OwnerDaggerland Owner = 1
	OwnerUserland   Owner = 2
)

var ErrOwnerRequired = errors.New("network realm is required")

func (owner Owner) Valid() bool {
	return owner == OwnerDaggerland || owner == OwnerUserland
}

// SocketTagger records the owner of a socket before traffic is sent.
type SocketTagger interface {
	SetSocketOwner(syscall.Conn, Owner) error
	SetSocketOwnerBeforeConnect(syscall.RawConn, Owner) error
}

type socketTaggerHolder struct {
	tagger SocketTagger
}

var activeSocketTagger atomic.Pointer[socketTaggerHolder]

// RegisterSocketTagger installs the engine's socket tagger. The returned
// cleanup only removes the tagger installed by this call.
func RegisterSocketTagger(tagger SocketTagger) func() {
	holder := &socketTaggerHolder{tagger: tagger}
	activeSocketTagger.Store(holder)
	return func() {
		activeSocketTagger.CompareAndSwap(holder, nil)
	}
}

// SetSocketOwner records owner when network accounting is active.
func SetSocketOwner(conn syscall.Conn, owner Owner) error {
	if !owner.Valid() {
		return ErrOwnerRequired
	}
	holder := activeSocketTagger.Load()
	if holder == nil {
		return nil
	}
	return holder.tagger.SetSocketOwner(conn, owner)
}

// SetSocketOwnerBeforeConnect records owner when network accounting is active.
func SetSocketOwnerBeforeConnect(raw syscall.RawConn, owner Owner) error {
	if !owner.Valid() {
		return ErrOwnerRequired
	}
	holder := activeSocketTagger.Load()
	if holder == nil {
		return nil
	}
	return holder.tagger.SetSocketOwnerBeforeConnect(raw, owner)
}
