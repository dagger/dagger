package gogenerator

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCollectionRuntimeState(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n\ngo 1.26\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main
import "encoding/json"
// +collection
type Items struct { Keys []string }
func (r Items) MarshalJSON() ([]byte, error) {
 var concrete struct { Keys []string }
 concrete.Keys = r.Keys
 return json.Marshal(&concrete)
}
func (r *Items) UnmarshalJSON(bs []byte) error {
 var concrete struct { Keys []string }
 if err := json.Unmarshal(bs, &concrete); err != nil { return err }
 r.Keys = concrete.Keys
 return nil
}
func main() {
 var items Items
 if err := json.Unmarshal([]byte("{\"Keys\":[\"a\"],\"__daggerCollectionBase\":\"original\"}"), &items); err != nil { panic(err) }
 copy := items
 copy.Keys = []string{"b"}
 b, err := json.Marshal(copy)
 if err != nil { panic(err) }
 println(string(b))
 // Positional literals, including elided types, still create new collections.
 type Alias = Items
 type ItemList []Alias
 type ItemMap map[string]*Alias
 type Derived Items
 fresh := []Items{{[]string{"c"}}, Items{[]string{"d"}}}
 fresh = append(fresh, Alias{[]string{"e"}})
 fresh = append(fresh, ItemList{{[]string{"f"}}}...)
 fresh = append(fresh, *ItemMap{"g": {[]string{"g"}}}["g"])
 fresh = append(fresh, Items(Derived{[]string{"h"}}))
 // A local type with the same name is not a collection.
 { type Items struct { N int }; _ = Items{1} }
 b, err = json.Marshal(fresh)
 if err != nil { panic(err) }
 println(string(b))
}
`), 0600))
	t.Chdir(dir)
	require.NoError(t, PrepareCollectionRuntime("."))
	cmd := exec.CommandContext(t.Context(), "go", "run", ".")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	require.Equal(t, "{\"Keys\":[\"b\"],\"__daggerCollectionBase\":\"original\"}\n[{\"Keys\":[\"c\"]},{\"Keys\":[\"d\"]},{\"Keys\":[\"e\"]},{\"Keys\":[\"f\"]},{\"Keys\":[\"g\"]},{\"Keys\":[\"h\"]}]\n", string(out))
}
