package llb

import (
	"context"
	"testing"

	"github.com/moby/buildkit/solver/pb"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStateMeta(t *testing.T) {
	t.Parallel()

	s := Image("foo")
	s = s.AddEnv("BAR", "abc").Dir("/foo/bar")

	v, ok := getEnvHelper(t, s, "BAR")
	assert.True(t, ok)
	assert.Equal(t, "abc", v)

	assert.Equal(t, "/foo/bar", getDirHelper(t, s))

	s2 := Image("foo2")
	s2 = s2.AddEnv("BAZ", "def").Reset(s)

	_, ok = getEnvHelper(t, s2, "BAZ")
	assert.False(t, ok)

	v, ok = getEnvHelper(t, s2, "BAR")
	assert.True(t, ok)
	assert.Equal(t, "abc", v)
}

func TestFormattingPatterns(t *testing.T) {
	t.Parallel()

	s := Image("foo")
	s = s.AddEnv("FOO", "ab%sc").Dir("/foo/bar%d")

	v, ok := getEnvHelper(t, s, "FOO")
	assert.True(t, ok)
	assert.Equal(t, "ab%sc", v)

	assert.Equal(t, "/foo/bar%d", getDirHelper(t, s))

	s2 := Image("foo")
	s2 = s2.AddEnvf("FOO", "ab%sc", "__").Dirf("/foo/bar%d", 1)

	v, ok = getEnvHelper(t, s2, "FOO")
	assert.True(t, ok)
	assert.Equal(t, "ab__c", v)

	assert.Equal(t, "/foo/bar1", getDirHelper(t, s2))
}

func TestStateSourceMapMarshal(t *testing.T) {
	t.Parallel()

	sm1 := NewSourceMap(nil, "foo", "lang1", []byte("data1"))
	sm2 := NewSourceMap(nil, "bar", "lang2", []byte("data2"))

	s := Image(
		"myimage",
		sm1.Location([]*pb.Range{{Start: pb.Position{Line: 7}}}),
		sm2.Location([]*pb.Range{{Start: pb.Position{Line: 8}}}),
		sm1.Location([]*pb.Range{{Start: pb.Position{Line: 9}}}),
	)

	def, err := s.Marshal(context.TODO())
	require.NoError(t, err)

	require.Equal(t, 2, len(def.Def))
	dgst := digest.FromBytes(def.Def[0])

	require.Equal(t, 2, len(def.Source.Infos))
	require.Equal(t, 1, len(def.Source.Locations))

	require.Equal(t, "foo", def.Source.Infos[0].Filename)
	require.Equal(t, "lang1", def.Source.Infos[0].Language)
	require.Equal(t, []byte("data1"), def.Source.Infos[0].Data)
	require.Nil(t, def.Source.Infos[0].Definition)

	require.Equal(t, "bar", def.Source.Infos[1].Filename)
	require.Equal(t, "lang2", def.Source.Infos[1].Language)
	require.Equal(t, []byte("data2"), def.Source.Infos[1].Data)
	require.Nil(t, def.Source.Infos[1].Definition)

	require.NotNil(t, def.Source.Locations[dgst.String()])
	require.Equal(t, 3, len(def.Source.Locations[dgst.String()].Locations))

	require.Equal(t, int32(0), def.Source.Locations[dgst.String()].Locations[0].SourceIndex)
	require.Equal(t, 1, len(def.Source.Locations[dgst.String()].Locations[0].Ranges))
	require.Equal(t, int32(7), def.Source.Locations[dgst.String()].Locations[0].Ranges[0].Start.Line)

	require.Equal(t, int32(1), def.Source.Locations[dgst.String()].Locations[1].SourceIndex)
	require.Equal(t, 1, len(def.Source.Locations[dgst.String()].Locations[1].Ranges))
	require.Equal(t, int32(8), def.Source.Locations[dgst.String()].Locations[1].Ranges[0].Start.Line)

	require.Equal(t, int32(0), def.Source.Locations[dgst.String()].Locations[2].SourceIndex)
	require.Equal(t, 1, len(def.Source.Locations[dgst.String()].Locations[2].Ranges))
	require.Equal(t, int32(9), def.Source.Locations[dgst.String()].Locations[2].Ranges[0].Start.Line)

	s = Merge([]State{s, Image("myimage",
		sm1.Location([]*pb.Range{{Start: pb.Position{Line: 10}}}),
	)})
	def, err = s.Marshal(context.TODO())
	require.NoError(t, err)
	require.Equal(t, 3, len(def.Def))
	dgst = digest.FromBytes(def.Def[0])

	require.Equal(t, 2, len(def.Source.Infos))
	require.Equal(t, 2, len(def.Source.Locations))

	require.Equal(t, "foo", def.Source.Infos[0].Filename)
	require.Equal(t, []byte("data1"), def.Source.Infos[0].Data)
	require.Nil(t, def.Source.Infos[0].Definition)

	require.Equal(t, "bar", def.Source.Infos[1].Filename)
	require.Equal(t, []byte("data2"), def.Source.Infos[1].Data)
	require.Nil(t, def.Source.Infos[1].Definition)

	require.NotNil(t, def.Source.Locations[dgst.String()])
	require.Equal(t, 4, len(def.Source.Locations[dgst.String()].Locations))

	require.Equal(t, int32(0), def.Source.Locations[dgst.String()].Locations[0].SourceIndex)
	require.Equal(t, 1, len(def.Source.Locations[dgst.String()].Locations[0].Ranges))
	require.Equal(t, int32(7), def.Source.Locations[dgst.String()].Locations[0].Ranges[0].Start.Line)

	require.Equal(t, int32(1), def.Source.Locations[dgst.String()].Locations[1].SourceIndex)
	require.Equal(t, 1, len(def.Source.Locations[dgst.String()].Locations[1].Ranges))
	require.Equal(t, int32(8), def.Source.Locations[dgst.String()].Locations[1].Ranges[0].Start.Line)

	require.Equal(t, int32(0), def.Source.Locations[dgst.String()].Locations[2].SourceIndex)
	require.Equal(t, 1, len(def.Source.Locations[dgst.String()].Locations[2].Ranges))
	require.Equal(t, int32(9), def.Source.Locations[dgst.String()].Locations[2].Ranges[0].Start.Line)

	require.Equal(t, int32(0), def.Source.Locations[dgst.String()].Locations[3].SourceIndex)
	require.Equal(t, 1, len(def.Source.Locations[dgst.String()].Locations[3].Ranges))
	require.Equal(t, int32(10), def.Source.Locations[dgst.String()].Locations[3].Ranges[0].Start.Line)
}

func TestPlatformFromImage(t *testing.T) {
	t.Parallel()

	s := Image("srcimage", LinuxS390x).
		Run(Args([]string{"foo"})).
		File(Mkdir("/foo", 0700).Mkfile("/bar", 0600, []byte("bar"))).
		Run(Args([]string{"afterfile"})).Root()

	dest := Image("destimage").File(Copy(s, "/", "/")).Run(Args([]string{"afterfile"}))

	def, err := dest.Marshal(context.TODO(), LinuxPpc64le)
	require.NoError(t, err)

	m, arr := parseDef(t, def.Def)
	_ = m
	require.Equal(t, 8, len(arr))

	dgst, idx := last(t, arr)
	require.Equal(t, 0, idx)

	vtx, ok := m[dgst]
	require.Equal(t, true, ok)

	_, ok = vtx.Op.(*pb.Op_Exec)
	require.Equal(t, true, ok)
	require.Equal(t, "ppc64le", vtx.Platform.Architecture)

	vtx, ok = m[vtx.Inputs[0].Digest]
	require.Equal(t, true, ok)

	f, ok := vtx.Op.(*pb.Op_File)
	require.Equal(t, true, ok)
	require.Equal(t, 1, len(f.File.Actions))
	require.Nil(t, vtx.Platform)

	mainVtx := vtx
	vtx, ok = m[vtx.Inputs[0].Digest]
	require.Equal(t, true, ok)

	src, ok := vtx.Op.(*pb.Op_Source)
	require.Equal(t, true, ok)
	require.Equal(t, "docker-image://docker.io/library/destimage:latest", src.Source.Identifier)
	require.Equal(t, "ppc64le", vtx.Platform.Architecture)

	vtx, ok = m[mainVtx.Inputs[1].Digest]
	require.Equal(t, true, ok)

	_, ok = vtx.Op.(*pb.Op_Exec)
	require.Equal(t, true, ok)
	require.Equal(t, "s390x", vtx.Platform.Architecture)

	vtx, ok = m[vtx.Inputs[0].Digest]
	require.Equal(t, true, ok)

	f, ok = vtx.Op.(*pb.Op_File)
	require.Equal(t, true, ok)
	require.Equal(t, 2, len(f.File.Actions))
	require.Nil(t, vtx.Platform)

	vtx, ok = m[vtx.Inputs[0].Digest]
	require.Equal(t, true, ok)

	_, ok = vtx.Op.(*pb.Op_Exec)
	require.Equal(t, true, ok)
	require.Equal(t, "s390x", vtx.Platform.Architecture)

	vtx, ok = m[vtx.Inputs[0].Digest]
	require.Equal(t, true, ok)

	src, ok = vtx.Op.(*pb.Op_Source)
	require.Equal(t, true, ok)
	require.Equal(t, "docker-image://docker.io/library/srcimage:latest", src.Source.Identifier)
	require.Equal(t, "s390x", vtx.Platform.Architecture)
}

func TestPlatformFromImageWithMerge(t *testing.T) {
	t.Parallel()

	s := Image("srcimage", LinuxS390x)

	s2 := Scratch().File(Mkdir("/foo", 0700).Mkfile("/bar", 0600, []byte("bar")))

	dest := Merge([]State{s, s2}).Run(Args([]string{"aftermerge"}))

	def, err := dest.Marshal(context.TODO(), LinuxPpc64le)
	require.NoError(t, err)

	m, arr := parseDef(t, def.Def)
	_ = m
	require.Equal(t, 5, len(arr))

	dgst, idx := last(t, arr)
	require.Equal(t, 0, idx)

	vtx, ok := m[dgst]
	require.Equal(t, true, ok)

	_, ok = vtx.Op.(*pb.Op_Exec)
	require.Equal(t, true, ok)
	require.Equal(t, "s390x", vtx.Platform.Architecture)

	vtx, ok = m[vtx.Inputs[0].Digest]
	require.Equal(t, true, ok)

	_, ok = vtx.Op.(*pb.Op_Merge)
	require.Equal(t, true, ok)
	require.Nil(t, vtx.Platform)

	mainVtx := vtx
	vtx, ok = m[vtx.Inputs[0].Digest]
	require.Equal(t, true, ok)

	src, ok := vtx.Op.(*pb.Op_Source)
	require.Equal(t, true, ok)
	require.Equal(t, "docker-image://docker.io/library/srcimage:latest", src.Source.Identifier)
	require.Equal(t, "s390x", vtx.Platform.Architecture)

	vtx, ok = m[mainVtx.Inputs[1].Digest]
	require.Equal(t, true, ok)

	f, ok := vtx.Op.(*pb.Op_File)
	require.Equal(t, true, ok)
	require.Equal(t, 2, len(f.File.Actions))
	require.Nil(t, vtx.Platform)
}

func getEnvHelper(t *testing.T, s State, k string) (string, bool) {
	t.Helper()
	v, ok, err := s.GetEnv(context.TODO(), k)
	require.NoError(t, err)
	return v, ok
}
