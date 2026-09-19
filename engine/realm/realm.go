// Package realm separates trusted Dagger traffic from user-requested traffic.
package realm

import (
	"context"
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/dagger/dagger/engine/netaccounting"
)

// Realm identifies why Dagger opened a connection. Connections may be reused
// and multiplexed within one realm, but never across realms.
type Realm uint8

var ErrRequired = netaccounting.ErrOwnerRequired

const (
	Unattributed Realm = iota
	Daggerland
	Userland
)

type contextKey struct{}

func With(ctx context.Context, realm Realm) context.Context {
	return context.WithValue(ctx, contextKey{}, realm)
}

// WithDefault selects realm only when trusted code has not already done so.
func WithDefault(ctx context.Context, realm Realm) context.Context {
	if FromContext(ctx) != Unattributed {
		return ctx
	}
	return With(ctx, realm)
}

func FromContext(ctx context.Context) Realm {
	realm, _ := ctx.Value(contextKey{}).(Realm)
	return realm
}

func (realm Realm) Valid() bool {
	return realm == Daggerland || realm == Userland
}

func (realm Realm) accountingOwner() netaccounting.Owner {
	switch realm {
	case Daggerland:
		return netaccounting.OwnerDaggerland
	case Userland:
		return netaccounting.OwnerUserland
	default:
		return 0
	}
}

// Dialer exposes only dial operations whose sockets belong to one realm.
type Dialer struct {
	base net.Dialer
}

func (dialer *Dialer) Dial(network, address string) (net.Conn, error) {
	return dialer.base.Dial(network, address)
}

func (dialer *Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return dialer.base.DialContext(ctx, network, address)
}

// Dialer returns a copy of base whose sockets are tagged before connect.
func (realm Realm) Dialer(base net.Dialer) *Dialer {
	originalControl := base.Control
	originalControlContext := base.ControlContext
	base.Control = nil
	base.ControlContext = func(ctx context.Context, network, address string, raw syscall.RawConn) error {
		if originalControlContext != nil {
			if err := originalControlContext(ctx, network, address, raw); err != nil {
				return err
			}
		} else if originalControl != nil {
			if err := originalControl(network, address, raw); err != nil {
				return err
			}
		}
		return netaccounting.SetSocketOwnerBeforeConnect(raw, realm.accountingOwner())
	}
	return &Dialer{base: base}
}

func (realm Realm) listenConfig(base net.ListenConfig) *net.ListenConfig {
	originalControl := base.Control
	base.Control = func(network, address string, raw syscall.RawConn) error {
		if originalControl != nil {
			if err := originalControl(network, address, raw); err != nil {
				return err
			}
		}
		return netaccounting.SetSocketOwnerBeforeConnect(raw, realm.accountingOwner())
	}
	return &base
}

func (realm Realm) Listen(ctx context.Context, network, address string) (net.Listener, error) {
	listener, err := realm.listenConfig(net.ListenConfig{}).Listen(ctx, network, address)
	if err != nil {
		return nil, err
	}
	tagged, err := realm.Listener(listener)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	return tagged, nil
}

func (realm Realm) ListenPacket(ctx context.Context, network, address string) (net.PacketConn, error) {
	conn, err := realm.listenConfig(net.ListenConfig{}).ListenPacket(ctx, network, address)
	if err != nil {
		return nil, err
	}
	if err := realm.TagPacketConn(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func (realm Realm) defaultDialer() *Dialer {
	dnsDialer := realm.Dialer(net.Dialer{Timeout: 30 * time.Second})
	resolver := &net.Resolver{PreferGo: true, Dial: dnsDialer.DialContext}
	return realm.Dialer(net.Dialer{
		Timeout:       30 * time.Second,
		KeepAlive:     30 * time.Second,
		FallbackDelay: 300 * time.Millisecond,
		Resolver:      resolver,
	})
}

// Transport returns an independent HTTP connection pool for one realm.
func (realm Realm) Transport(base *http.Transport) *http.Transport {
	transport := base.Clone()
	transport.DialContext = realm.defaultDialer().DialContext
	transport.DialTLSContext = nil
	return transport
}

func (realm Realm) HTTPClient(base *http.Transport) *http.Client {
	return &http.Client{Transport: realm.Transport(base)}
}

// Transport keeps independent pools for each realm while preserving reuse
// and HTTP multiplexing within a realm.
type Transport struct {
	daggerland *http.Transport
	userland   *http.Transport
}

func NewTransport(base *http.Transport) *Transport {
	return &Transport{
		daggerland: Daggerland.Transport(base),
		userland:   Userland.Transport(base),
	}
}

func (transport *Transport) Transform(fn func(*http.Transport) *http.Transport) *Transport {
	return &Transport{
		daggerland: fn(transport.daggerland),
		userland:   fn(transport.userland),
	}
}

func (transport *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	switch FromContext(req.Context()) {
	case Daggerland:
		return transport.daggerland.RoundTrip(req)
	case Userland:
		return transport.userland.RoundTrip(req)
	default:
		return nil, ErrRequired
	}
}

func (transport *Transport) CloseIdleConnections() {
	transport.daggerland.CloseIdleConnections()
	transport.userland.CloseIdleConnections()
}

type listener struct {
	net.Listener
	realm Realm
}

// Listener tags a listener and every accepted connection with one realm.
func (realm Realm) Listener(base net.Listener) (net.Listener, error) {
	if !realm.Valid() {
		return nil, ErrRequired
	}
	if conn, ok := base.(syscall.Conn); ok {
		if err := netaccounting.SetSocketOwner(conn, realm.accountingOwner()); err != nil {
			return nil, err
		}
	}
	return &listener{Listener: base, realm: realm}, nil
}

func (listener *listener) Accept() (net.Conn, error) {
	conn, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	raw, ok := conn.(syscall.Conn)
	if !ok {
		_ = conn.Close()
		return nil, ErrRequired
	}
	if err := netaccounting.SetSocketOwner(raw, listener.realm.accountingOwner()); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func (realm Realm) TagPacketConn(conn net.PacketConn) error {
	if !realm.Valid() {
		return ErrRequired
	}
	raw, ok := conn.(syscall.Conn)
	if !ok {
		return ErrRequired
	}
	return netaccounting.SetSocketOwner(raw, realm.accountingOwner())
}
