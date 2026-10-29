package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TestBenchDagAddressSetting times `dagger check` on a workspace whose module
// takes a setting from a DAG address and runs many checks, each of which
// builds the module object and so resolves the setting. The provider module
// has many other artifacts, which every lookup of the address matches
// against.
//
// Skipped unless _DAGGER_BENCH is set (engine-dev's testProfile sets it), to
// keep it out of CI:
//
//	engine-dev testProfile(pkg: "./core/integration", testVerbose: true, count: 3,
//	  run: "TestArtifacts/TestBenchDagAddressSetting")
func (ArtifactsSuite) TestBenchDagAddressSetting(ctx context.Context, t *testctx.T) {
	if os.Getenv(benchEnv) == "" {
		t.Skip("benchmark: set " + benchEnv + "=1 to run (engine-dev testProfile does)")
	}
	const checks, artifacts = 100, 300
	c := connect(ctx, t, dagger.WithLogOutput(io.Discard))

	var provider strings.Builder
	provider.WriteString("type Toolchain {\n  pub goRuntime: Container! { container.withNewFile(\"/runtime\", \"go\") }\n")
	for i := range artifacts {
		fmt.Fprintf(&provider, "  pub tool%d: Container! { container.withNewFile(\"/tool\", \"%d\") }\n", i, i)
	}
	provider.WriteString("}\n")
	var consumer strings.Builder
	consumer.WriteString("type GoTests {\n  pub base: Container!\n")
	for i := range checks {
		fmt.Fprintf(&consumer, "  pub check%d: Void @check {\n    if (base.withNewFile(\"/check\", \"%d\").file(\"/check\").contents != \"%d\") { raise \"wrong base\" }\n    null\n  }\n", i, i, i)
	}
	consumer.WriteString("}\n")

	ws := workspaceBase(t, c).
		WithNewFile("dagger.toml", `[modules.toolchain]
source = "./toolchain"
[modules.go-tests]
source = "./go-tests"
settings.base = "dag://toolchain/go-runtime"
`).
		WithNewFile("toolchain/dagger-module.toml", "name = \"toolchain\"\nengineVersion = \"v1.0.0-beta.20\"\n[runtime]\nsource = \"dang\"\n").
		WithNewFile("toolchain/main.dang", provider.String()).
		WithNewFile("go-tests/dagger-module.toml", "name = \"go-tests\"\nengineVersion = \"v1.0.0-beta.20\"\n[runtime]\nsource = \"dang\"\n").
		WithNewFile("go-tests/main.dang", consumer.String())
	_, err := ws.Sync(ctx)
	require.NoError(t, err)

	started := time.Now()
	// The nonce keeps repeated runs (-count) from reusing the cached exec.
	out, err := ws.WithEnvVariable("BENCH_NONCE", time.Now().String()).
		With(daggerExec("check", "--progress=report")).CombinedOutput(ctx)
	require.NoError(t, err, out)
	t.Logf("BENCH dag-address-setting checks=%d artifacts=%d wall=%s", checks, artifacts, time.Since(started))
	require.Contains(t, out, fmt.Sprintf("%d passed", checks))
}
