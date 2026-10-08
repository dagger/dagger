package core

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

// describePartsPerOutput is DescribeParts as it was before a probe routed
// every output on one decode of the payload: each output is routed on its own
// decode, and the metadata dependencies decode the payload once more.
func describePartsPerOutput(family foreignFamilyCodec, v dagql.PersistedPayloadVisit) ([]dagql.PartProbe, error) {
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

// probeTestContainer describes a captured Container for probe tests.
type probeTestContainer struct {
	field    string
	lazy     any
	metadata persistedContainerMetadataValue
	consumed bool
	pending  []dagql.PartKey
	absent   []dagql.PartKey
}

// visit returns the Container's payload visit. With consumed metadata every
// snapshot part is captured: complete with a snapshot, unless pending or
// absent. Without it no part is captured yet.
func (ctr probeTestContainer) visit(t testing.TB) dagql.PersistedPayloadVisit {
	t.Helper()
	parts := map[dagql.PartKey]persistedContainerPart{}
	var links []dagql.PersistedSnapshotRefLink
	capture := func(key dagql.PartKey, kind, role string) {
		switch {
		case slices.Contains(ctr.pending, key):
			parts[key] = persistedContainerPart{Kind: containerPartPending, ValueKind: kind, Role: role}
		case slices.Contains(ctr.absent, key):
			parts[key] = persistedContainerPart{Kind: containerPartAbsent, Role: role}
		default:
			platform := ctr.metadata.Platform
			parts[key] = persistedContainerPart{Kind: kind, Role: role, Path: "/", Platform: &platform, Services: []persistedServiceBinding{{ServiceResultID: 7, Hostname: "svc"}}}
			links = append(links, dagql.PersistedSnapshotRefLink{RefKey: "ref-" + role, Role: role})
		}
	}
	if ctr.consumed {
		capture(ContainerPartFS, containerPartDirectory, "fs")
		capture(ContainerPartExecMeta, containerPartSnapshot, "meta")
		for i, mount := range ctr.metadata.Mounts {
			switch mount.Kind {
			case persistedContainerMountKindDirectory:
				capture(ContainerPartMount(mount.Target), containerPartDirectory, fmt.Sprintf("mount_dir:%d", i))
			case persistedContainerMountKindFile:
				capture(ContainerPartMount(mount.Target), containerPartFile, fmt.Sprintf("mount_file:%d", i))
			}
		}
	}
	payload := persistedContainerPayload{Metadata: persistedContainerMetadata{Consumed: ctr.consumed, Value: ctr.metadata}, Parts: parts}
	if ctr.lazy != nil {
		raw, err := json.Marshal(ctr.lazy)
		require.NoError(t, err)
		payload.LazyJSON = raw
	}
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	call := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: ctr.field, Type: dagql.NewResultCallType((&Container{}).Type()), Receiver: &dagql.ResultCallRef{ResultID: 42}}
	return dagql.PersistedPayloadVisit{Call: call, Path: dagql.PersistedRefPath{}.Field("items").Index(1), Payload: raw, SnapshotLinks: links}
}

// probeTestFileMounts returns n read-only file mounts at /m0, /m1, and so on:
// a Container after n chained withMountedFile calls.
func probeTestFileMounts(n int) persistedContainerMetadataValue {
	metadata := persistedContainerMetadataValue{Platform: Platform{OS: "linux", Architecture: "amd64"}}
	for i := range n {
		metadata.Mounts = append(metadata.Mounts, persistedContainerMountPayload{Target: fmt.Sprintf("/m%d", i), Kind: persistedContainerMountKindFile, Readonly: true})
	}
	return metadata
}

// probeTestMountedFile is the Container its last withMountedFile call returns
// before that mount's part is evaluated.
func probeTestMountedFile(n int) probeTestContainer {
	last := fmt.Sprintf("/m%d", n-1)
	return probeTestContainer{field: "withMountedFile", lazy: persistedContainerWithMountedFileLazy{Target: last}, metadata: probeTestFileMounts(n), consumed: true, pending: []dagql.PartKey{ContainerPartMount(last)}}
}

type probeTestCase struct {
	name    string
	family  foreignFamilyCodec
	visit   dagql.PersistedPayloadVisit
	wantErr bool
}

func probeTestCases(t testing.TB) []probeTestCase {
	t.Helper()
	platform := Platform{OS: "linux", Architecture: "amd64"}
	mixed := persistedContainerMetadataValue{
		Platform: platform,
		Mounts: []persistedContainerMountPayload{
			{Target: "/src", Kind: persistedContainerMountKindDirectory},
			{Target: "/ro", Kind: persistedContainerMountKindDirectory, Readonly: true},
			{Target: "/cfg", Kind: persistedContainerMountKindFile, Readonly: true},
			{Target: "/out", Kind: persistedContainerMountKindFile},
			{Target: "/cache", Kind: persistedContainerMountKindCache, CacheSourceResultID: 11},
			{Target: "/vol", Kind: persistedContainerMountKindVolume, VolumeSourceResultID: 12},
		},
		Secrets:  []persistedContainerSecretPayload{{SecretResultID: 13, EnvName: "TOKEN"}},
		Sockets:  []persistedContainerSocketPayload{{SourceResultID: 14, ContainerPath: "/run/agent.sock"}},
		Services: []persistedServiceBinding{{ServiceResultID: 15, Hostname: "db"}},
	}
	execPending := []dagql.PartKey{ContainerPartFS, ContainerPartExecMeta, ContainerPartMount("/src"), ContainerPartMount("/out")}
	containers := []struct {
		name    string
		ctr     probeTestContainer
		wantErr bool
	}{
		{name: "withMountedFile", ctr: probeTestMountedFile(20)},
		{name: "withMountedFile metadata pending", ctr: probeTestContainer{field: "withMountedFile", lazy: persistedContainerWithMountedFileLazy{Target: "/m3"}, metadata: probeTestFileMounts(4)}},
		{name: "withMountedFile evaluated", ctr: probeTestContainer{field: "withMountedFile", metadata: probeTestFileMounts(4), consumed: true}},
		{name: "withExec", ctr: probeTestContainer{field: "withExec", lazy: persistedContainerExecLazy{}, metadata: mixed, consumed: true, pending: execPending}},
		{name: "withExec volatile cache hit", ctr: probeTestContainer{field: "withExec", lazy: persistedContainerExecLazy{VolatileCacheHitParentResultID: 99}, metadata: mixed, consumed: true, pending: execPending}},
		{name: "withDirectory", ctr: probeTestContainer{field: "withDirectory", lazy: persistedContainerWithDirectoryLazy{Path: "/src/child"}, metadata: mixed, consumed: true, pending: []dagql.PartKey{ContainerPartMount("/src")}}},
		{name: "withSymlink", ctr: probeTestContainer{field: "withSymlink", lazy: persistedContainerWithSymlinkLazy{LinkPath: "/link"}, metadata: mixed, consumed: true, pending: []dagql.PartKey{ContainerPartFS}}},
		{name: "from", ctr: probeTestContainer{field: "from", lazy: struct{}{}, metadata: mixed, consumed: true, pending: []dagql.PartKey{ContainerPartFS}}},
		{name: "builtin container", ctr: probeTestContainer{field: "_builtinContainer", lazy: struct{}{}, metadata: mixed, consumed: true, pending: []dagql.PartKey{ContainerPartFS}}},
		{name: "withWorkdir delegation", ctr: probeTestContainer{field: "withWorkdir", metadata: mixed, consumed: true, pending: []dagql.PartKey{ContainerPartMount("/src")}, absent: []dagql.PartKey{ContainerPartMount("/out")}}},
		{name: "withDirectory into cache mount", ctr: probeTestContainer{field: "withDirectory", lazy: persistedContainerWithDirectoryLazy{Path: "/cache/child"}, metadata: mixed, consumed: true, pending: []dagql.PartKey{ContainerPartFS}}, wantErr: true},
		{name: "unknown operation", ctr: probeTestContainer{field: "notAnOperation", lazy: struct{}{}, metadata: mixed, consumed: true}, wantErr: true},
	}
	var cases []probeTestCase
	for _, test := range containers {
		cases = append(cases, probeTestCase{name: "Container " + test.name, family: "Container", visit: test.ctr.visit(t), wantErr: test.wantErr})
	}
	snapshotLinks := []dagql.PersistedSnapshotRefLink{{RefKey: "ref-snapshot", Role: "snapshot"}}
	services := []persistedServiceBinding{{ServiceResultID: 21, Hostname: "svc"}}
	encode := func(payload any) json.RawMessage {
		raw, err := json.Marshal(payload)
		require.NoError(t, err)
		return raw
	}
	for _, kind := range slices.Sorted(maps.Keys(persistedDirectoryLazyVisitors)) {
		cases = append(cases, probeTestCase{name: "Directory lazy " + kind, family: "Directory", visit: dagql.PersistedPayloadVisit{Payload: encode(persistedDirectoryPayload{Form: persistedDirectoryFormLazy, Platform: platform, LazyKind: kind})}})
	}
	for _, kind := range slices.Sorted(maps.Keys(persistedFileLazyVisitors)) {
		cases = append(cases, probeTestCase{name: "File lazy " + kind, family: "File", visit: dagql.PersistedPayloadVisit{Payload: encode(persistedFilePayload{Form: persistedFileFormLazy, Platform: platform, LazyKind: kind})}})
	}
	cases = append(cases,
		probeTestCase{name: "Directory snapshot", family: "Directory", visit: dagql.PersistedPayloadVisit{Payload: encode(persistedDirectoryPayload{Form: persistedDirectoryFormSnapshot, Dir: "/", Platform: platform, Services: services}), SnapshotLinks: snapshotLinks}},
		probeTestCase{name: "File snapshot", family: "File", visit: dagql.PersistedPayloadVisit{Payload: encode(persistedFilePayload{Form: persistedFileFormSnapshot, File: "/f", Platform: platform, Services: services}), SnapshotLinks: snapshotLinks}},
		probeTestCase{name: "Directory unknown operation", family: "Directory", visit: dagql.PersistedPayloadVisit{Payload: encode(persistedDirectoryPayload{Form: persistedDirectoryFormLazy, Platform: platform, LazyKind: "notAnOperation"})}, wantErr: true},
	)
	return cases
}

// A probe routes every output on one decode of its payload, and reports
// exactly what routing each output on its own decode reports: the complete
// parts a lazy span emits are read from these probes.
func TestDescribePartsMatchesPerOutputRoutes(t *testing.T) {
	for _, test := range probeTestCases(t) {
		t.Run(test.name, func(t *testing.T) {
			want, wantErr := describePartsPerOutput(test.family, test.visit)
			if test.wantErr {
				require.Error(t, wantErr)
			} else {
				require.NoError(t, wantErr)
				require.NotEmpty(t, want)
			}
			got, err := test.family.DescribeParts(test.visit)
			require.Equal(t, fmt.Sprint(wantErr), fmt.Sprint(err))
			require.Equal(t, want, got)
		})
	}
}

// One router routes every demand of a payload as RouteParts routes it on a
// fresh decode, whatever was routed before; without a write set, only the
// write set differs.
func TestPartRouterMatchesRouteParts(t *testing.T) {
	for _, test := range probeTestCases(t) {
		t.Run(test.name, func(t *testing.T) {
			outputs, err := test.family.MapSnapshotParts(test.visit)
			require.NoError(t, err)
			router, err := test.family.newPartRouter(test.visit)
			require.NoError(t, err)
			probe, err := test.family.newPartRouter(test.visit)
			require.NoError(t, err)
			for _, out := range outputs {
				want, wantErr := test.family.RouteParts(test.visit, out.Address.Part)
				got, err := router.route(out.Address.Part, true)
				require.Equal(t, fmt.Sprint(wantErr), fmt.Sprint(err))
				require.Equal(t, want, got)
				got, err = probe.route(out.Address.Part, false)
				require.Equal(t, fmt.Sprint(wantErr), fmt.Sprint(err))
				want.WriteSet = nil
				require.Equal(t, want, got)
			}
		})
	}
}

// Describing a Container decodes its payload once per probe, not once per
// output, so its cost grows with the number of mounts rather than with its
// square: four times the mounts must cost well under sixteen times as much.
func TestDescribeContainerPartsScalesWithMounts(t *testing.T) {
	allocs := func(n int) float64 {
		v := probeTestMountedFile(n).visit(t)
		return testing.AllocsPerRun(3, func() {
			if _, err := foreignFamilyCodec("Container").DescribeParts(v); err != nil {
				panic(err)
			}
		})
	}
	small, large := allocs(50), allocs(200)
	require.Less(t, large, 6*small, "describing 200 mounts allocated %.0f times, 50 mounts %.0f times", large, small)
}

func BenchmarkDescribeContainerParts(b *testing.B) {
	for _, n := range []int{10, 50, 200} {
		b.Run(fmt.Sprintf("mounts=%d", n), func(b *testing.B) {
			v := probeTestMountedFile(n).visit(b)
			b.ReportAllocs()
			for b.Loop() {
				if _, err := foreignFamilyCodec("Container").DescribeParts(v); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
