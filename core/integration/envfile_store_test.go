package core

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	// The engine's own store driver, to read the saved store.
	_ "modernc.org/sqlite"
)

// envProbeModule is a module whose constructor takes one argument, greeting,
// that a .env can give a default, and whose function returns it.
func envProbeModule(name string) (string, string) {
	cfg := fmt.Sprintf(`{
  "name": %q,
  "engineVersion": "v0.21.9",
  "sdk": {
    "source": "dang"
  }
}
`, name)
	src := fmt.Sprintf(`type %s {
  let greeting: String! = "unset"

  new(greeting: String! = "unset") {
    self.greeting = greeting
    self
  }

  pub hello: String! {
    greeting
  }
}
`, strings.ToUpper(name[:1])+name[1:])
	return cfg, src
}

func writeEnvProbeModule(t *testctx.T, dir, name string) {
	t.Helper()
	cfg, src := envProbeModule(name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dagger.json"), []byte(cfg), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.dang"), []byte(src), 0o644))
}

// TestEnvFileLinesInStore checks which lines of a caller's .env a module load
// leaves in the engine's saved cache store. A client loads a module with a
// .env in reach, holding a line namespaced to the module, which is the
// module's user default, and lines that are not; the engine stops gracefully
// and saves its cache; the test searches every byte of the engine's state
// for each line's value, and every column of every table of the saved
// dagql store, naming the rows that hold it.
//
// Two layouts: the .env in the directory the client runs from, above the
// module (outer), and the .env at the module's own root, the client running
// there (inner).
//
// The test asserts that no value of a line of the outer .env that is not
// namespaced to the module is anywhere in the engine's state, the saved
// store included; it reports the others.
func (CachePersistenceSuite) TestEnvFileLinesInStore(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	const noGC = "1000000000000000"
	stateKey := "envfile-store-" + rand.Text()
	engineCtr := devEngineContainerWithStateKey(c, stateKey, engineWithConfig(ctx, t,
		engineConfigWithEnabled(true),
		engineConfigWithGC(noGC, "0", noGC, "0"),
	))
	upstream := devEngineContainerAsService(engineCtr)
	tunnel, err := core.NewQuery(c).Host().Tunnel(upstream).Start(ctx)
	require.NoError(t, err)
	endpoint, err := tunnel.Endpoint(ctx, core.ServiceEndpointOpts{Scheme: "tcp"})
	require.NoError(t, err)
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			_, _ = upstream.Stop(ctx, core.ServiceStopOpts{Kill: true})
			_, _ = tunnel.Stop(ctx, core.ServiceStopOpts{Kill: true})
		}
	})

	marker := func(kind string) string { return "ENVPROBE" + kind + rand.Text() }
	// A probe is one line's value. kind is "default" for a line that is the
	// module's user default, "own file" for any other line of the module's
	// own .env, which the engine reads whole as the module's, and "other"
	// for a line of a .env above the module that is not namespaced to it.
	type probe struct {
		name, value, kind string
	}
	var probes []probe

	load := func(t *testctx.T, workdir, ref, field, want string) {
		t.Helper()
		cl, err := dagger.Connect(ctx,
			dagger.WithRunnerHost(endpoint),
			dagger.WithWorkdir(workdir),
			dagger.WithLogOutput(testutil.NewTWriter(t)),
		)
		require.NoError(t, err)
		require.NoError(t, core.NewQuery(cl).ModuleSource(ref).AsModule().Serve(ctx))
		res, err := testutil.QueryWithClient[map[string]map[string]string](cl, t, fmt.Sprintf(`{ %s { hello } }`, field), nil)
		require.NoError(t, err)
		// The control: the client's .env was read, and its own line applied.
		require.Equal(t, want, (*res)[field]["hello"])
		require.NoError(t, cl.Close())
	}

	// Outer: /tmp/x/.env, the module at /tmp/x/envouter, run from /tmp/x.
	outerOwn, outerOther := marker("OUTEROWN"), marker("OUTEROTHER")
	outerDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outerDir, ".env"), []byte(
		"ENVOUTER_GREETING="+outerOwn+"\n"+
			"UNRELATED_TOKEN="+outerOther+"\n"), 0o600))
	writeEnvProbeModule(t, filepath.Join(outerDir, "envouter"), "envouter")
	probes = append(probes,
		probe{"outer ENVOUTER_GREETING", outerOwn, "default"},
		probe{"outer UNRELATED_TOKEN", outerOther, "other"})
	load(t, outerDir, "./envouter", "envouter", outerOwn)

	// Inner: the module and its .env at /tmp/y, run from /tmp/y.
	innerOwn, innerOther := marker("INNEROWN"), marker("INNEROTHER")
	innerDir := t.TempDir()
	writeEnvProbeModule(t, innerDir, "envinner")
	require.NoError(t, os.WriteFile(filepath.Join(innerDir, ".env"), []byte(
		"GREETING="+innerOwn+"\n"+
			"APP_DB_PASSWORD="+innerOther+"\n"), 0o600))
	probes = append(probes,
		probe{"inner GREETING", innerOwn, "default"},
		probe{"inner APP_DB_PASSWORD", innerOther, "own file"})
	load(t, innerDir, ".", "envinner", innerOwn)

	// A graceful stop saves the cache.
	_, err = upstream.Stop(ctx)
	require.NoError(t, err, "stop the engine gracefully")
	_, err = tunnel.Stop(ctx, core.ServiceStopOpts{Kill: true})
	require.NoError(t, err)
	stopped = true

	// Every file of the engine's state holding a value, and the saved store.
	var script strings.Builder
	script.WriteString("set -eu\nmkdir -p /out\n")
	for i, p := range probes {
		fmt.Fprintf(&script, "grep -rlF -a %q /state > /out/files-%d || true\n", p.value, i)
	}
	script.WriteString("cp /state/dagql-cache.db* /out/\nls -la /state > /out/state-ls\n")
	outDir := t.TempDir()
	_, err = core.NewQuery(c).Container().From(alpineImage).
		WithMountedCache("/state", core.NewQuery(c).CacheVolume(stateKey)).
		WithEnvVariable("CACHEBUSTER", rand.Text()).
		WithExec([]string{"sh", "-c", script.String()}).
		Directory("/out").
		Export(ctx, outDir)
	require.NoError(t, err)

	db, err := sql.Open("sqlite", "file:"+filepath.Join(outDir, "dagql-cache.db")+"?mode=ro")
	require.NoError(t, err)
	defer db.Close()
	var clean string
	require.NoError(t, db.QueryRow(`SELECT value FROM meta WHERE key = 'clean_shutdown'`).Scan(&clean))
	require.Equal(t, "1", clean, "the engine saved its cache on its stop")

	// Every column of every table, as text.
	type hit struct{ table, row string }
	hits := make([][]hit, len(probes))
	tables, err := queryStrings(db, `SELECT name FROM sqlite_master WHERE type = 'table'`)
	require.NoError(t, err)
	for _, table := range tables {
		rows, err := db.Query(fmt.Sprintf(`SELECT rowid, * FROM %q`, table))
		if err != nil {
			// WITHOUT ROWID tables have no rowid.
			rows, err = db.Query(fmt.Sprintf(`SELECT 0, * FROM %q`, table))
		}
		require.NoError(t, err, table)
		cols, err := rows.Columns()
		require.NoError(t, err)
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			require.NoError(t, rows.Scan(ptrs...))
			var text strings.Builder
			for _, v := range vals {
				switch v := v.(type) {
				case []byte:
					text.Write(v)
				default:
					fmt.Fprint(&text, v)
				}
				text.WriteByte('\x00')
			}
			for i, p := range probes {
				if strings.Contains(text.String(), p.value) {
					hits[i] = append(hits[i], hit{table, fmt.Sprint(vals[0])})
				}
			}
		}
		require.NoError(t, rows.Err())
		rows.Close()
	}

	// The rows of results that hold a value: their call, their type, and
	// the saved roots whose closures keep them.
	parents := map[int64][]int64{}
	depRows, err := db.Query(`SELECT parent_result_id, dep_result_id FROM result_deps`)
	require.NoError(t, err)
	for depRows.Next() {
		var p, d int64
		require.NoError(t, depRows.Scan(&p, &d))
		parents[d] = append(parents[d], p)
	}
	require.NoError(t, depRows.Err())
	depRows.Close()
	edges := map[int64]string{}
	edgeRows, err := db.Query(`SELECT result_id, expires_at_unix, unpruneable FROM persisted_edges`)
	require.NoError(t, err)
	for edgeRows.Next() {
		var id, exp, unp int64
		require.NoError(t, edgeRows.Scan(&id, &exp, &unp))
		edges[id] = fmt.Sprintf("expires_at_unix=%d unpruneable=%d", exp, unp)
	}
	require.NoError(t, edgeRows.Err())
	edgeRows.Close()
	describe := func(id int64) string {
		var frame, recordType, description string
		var payload []byte
		if err := db.QueryRow(`SELECT call_frame_json, CAST(self_payload AS BLOB), record_type, description FROM results WHERE id = ?`, id).
			Scan(&frame, &payload, &recordType, &description); err != nil {
			return fmt.Sprintf("result %d: %v", id, err)
		}
		var f struct {
			Field string `json:"field"`
			Type  *struct {
				NamedType string `json:"namedType"`
			} `json:"type"`
			Receiver *struct {
				ResultID uint64 `json:"resultID"`
			} `json:"receiver"`
		}
		_ = json.Unmarshal([]byte(frame), &f)
		var env struct {
			TypeName    string `json:"typeName"`
			ObjectCodec string `json:"objectCodec"`
		}
		_ = json.Unmarshal(payload, &env)
		typ := ""
		if f.Type != nil {
			typ = f.Type.NamedType
		}
		recv := uint64(0)
		if f.Receiver != nil {
			recv = f.Receiver.ResultID
		}
		edge := edges[id]
		return fmt.Sprintf("result %d field=%s type=%s value=%s codec=%s receiver=%d record_type=%q description=%q root=[%s]",
			id, f.Field, typ, env.TypeName, env.ObjectCodec, recv, recordType, description, edge)
	}
	rootsOf := func(id int64) []int64 {
		seen := map[int64]bool{id: true}
		queue := []int64{id}
		var roots []int64
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			if _, ok := edges[cur]; ok {
				roots = append(roots, cur)
			}
			for _, p := range parents[cur] {
				if !seen[p] {
					seen[p] = true
					queue = append(queue, p)
				}
			}
		}
		sort.Slice(roots, func(i, j int) bool { return roots[i] < roots[j] })
		return roots
	}

	var leaked []string
	for i, p := range probes {
		files, err := os.ReadFile(filepath.Join(outDir, fmt.Sprintf("files-%d", i)))
		require.NoError(t, err)
		t.Logf("ENVFINDING %s (%s): state files holding it: %q", p.name, p.kind, strings.Fields(string(files)))
		t.Logf("ENVFINDING %s (%s): store rows holding it: %d", p.name, p.kind, len(hits[i]))
		for _, h := range hits[i] {
			if h.table != "results" {
				t.Logf("ENVFINDING %s:   table %s row %s", p.name, h.table, h.row)
				continue
			}
			var id int64
			fmt.Sscan(h.row, &id)
			t.Logf("ENVFINDING %s:   %s", p.name, describe(id))
			for _, root := range rootsOf(id) {
				t.Logf("ENVFINDING %s:     kept by saved root %s", p.name, describe(root))
			}
		}
		if p.kind == "other" && (len(hits[i]) > 0 || len(strings.Fields(string(files))) > 0) {
			leaked = append(leaked, p.name)
		}
	}
	require.Empty(t, leaked, "values of outer .env lines not namespaced to the module are in the engine's state")
}

func queryStrings(db *sql.DB, q string) ([]string, error) {
	rows, err := db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
