package core

import (
	"github.com/dagger/dagger/dagql"
)

func (family foreignFamilyCodec) DescribeParts(v dagql.PersistedPayloadVisit) ([]dagql.PartProbe, error) {
	payload, err := family.decodePartPayload(v)
	if err != nil {
		return nil, err
	}
	outputs, err := family.mapSnapshotParts(v, payload)
	if err != nil {
		return nil, err
	}
	// Every output is routed on the one decode its outputs were mapped from:
	// per output, a Container with many mounts would decode them all again. A
	// payload that decodes strictly decodes to the same value as RouteParts
	// decodes it, so each route is the one RouteParts returns.
	router := &partRouter{family: family, v: v, payload: payload}
	probes := make([]dagql.PartProbe, 0, len(outputs))
	for _, out := range outputs {
		route, err := router.route(out.Address.Part, false)
		if err != nil {
			return nil, err
		}
		p := dagql.PartProbe{Descriptor: dagql.PartDescriptor{Address: out.Address, Value: out.Value, Absent: out.State == "absent", SnapshotID: out.SnapshotID}, LocalComplete: out.State == "completed" || out.State == "absent" || out.State == "metadata", RestoreOnly: out.SnapshotID != "", HasLazyOperation: route.HasLazyOperation}
		if out.Value != nil {
			for _, svc := range out.Value.Services {
				p.Descriptor.DependencyIDs = append(p.Descriptor.DependencyIDs, svc.ServiceResultID)
			}
		}
		if out.Address.Part == ContainerPartMetadata && p.LocalComplete {
			m := payload.container.Metadata.Value
			add := func(id uint64) {
				if id != 0 {
					p.Descriptor.DependencyIDs = append(p.Descriptor.DependencyIDs, id)
				}
			}
			for _, mount := range m.Mounts {
				add(mount.CacheSourceResultID)
				add(mount.VolumeSourceResultID)
			}
			for _, secret := range m.Secrets {
				add(secret.SecretResultID)
			}
			for _, socket := range m.Sockets {
				add(socket.SourceResultID)
			}
			for _, svc := range m.Services {
				add(svc.ServiceResultID)
			}
		}
		probes = append(probes, p)
	}
	return probes, nil
}
