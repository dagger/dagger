package server

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/engineutil"
)

// attachableRule says which client answers calls to an attachable.
type attachableRule int

const (
	// ruleHostAccess: the requesting client, or the owner recorded in a value
	// such as a local workspace. Applied by getClientCaller and
	// resolveHostServiceCaller.
	ruleHostAccess attachableRule = iota
	// ruleValueBound: the client recorded in the value, falling back to other
	// clients that bound the same handle. Applied by
	// SpecificClientAttachableConn and the session resource candidates.
	ruleValueBound
	// ruleInteractive: the first client that provides the attachable on the
	// requesting client's chain, else the oldest connected root client that
	// provides it.
	ruleInteractive
	// ruleSessionCredentials: the oldest connected root client that provides
	// the attachable, else none.
	ruleSessionCredentials
)

type attachableKind struct {
	// name is how the kind is shown to users.
	name string
	rule attachableRule
}

// attachableKinds maps each attachable gRPC service a client can serve to its
// kind and routing rule.
var attachableKinds = map[string]attachableKind{
	"moby.filesync.v1.FileSync":              {"files", ruleHostAccess},
	"moby.filesync.v1.FileSend":              {"files", ruleHostAccess},
	"dagger.git.Git":                         {"git", ruleHostAccess},
	"dagger.store.BasicStore":                {"images", ruleHostAccess},
	"containerd.services.content.v1.Content": {"images", ruleHostAccess},
	"containerd.services.images.v1.Images":   {"images", ruleHostAccess},
	"containerd.services.leases.v1.Leases":   {"images", ruleHostAccess},
	"dagger.h2c.TunnelListener":              {"tunnels", ruleHostAccess},
	"moby.buildkit.secrets.v1.Secrets":       {"secrets", ruleValueBound},
	"moby.sshforward.v1.SSH":                 {"sockets", ruleValueBound},
	registryAuthService:                      {"registry-auth", ruleSessionCredentials},
	engineutil.TerminalService:               {"terminal", ruleInteractive},
	engineutil.PromptService:                 {"prompt", ruleInteractive},
	engineutil.PipeService:                   {"pipe", ruleInteractive},
}

const registryAuthService = "moby.filesync.v1.Auth"

// getProviderCaller returns the client that answers calls to an interactive
// service for the client in ctx.
func (sess *daggerSession) getProviderCaller(ctx context.Context, service string) (engineutil.SessionCaller, error) {
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return sess.attachableProvider(md.ClientID, service)
}

// attachableProvider returns the client that answers calls to service for a
// request from requesterID, by the service's interactive or session-credentials
// rule. It never waits: a client provides a service only while its attachables
// connection is open. It returns nil and no error when no client provides a
// session-credentials service.
func (sess *daggerSession) attachableProvider(requesterID, service string) (engineutil.SessionCaller, error) {
	kind, ok := attachableKinds[service]
	if !ok {
		return nil, fmt.Errorf("unknown attachable service %q", service)
	}
	switch kind.rule {
	case ruleInteractive:
		sess.clientMu.RLock()
		requester := sess.clientRecords[requesterID]
		sess.clientMu.RUnlock()
		if requester == nil {
			return nil, fmt.Errorf("client %q not found", requesterID)
		}
		chain := append(slices.Clone(requester.parentClientIDs), requester.clientID)
		for _, clientID := range slices.Backward(chain) {
			if caller := sess.providingCaller(clientID, service); caller != nil {
				return caller, nil
			}
		}
		if caller := sess.oldestRootProvider(service); caller != nil {
			return caller, nil
		}
		if sess.detached {
			return nil, fmt.Errorf("no attached client provides a %s; run `dagger sessions attach %s`", kind.name, sess.sessionID)
		}
		return nil, fmt.Errorf("no attached client provides a %s", kind.name)
	case ruleSessionCredentials:
		if caller := sess.oldestRootProvider(service); caller != nil {
			return caller, nil
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("attachable service %q is not routed by provider", service)
	}
}

// providingCaller returns the open attachables connection serving clientID, if
// it provides service.
func (sess *daggerSession) providingCaller(clientID, service string) *sessionAttachableCaller {
	sess.clientMu.RLock()
	record := sess.clientRecords[clientID]
	sess.clientMu.RUnlock()
	if record == nil {
		return nil
	}
	sess.scopeMu.Lock()
	inertAttachables := record.inertAttachables
	attachablesClientID := record.attachablesClientID
	sess.scopeMu.Unlock()
	if inertAttachables {
		return nil
	}
	if attachablesClientID == "" {
		attachablesClientID = record.clientID
	}
	caller := sess.attachables.lookup(attachablesClientID)
	if caller == nil || !caller.Provides(service) {
		return nil
	}
	return caller
}

// oldestRootProvider returns the open attachables connection of the
// earliest-created root client that provides service.
func (sess *daggerSession) oldestRootProvider(service string) *sessionAttachableCaller {
	sess.clientMu.RLock()
	var roots []*clientRecord
	for _, record := range sess.clientRecords {
		if len(record.parentClientIDs) == 0 {
			roots = append(roots, record)
		}
	}
	sess.clientMu.RUnlock()
	slices.SortFunc(roots, func(a, b *clientRecord) int {
		return cmp.Compare(a.seq, b.seq)
	})
	for _, record := range roots {
		if caller := sess.providingCaller(record.clientID, service); caller != nil {
			return caller
		}
	}
	return nil
}
