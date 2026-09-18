package core

import (
	"encoding/json"

	"github.com/dagger/dagger/dagql"
)

func (family foreignFamilyCodec) DescribeParts(v dagql.PersistedPayloadVisit) ([]dagql.PartProbe, error) {
	outputs, err := family.MapSnapshotParts(v)
	if err != nil {
		return nil, err
	}
	probes := make([]dagql.PartProbe, 0, len(outputs))
	for _, out := range outputs {
		route, err := family.RouteParts(v, out.Address.Part)
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
			var payload persistedContainerPayload
			if err := json.Unmarshal(v.Payload, &payload); err != nil {
				return nil, err
			}
			m := payload.Metadata.Value
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
