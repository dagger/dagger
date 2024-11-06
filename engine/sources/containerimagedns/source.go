package containerimagedns

import (
	"context"
	"fmt"
	"net/http"

	"github.com/containerd/containerd/remotes/docker"
	"github.com/dagger/dagger/engine/sources/netconfhttp"
	"github.com/dagger/dagger/network"
	"github.com/moby/buildkit/executor/oci"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/source"
	srcimg "github.com/moby/buildkit/source/containerimage"
	"github.com/pkg/errors"
)

type SourceOpt struct {
	srcimg.SourceOpt
	BaseDNSConfig *oci.DNSConfig
}

type Source struct {
	*srcimg.Source
	BaseDNSConfig *oci.DNSConfig
}

func NewSource(opt SourceOpt) (*Source, error) {
	src, err := srcimg.NewSource(opt.SourceOpt)
	if err != nil {
		return nil, err
	}
	is := &Source{
		Source:        src,
		BaseDNSConfig: opt.BaseDNSConfig,
	}
	return is, nil
}

func (is *Source) Schemes() []string {
	return is.Source.Schemes()
}

func (is *Source) Identifier(scheme, ref string, attrs map[string]string, platform *pb.Platform) (source.Identifier, error) {
	srcid, err := is.Source.Identifier(scheme, ref, attrs, platform)
	if err != nil {
		return nil, err
	}
	id := &Identifier{Identifier: srcid}

	if v, ok := attrs[AttrDNSNamespace]; ok {
		id.Namespace = v
	}
	return id, nil
}

func (is *Source) Resolve(ctx context.Context, id source.Identifier, sm *session.Manager, vtx solver.Vertex) (source.SourceInstance, error) {
	imgid, ok := id.(*Identifier)
	if !ok {
		return nil, errors.Errorf("invalid image identifier %v", id)
	}

	// set registryhosts, call srcimg.Resolve
	baseRegistryHosts := is.Source.RegistryHosts
	is.RegistryHosts = func(namespace string) ([]docker.RegistryHost, error) {
		fmt.Println("!!!!!! this is actually happening !!!!!!")
		clientDomains := []string{}
		if ns := imgid.Namespace; ns != "" {
			clientDomains = append(clientDomains, network.SessionDomain(ns))
		}

		dns := is.BaseDNSConfig
		dns.SearchDomains = append(clientDomains, dns.SearchDomains...)

		hosts, err := baseRegistryHosts(namespace)
		if err != nil {
			return nil, err
		}
		fmt.Printf("srv.dns: %v\n", is.BaseDNSConfig)
		for i := range hosts {
			hosts[i].Client = &http.Client{Transport: netconfhttp.NewTransport(hosts[i].Client.Transport, dns)}
		}
		return hosts, nil
	}

	return is.Source.Resolve(ctx, id, sm, vtx)
}
