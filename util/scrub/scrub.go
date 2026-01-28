package scrub

import (
	"regexp"
	"strings"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/vito/midterm"
)

type scrubber struct {
	re     *regexp.Regexp
	sample string
	repl   string
}

const (
	privateIP = `10\.\d+\.\d+\.\d+`
	month     = `Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec`
)

func Stabilize(out string) string {
	for _, s := range scrubs {
		out = s.re.ReplaceAllString(out, s.repl)
	}
	return out
}

var scrubs = []scrubber{
	// Redis logs
	{
		regexp.MustCompile(`\d+:([MC]) \d+ (` + month + `) 20\d+ \d+:\d+:\d+\.\d+`),
		"7:C 1 Jan 2020 00:00:00.000",
		"X:M XX XXX 20XX XX:XX:XX.XXX",
	},
	{
		regexp.MustCompile(`Redis version=\d+.\d+.\d+`),
		"* Redis version=7.4.1, bits=64, commit=00000000, modified=0",
		"Redis version=X.X.X",
	},
	{
		regexp.MustCompile(`\bpid=\d+\b`),
		"pid=8",
		"pid=X",
	},
	// Durations
	{
		regexp.MustCompile(`\b(\d+m)?\d+(\.\d+)?s\b`),
		"1m2.345s",
		"X.Xs",
	},
	// IP addresses
	{
		regexp.MustCompile(`\[::ffff:` + privateIP + `\]:\d+:`),
		"[::ffff:10.89.0.8]:53172:",
		"[::ffff:10.XX.XX.XX]:XXXXX:",
	},
	{
		regexp.MustCompile(`\b` + privateIP + `\b`),
		"10.89.0.8",
		"10.XX.XX.XX",
	},
	// time.Now().String()
	{
		regexp.MustCompile(`\b20\d{2}-\d{2}-\d{2} \d+:\d+:\d+\.\d+ \+\d{4} UTC m=\+\d+.\d+\b`),
		"2024-09-12 10:02:03.4567 +0000 UTC m=+0.987654321",
		"20XX-XX-XX XX:XX:XX.XXXX +XXXX UTC m=+X.X",
	},
	// datetime.datetime.now()
	{
		regexp.MustCompile(`\b20\d{2}-\d{2}-\d{2} \d+:\d+:\d+\.\d+`),
		"2024-09-12 10:02:03.4567",
		"20XX-XX-XX XX:XX:XX.XXXX",
	},
	// new Date().toISOString()
	{
		regexp.MustCompile(`\b20\d{2}-\d{2}-\d{2}T\d+:\d+:\d+\.\d+Z\b`),
		"2024-09-25T20:47:16.793Z",
		"XXXX-XX-XXTXX:XX:XX.XXXZ",
	},
	// Dates
	{
		regexp.MustCompile(`\b20\d{2}-\d{2}-\d{2}\b`),
		"2024-09-12",
		"20XX-XX-XX",
	},
	{
		regexp.MustCompile(`\b\d+/(` + month + `)/20\d{2}\b`),
		"12/Jan/2024",
		"XX/XXX/20XX",
	},
	// Times
	{
		regexp.MustCompile(`\b\d+:\d+:\d+\b`),
		"12:34:56",
		"XX:XX:XX",
	},
	// *.dagger.local
	{
		regexp.MustCompile(`[a-z0-9]+\.[a-z0-9]+\.dagger\.local`),
		"iujpijlqnc7me.tun3vdbg35c6q.dagger.local",
		"xxxxxxxxxxxxx.xxxxxxxxxxxxx.dagger.local",
	},
	// version=
	{
		regexp.MustCompile(`version=v[a-fv0-9.-]+`), // "v" is in "dev" :)
		"version=v0.18.13-250710134709-7edd4496ecc1",
		"version=vX.X.X-xxxxxxxxxxxx-xxxxxxxxxxxx",
	},
	// Trailing whitespace
	{
		regexp.MustCompile(`\s*` + regexp.QuoteMeta(midterm.Reset.Render())),
		"	        \x1b[0m", // from logs (which ignore NO_COLOR for the reset - bug)
		"",
	},
	{
		regexp.MustCompile(`[ \t]\n`),
		"foo	        \nbar",
		"\n",
	},
	// Dagger Cloud logged out
	{
		regexp.MustCompile(`\b` + strings.Join(idtui.SkipLoggedOutTraceMsgEnvs, "|") + `\b`),
		"SHUTUP",
		"DAGGER_NO_NAG",
	},
	// Uploads
	{
		regexp.MustCompile(`upload ([^ ]+) from [a-z0-9]+ \(client id: [a-z0-9]+, session id: [a-z0-9]+\)`),
		"upload /app/dagql/idtui/viztest/broken from uiyf0ymsapvxhhgrsamouqh8h (client id: xutan9vz6sjtdcrqcqrd6cvh4, session id: u5mj1p0sw07k6579r3xcuiuf3)",
		"upload /XXX/XXX/XXX from XXXXXXXXXXX (client id: XXXXXXXXXXX, session id: XXXXXXXXXXX)",
	},
	{
		regexp.MustCompile(`\(include: [^)]+\)`),
		"(include: dagql/idtui/viztest/broken/dagger.json, dagql/idtui/viztest/broken/**/*, **/go.mod, **/go.sum, **/go.work, **/go.work.sum, **/vendor/, **/*.go)",
		"(include: XXXXXXXXXXX)",
	},
	{
		regexp.MustCompile(`\(exclude: [^)]+\)`),
		"(exclude: **/.git)",
		"(exclude: XXXXXXXXXXX)",
	},
	// sha256:... digests
	{
		regexp.MustCompile(`sha256:[a-f0-9]{64}`),
		// an almost natural deadbeef!
		"docker.io/library/alpine:latest@sha256:beefdbd8a1da6d2915566fde36db9db0b524eb737fc57cd1367effd16dc0d06d",
		"sha256:XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX",
	},
	// xxh3:... digests
	{
		regexp.MustCompile(`xxh3:[a-f0-9]{16}`),
		// an almost natural deadbeef!
		"xxh3:0724b85200c28a1d",
		"xxh3:XXXXXXXXXXXXXXXX",
	},
	// byte quantities
	{
		regexp.MustCompile(`\d+(\.\d+)?\s?(B|kB|MB|GB|TB)`),
		"9.3 kB",
		"X.X B",
	},
	{
		regexp.MustCompile(`\d+?\sbytes`),
		"1048576000 bytes",
		"XX bytes",
	},
	// duration quantities
	{
		regexp.MustCompile(`\d+(\.\d+)?(µs|ms|s)`),
		"4.063ms",
		"X.Xs",
	},
	{
		regexp.MustCompile(`\d+(\.\d+)?\s(seconds|minutes)`),
		"4.063 seconds",
		"X.X seconds",
	},
	// Memory overcommit warning for redis
	{
		regexp.MustCompile(`.+WARNING Memory overcommit.+\n`),
		"# WARNING Memory overcommit must be enabled! Without it, a background save or replication may fail under low memory condition. Being disabled, it can also cause failures without low memory condition, see https://github.com/jemalloc/jemalloc/issues/1328. To fix this issue add 'vm.overcommit_memory = 1' to /etc/sysctl.conf and then reboot or run the command 'sysctl vm.overcommit_memory=1' for this to take effect.\n",
		"",
	},
	// Container constructor CACHED label; this is cached on the dagql level and can easily show up
	// as either CACHED or not depending on anything else concurrently running against the engine.
	// It's not something we particularly care about, so we just scrub it.
	{
		regexp.MustCompile(`\$ container: Container! X\.Xs CACHED`),
		idtui.IconCached + " container: Container! X.Xs CACHED",
		idtui.IconSuccess + " container: Container! X.Xs",
	},
	// Container.from cache status depends on whether the ref is pinned or not (which may be a bug)
	{
		regexp.MustCompile(`✔ \.from\(address: "alpine"\): Container! X\.Xs`),
		idtui.IconSuccess + ` .from(address: "alpine"): Container! X.Xs`,
		idtui.IconCached + ` .from(address: "alpine"): Container! X.Xs CACHED`,
	},
	{
		regexp.MustCompile(`✔ \.from\(address: "alpine:latest"\): Container! X\.Xs`),
		idtui.IconSuccess + ` .from(address: "alpine:latest"): Container! X.Xs`,
		idtui.IconCached + ` .from(address: "alpine:latest"): Container! X.Xs CACHED`,
	},
	{
		regexp.MustCompile(`✔ Container\.from\(address: "alpine"\): Container! X\.Xs`),
		idtui.IconSuccess + ` Container.from(address: "alpine"): Container! X.Xs`,
		idtui.IconCached + ` Container.from(address: "alpine"): Container! X.Xs CACHED`,
	},
	{
		regexp.MustCompile(`✔ Container\.from\(address: "alpine:latest"\): Container! X\.Xs`),
		idtui.IconSuccess + ` Container.from(address: "alpine:latest"): Container! X.Xs`,
		idtui.IconCached + ` Container.from(address: "alpine:latest"): Container! X.Xs CACHED`,
	},
	{
		regexp.MustCompile(`, line \d+, in`),
		"File \"/src/some/path/to/module.py\", line 386, in some_func",
		", line XXX, in",
	},
	{
		regexp.MustCompile(`0x[0-9a-f]+`),
		"File \"<@beartype(dagger.client.gen.Container.sync) at 0x7f80cbe716c0>\", line 12, in sync",
		"0xXXXXXXXXXXXX",
	},
	{
		telemetry.ErrorOriginRegex,
		`local path "/app/dagql/idtui/viztest/broken-dep-sdk/invalid/unknown" does not exist [traceparent:8a3363a90e36b75e39d5c26de0286cc9-fb8de840ef712c7b]`,
		` [traceparent:00000000000000000000000000000000-0000000000000000]`,
	},
	{
		regexp.MustCompile(`[⡀⡄⡆⡇⣇⣧⣷⣿]+`),
		`╰╴✘ roll-up pseudo-check span X.Xs ⣷⡆⡆⡆ ERROR`,
		`⡀⡄⡆⡇⣇⣧⣷⣿`,
	},
}
