package containerimagedns

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/containerd/containerd/remotes/docker"
	"github.com/dagger/dagger/engine/sources/netconfhttp"
	"github.com/dagger/dagger/network"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/executor/oci"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/source"
	srcimg "github.com/moby/buildkit/source/containerimage"
	"github.com/moby/buildkit/util/resolver"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
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

var _ source.Source = &Source{}

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

type resolveImageResult struct {
	dgst digest.Digest
	dt   []byte
}

func (is *Source) registryIdentifier(ref string, attrs map[string]string, platform *pb.Platform) (source.Identifier, error) {
	id, err := srcimg.NewImageIdentifier(ref)
	if err != nil {
		return nil, err
	}

	if platform != nil {
		id.Platform = &ocispecs.Platform{
			OS:           platform.OS,
			Architecture: platform.Architecture,
			Variant:      platform.Variant,
			OSVersion:    platform.OSVersion,
		}
		if platform.OSFeatures != nil {
			id.Platform.OSFeatures = append([]string{}, platform.OSFeatures...)
		}
	}

	for k, v := range attrs {
		switch k {
		case pb.AttrImageResolveMode:
			rm, err := resolver.ParseImageResolveMode(v)
			if err != nil {
				return nil, err
			}
			id.ResolveMode = rm
		case pb.AttrImageRecordType:
			rt, err := parseImageRecordType(v)
			if err != nil {
				return nil, err
			}
			id.RecordType = rt
		case pb.AttrImageLayerLimit:
			l, err := strconv.Atoi(v)
			if err != nil {
				return nil, errors.Wrapf(err, "invalid layer limit %s", v)
			}
			if l <= 0 {
				return nil, errors.Errorf("invalid layer limit %s", v)
			}
			id.LayerLimit = &l
		}
	}

	return id, nil
}

func (is *Source) ociIdentifier(ref string, attrs map[string]string, platform *pb.Platform) (source.Identifier, error) {
	id, err := srcimg.NewOCIIdentifier(ref)
	if err != nil {
		return nil, err
	}

	if platform != nil {
		id.Platform = &ocispecs.Platform{
			OS:           platform.OS,
			Architecture: platform.Architecture,
			Variant:      platform.Variant,
			OSVersion:    platform.OSVersion,
		}
		if platform.OSFeatures != nil {
			id.Platform.OSFeatures = append([]string{}, platform.OSFeatures...)
		}
	}

	for k, v := range attrs {
		switch k {
		case pb.AttrOCILayoutSessionID:
			id.SessionID = v
		case pb.AttrOCILayoutStoreID:
			id.StoreID = v
		case pb.AttrOCILayoutLayerLimit:
			l, err := strconv.Atoi(v)
			if err != nil {
				return nil, errors.Wrapf(err, "invalid layer limit %s", v)
			}
			if l <= 0 {
				return nil, errors.Errorf("invalid layer limit %s", v)
			}
			id.LayerLimit = &l
		}
	}

	return id, nil
}

func parseImageRecordType(v string) (client.UsageRecordType, error) {
	switch client.UsageRecordType(v) {
	case "", client.UsageRecordTypeRegular:
		return client.UsageRecordTypeRegular, nil
	case client.UsageRecordTypeInternal:
		return client.UsageRecordTypeInternal, nil
	case client.UsageRecordTypeFrontend:
		return client.UsageRecordTypeFrontend, nil
	default:
		return "", errors.Errorf("invalid record type %s", v)
	}
}
