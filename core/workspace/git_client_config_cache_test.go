package workspace

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/dagger/dagger/engine/slog"
)

const gitDeclarationFixture = `[modules.go]
source = "go"
[sdks.go]
module = "go"
[sdks.go.scopes."."]
is-module = true
clients = ["github.com/example/root"]
[sdks.go.scopes."modules/caller"]
is-module = true
name = "caller"
clients = ["github.com/example/first"]
[sdks.go.scopes."modules/other"]
is-module = true
clients = ["github.com/example/other"]
`

func TestDeclaredGitClientsFreshInputAndOwnership(t *testing.T) {
	files := map[string]string{"dagger.toml": gitDeclarationFixture}
	var readErr error
	read := func(rel string) ([]byte, bool, error) {
		data, found := files[rel]
		return []byte(data), found, readErr
	}
	check := func(dir string, want ...string) []string {
		t.Helper()
		got, err := DeclaredGitClients(t.Context(), dir, read)
		require.NoError(t, err)
		require.Equal(t, want, got)
		return got
	}
	got := check("modules/caller", "github.com/example/first")
	got[0] = "changed by caller"
	check("modules/caller", "github.com/example/first")
	check("modules/other", "github.com/example/other")
	files["dagger.toml"] = strings.ReplaceAll(gitDeclarationFixture, "example/first", "example/newer")
	check("modules/caller", "github.com/example/newer")
	// Identical bytes at a nearer path select a different scope.
	files["modules/caller/dagger.toml"] = gitDeclarationFixture
	check("modules/caller", "github.com/example/root")
	delete(files, "modules/caller/dagger.toml")
	check("modules/caller", "github.com/example/newer")
	files["dagger.toml"] = "[invalid"
	check("modules/caller")
	files["dagger.toml"] = gitDeclarationFixture
	readErr = errors.New("unreadable configuration")
	_, err := DeclaredGitClients(t.Context(), "modules/caller", read)
	require.ErrorIs(t, err, readErr)
	readErr = nil
	check("modules/caller", "github.com/example/first")
	delete(files, "dagger.toml")
	check("modules/caller")
	files["dagger.toml"] = gitDeclarationFixture + strings.Repeat("# padding\n", 8000)
	check("modules/caller", "github.com/example/first")
}

func TestDeclaredGitClientsConcurrentOwnership(t *testing.T) {
	data := []byte(gitDeclarationFixture)
	read := func(rel string) ([]byte, bool, error) { return data, rel == "dagger.toml", nil }
	var group sync.WaitGroup
	for range 16 {
		group.Go(func() {
			for range 8 {
				got, err := DeclaredGitClients(t.Context(), "modules/caller", read)
				if err != nil || len(got) != 1 || got[0] != "github.com/example/first" {
					t.Errorf("declarations = %v, err = %v", got, err)
					return
				}
				got[0] = "owned by this caller"
			}
		})
	}
	group.Wait()
}

type configWarningExporter struct {
	mu       sync.Mutex
	messages []string
}

func (e *configWarningExporter) Export(_ context.Context, records []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, record := range records {
		e.messages = append(e.messages, record.Body().AsString())
	}
	return nil
}

func (*configWarningExporter) Shutdown(context.Context) error   { return nil }
func (*configWarningExporter) ForceFlush(context.Context) error { return nil }

func TestDeclaredGitClientsWarningsPerCaller(t *testing.T) {
	legacy := strings.Replace(gitDeclarationFixture, "source = \"go\"", "source = \"go\"\nas-sdk = true", 1)
	duplicate := `
[sdks.go.scopes."./modules/caller"]
is-module = true
name = "caller"
clients = ["github.com/example/first"]
`
	for _, test := range []struct {
		name     string
		data     string
		warnings int
		clients  []string
	}{
		{"reconciled", legacy + duplicate, 2, []string{"github.com/example/first"}},
		{"conflicting", legacy + strings.Replace(duplicate, `name = "caller"`, `name = "conflicting"`, 1), 1, nil},
		{"unknown field", legacy + duplicate + "unknown = true\n", 0, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := []byte(test.data)
			read := func(rel string) ([]byte, bool, error) { return data, rel == "dagger.toml", nil }
			for range 2 {
				export := &configWarningExporter{}
				provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(export)))
				ctx := telemetry.WithLoggerProvider(t.Context(), provider)
				ctx = slog.ContextWithColorMode(ctx, true)
				got, err := DeclaredGitClients(ctx, "modules/caller", read)
				require.NoError(t, err)
				require.Equal(t, test.clients, got)
				require.NoError(t, provider.Shutdown(context.Background()))
				require.Len(t, export.messages, test.warnings)
				if test.warnings > 0 {
					require.Contains(t, export.messages[0], "unsupported field modules.go.as-sdk")
				}
				if test.warnings > 1 {
					require.Contains(t, export.messages[1], "reconciled scope keys")
				}
			}
		})
	}
}
