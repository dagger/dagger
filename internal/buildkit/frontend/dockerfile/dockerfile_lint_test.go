package dockerfile

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"regexp"
	"sort"
	"testing"
	"time"

	"github.com/containerd/continuity/fs/fstest"
	"github.com/dagger/dagger/buildkit/client"
	"github.com/dagger/dagger/buildkit/frontend/dockerfile/linter"
	"github.com/dagger/dagger/buildkit/frontend/dockerui"
	gateway "github.com/dagger/dagger/buildkit/frontend/gateway/client"

	"github.com/dagger/dagger/buildkit/frontend/subrequests/lint"
	"github.com/dagger/dagger/buildkit/util/testutil/integration"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/fsutil"
)

var lintTests = integration.TestFuncs(
	testRuleCheckOption,
	testStageName,
	testNoEmptyContinuation,
	testConsistentInstructionCasing,
	testDuplicateStageName,
	testReservedStageName,
	testJSONArgsRecommended,
	testMaintainerDeprecated,
	testWarningsBeforeError,
	testUndeclaredArg,
	testWorkdirRelativePath,
	testUnmatchedVars,
	testMultipleInstructionsDisallowed,
	testLegacyKeyValueFormat,
	testBaseImagePlatformMismatch,
	testAllTargetUnmarshal,
	testRedundantTargetPlatform,
	testSecretsUsedInArgOrEnv,
	testInvalidDefaultArgInFrom,
	testFromPlatformFlagConstDisallowed,
	testCopyIgnoredFiles,
)

func testCopyIgnoredFiles(t *testing.T, sb integration.Sandbox) {
	dockerignore := []byte(`
Dockerfile
`)
	dockerfile := []byte(`
FROM scratch
COPY Dockerfile .
ADD Dockerfile /windy
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile:           dockerfile,
		DockerIgnore:         dockerignore,
		BuildErrLocation:     3,
		StreamBuildErrRegexp: regexp.MustCompile(`failed to solve: failed to compute cache key: failed to calculate checksum of ref [^\s]+ "/Dockerfile": not found`),
	})

	dockerfile = []byte(`# check=experimental=CopyIgnoredFile
FROM scratch
COPY Dockerfile .
ADD Dockerfile /windy
`)

	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile:           dockerfile,
		DockerIgnore:         dockerignore,
		BuildErrLocation:     3,
		StreamBuildErrRegexp: regexp.MustCompile(`failed to solve: failed to compute cache key: failed to calculate checksum of ref [^\s]+ "/Dockerfile": not found`),
		Warnings: []expectedLintWarning{
			{
				RuleName:    "CopyIgnoredFile",
				Description: "Attempting to Copy file that is excluded by .dockerignore",
				Detail:      `Attempting to Copy file "Dockerfile" that is excluded by .dockerignore`,
				URL:         "https://docs.docker.com/go/dockerfile/rule/copy-ignored-file/",
				Level:       1,
				Line:        3,
			},
			{
				RuleName:    "CopyIgnoredFile",
				Description: "Attempting to Copy file that is excluded by .dockerignore",
				Detail:      `Attempting to Add file "Dockerfile" that is excluded by .dockerignore`,
				URL:         "https://docs.docker.com/go/dockerfile/rule/copy-ignored-file/",
				Level:       1,
				Line:        4,
			},
		},
	})

	dockerfile = []byte(`# check=skip=all;experimental=CopyIgnoredFile
FROM scratch
COPY Dockerfile .
ADD Dockerfile /windy
`)

	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile:           dockerfile,
		DockerIgnore:         dockerignore,
		BuildErrLocation:     3,
		StreamBuildErrRegexp: regexp.MustCompile(`failed to solve: failed to compute cache key: failed to calculate checksum of ref [^\s]+ "/Dockerfile": not found`),
		Warnings: []expectedLintWarning{
			{
				RuleName:    "CopyIgnoredFile",
				Description: "Attempting to Copy file that is excluded by .dockerignore",
				Detail:      `Attempting to Copy file "Dockerfile" that is excluded by .dockerignore`,
				URL:         "https://docs.docker.com/go/dockerfile/rule/copy-ignored-file/",
				Level:       1,
				Line:        3,
			},
			{
				RuleName:    "CopyIgnoredFile",
				Description: "Attempting to Copy file that is excluded by .dockerignore",
				Detail:      `Attempting to Add file "Dockerfile" that is excluded by .dockerignore`,
				URL:         "https://docs.docker.com/go/dockerfile/rule/copy-ignored-file/",
				Level:       1,
				Line:        4,
			},
		},
	})

	dockerignore = []byte(`
foobar
`)
	dockerfile = []byte(`
FROM scratch AS base
COPY Dockerfile /foobar
ADD Dockerfile /windy

FROM base
COPY --from=base /foobar /Dockerfile
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile:   dockerfile,
		DockerIgnore: dockerignore,
	})
}

func testSecretsUsedInArgOrEnv(t *testing.T, sb integration.Sandbox) {
	dockerfile := []byte(`
FROM scratch
ARG SECRET_PASSPHRASE
ENV SUPER_Secret=foo
ENV password=bar secret=baz
ARG super_duper_secret_token=foo auth=bar
ENV apikey=bar sunflower=foo
ENV git_key=
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "SecretsUsedInArgOrEnv",
				Description: "Sensitive data should not be used in the ARG or ENV commands",
				Detail:      `Do not use ARG or ENV instructions for sensitive data (ARG "SECRET_PASSPHRASE")`,
				URL:         "https://docs.docker.com/go/dockerfile/rule/secrets-used-in-arg-or-env/",
				Level:       1,
				Line:        3,
			},
			{
				RuleName:    "SecretsUsedInArgOrEnv",
				Description: "Sensitive data should not be used in the ARG or ENV commands",
				Detail:      `Do not use ARG or ENV instructions for sensitive data (ENV "SUPER_Secret")`,
				URL:         "https://docs.docker.com/go/dockerfile/rule/secrets-used-in-arg-or-env/",
				Level:       1,
				Line:        4,
			},
			{
				RuleName:    "SecretsUsedInArgOrEnv",
				Description: "Sensitive data should not be used in the ARG or ENV commands",
				Detail:      `Do not use ARG or ENV instructions for sensitive data (ENV "password")`,
				URL:         "https://docs.docker.com/go/dockerfile/rule/secrets-used-in-arg-or-env/",
				Level:       1,
				Line:        5,
			},
			{
				RuleName:    "SecretsUsedInArgOrEnv",
				Description: "Sensitive data should not be used in the ARG or ENV commands",
				Detail:      `Do not use ARG or ENV instructions for sensitive data (ENV "secret")`,
				URL:         "https://docs.docker.com/go/dockerfile/rule/secrets-used-in-arg-or-env/",
				Level:       1,
				Line:        5,
			},
			{
				RuleName:    "SecretsUsedInArgOrEnv",
				Description: "Sensitive data should not be used in the ARG or ENV commands",
				Detail:      `Do not use ARG or ENV instructions for sensitive data (ARG "auth")`,
				URL:         "https://docs.docker.com/go/dockerfile/rule/secrets-used-in-arg-or-env/",
				Level:       1,
				Line:        6,
			},
			{
				RuleName:    "SecretsUsedInArgOrEnv",
				Description: "Sensitive data should not be used in the ARG or ENV commands",
				Detail:      `Do not use ARG or ENV instructions for sensitive data (ARG "super_duper_secret_token")`,
				URL:         "https://docs.docker.com/go/dockerfile/rule/secrets-used-in-arg-or-env/",
				Level:       1,
				Line:        6,
			},
			{
				RuleName:    "SecretsUsedInArgOrEnv",
				Description: "Sensitive data should not be used in the ARG or ENV commands",
				Detail:      `Do not use ARG or ENV instructions for sensitive data (ENV "apikey")`,
				URL:         "https://docs.docker.com/go/dockerfile/rule/secrets-used-in-arg-or-env/",
				Level:       1,
				Line:        7,
			},
			{
				RuleName:    "SecretsUsedInArgOrEnv",
				Description: "Sensitive data should not be used in the ARG or ENV commands",
				Detail:      `Do not use ARG or ENV instructions for sensitive data (ENV "git_key")`,
				URL:         "https://docs.docker.com/go/dockerfile/rule/secrets-used-in-arg-or-env/",
				Level:       1,
				Line:        8,
			},
		},
	})
}

func testAllTargetUnmarshal(t *testing.T, sb integration.Sandbox) {
	dockerfile := []byte(`
FROM scratch AS first
COPY $foo .

FROM scratch AS second
COPY $bar .
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "UndefinedVar",
				Description: "Variables should be defined before their use",
				URL:         "https://docs.docker.com/go/dockerfile/rule/undefined-var/",
				Detail:      "Usage of undefined variable '$bar'",
				Level:       1,
				Line:        6,
			},
		},
		UnmarshalWarnings: []expectedLintWarning{
			{
				RuleName:    "UndefinedVar",
				Description: "Variables should be defined before their use",
				URL:         "https://docs.docker.com/go/dockerfile/rule/undefined-var/",
				Detail:      "Usage of undefined variable '$foo'",
				Level:       1,
				Line:        3,
			},
			{
				RuleName:    "UndefinedVar",
				Description: "Variables should be defined before their use",
				URL:         "https://docs.docker.com/go/dockerfile/rule/undefined-var/",
				Detail:      "Usage of undefined variable '$bar'",
				Level:       1,
				Line:        6,
			},
		},
	})
}

func testRuleCheckOption(t *testing.T, sb integration.Sandbox) {
	dockerfile := []byte(`#check=skip=all
FROM scratch as base
copy Dockerfile .
`)
	checkLinterWarnings(t, sb, &lintTestParams{Dockerfile: dockerfile})

	dockerfile = []byte(`#check=skip=all;error=true
FROM scratch as base
copy Dockerfile .
`)
	checkLinterWarnings(t, sb, &lintTestParams{Dockerfile: dockerfile})

	dockerfile = []byte(`#check=skip=ConsistentInstructionCasing,FromAsCasing
FROM scratch as base
copy Dockerfile .
`)
	checkLinterWarnings(t, sb, &lintTestParams{Dockerfile: dockerfile})

	dockerfile = []byte(`#check=skip=ConsistentInstructionCasing,FromAsCasing;error=true
FROM scratch as base
copy Dockerfile .
`)
	checkLinterWarnings(t, sb, &lintTestParams{Dockerfile: dockerfile})

	dockerfile = []byte(`#check=skip=ConsistentInstructionCasing
FROM scratch as base
copy Dockerfile .
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "FromAsCasing",
				Description: "The 'as' keyword should match the case of the 'from' keyword",
				URL:         "https://docs.docker.com/go/dockerfile/rule/from-as-casing/",
				Detail:      "'as' and 'FROM' keywords' casing do not match",
				Line:        2,
				Level:       1,
			},
		},
	})

	dockerfile = []byte(`#check=skip=ConsistentInstructionCasing;error=true
FROM scratch as base
copy Dockerfile .
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "FromAsCasing",
				Description: "The 'as' keyword should match the case of the 'from' keyword",
				URL:         "https://docs.docker.com/go/dockerfile/rule/from-as-casing/",
				Detail:      "'as' and 'FROM' keywords' casing do not match",
				Line:        2,
				Level:       1,
			},
		},
		StreamBuildErr:    "failed to solve: lint violation found for rules: FromAsCasing",
		UnmarshalBuildErr: "lint violation found for rules: FromAsCasing",
		BuildErrLocation:  2,
	})

	dockerfile = []byte(`#check=skip=all
FROM scratch as base
copy Dockerfile .
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "FromAsCasing",
				Description: "The 'as' keyword should match the case of the 'from' keyword",
				URL:         "https://docs.docker.com/go/dockerfile/rule/from-as-casing/",
				Detail:      "'as' and 'FROM' keywords' casing do not match",
				Line:        2,
				Level:       1,
			},
		},
		StreamBuildErr:    "failed to solve: lint violation found for rules: FromAsCasing",
		UnmarshalBuildErr: "lint violation found for rules: FromAsCasing",
		BuildErrLocation:  2,
		FrontendAttrs: map[string]string{
			"build-arg:BUILDKIT_DOCKERFILE_CHECK": "skip=ConsistentInstructionCasing;error=true",
		},
	})

	dockerfile = []byte(`#check=error=true
FROM scratch as base
copy Dockerfile .
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		FrontendAttrs: map[string]string{
			"build-arg:BUILDKIT_DOCKERFILE_CHECK": "skip=all",
		},
	})
}

func testStageName(t *testing.T, sb integration.Sandbox) {
	dockerfile := []byte(`
# warning: stage name should be lowercase
FROM scratch AS BadStageName

# warning: 'as' should match 'FROM' cmd casing.
FROM scratch as base2

FROM scratch AS base3
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "StageNameCasing",
				Description: "Stage names should be lowercase",
				URL:         "https://docs.docker.com/go/dockerfile/rule/stage-name-casing/",
				Detail:      "Stage name 'BadStageName' should be lowercase",
				Line:        3,
				Level:       1,
			},
			{
				RuleName:    "FromAsCasing",
				Description: "The 'as' keyword should match the case of the 'from' keyword",
				URL:         "https://docs.docker.com/go/dockerfile/rule/from-as-casing/",
				Detail:      "'as' and 'FROM' keywords' casing do not match",
				Line:        6,
				Level:       1,
			},
		},
	})

	dockerfile = []byte(`
# warning: 'AS' should match 'from' cmd casing.
from scratch AS base

from scratch as base2
`)

	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "FromAsCasing",
				Description: "The 'as' keyword should match the case of the 'from' keyword",
				URL:         "https://docs.docker.com/go/dockerfile/rule/from-as-casing/",
				Detail:      "'AS' and 'from' keywords' casing do not match",
				Line:        3,
				Level:       1,
			},
		},
	})
}

func testNoEmptyContinuation(t *testing.T, sb integration.Sandbox) {
	dockerfile := []byte(`
FROM scratch
# warning: empty continuation line
COPY Dockerfile \

.
COPY Dockerfile \
.
`)

	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "NoEmptyContinuation",
				Description: "Empty continuation lines will become errors in a future release",
				URL:         "https://docs.docker.com/go/dockerfile/rule/no-empty-continuation/",
				Detail:      "Empty continuation line",
				Level:       1,
				Line:        6,
			},
		},
	})
}

func testConsistentInstructionCasing(t *testing.T, sb integration.Sandbox) {
	dockerfile := []byte(`
# warning: 'FROM' should be either lowercased or uppercased
From scratch as base
FROM scratch AS base2
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "ConsistentInstructionCasing",
				Description: "All commands within the Dockerfile should use the same casing (either upper or lower)",
				URL:         "https://docs.docker.com/go/dockerfile/rule/consistent-instruction-casing/",
				Detail:      "Command 'From' should match the case of the command majority (uppercase)",
				Level:       1,
				Line:        3,
			},
		},
	})

	dockerfile = []byte(`
FROM scratch
# warning: 'copy' should match command majority's casing (uppercase)
copy Dockerfile /foo
COPY Dockerfile /bar
`)

	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "ConsistentInstructionCasing",
				Description: "All commands within the Dockerfile should use the same casing (either upper or lower)",
				URL:         "https://docs.docker.com/go/dockerfile/rule/consistent-instruction-casing/",
				Detail:      "Command 'copy' should match the case of the command majority (uppercase)",
				Line:        4,
				Level:       1,
			},
		},
	})

	dockerfile = []byte(`
# warning: 'frOM' should be either lowercased or uppercased
frOM scratch as base
from scratch as base2
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "ConsistentInstructionCasing",
				Description: "All commands within the Dockerfile should use the same casing (either upper or lower)",
				URL:         "https://docs.docker.com/go/dockerfile/rule/consistent-instruction-casing/",
				Detail:      "Command 'frOM' should match the case of the command majority (lowercase)",
				Line:        3,
				Level:       1,
			},
		},
	})

	dockerfile = []byte(`
from scratch
# warning: 'COPY' should match command majority's casing (lowercase)
COPY Dockerfile /foo
copy Dockerfile /bar
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "ConsistentInstructionCasing",
				Description: "All commands within the Dockerfile should use the same casing (either upper or lower)",
				URL:         "https://docs.docker.com/go/dockerfile/rule/consistent-instruction-casing/",
				Detail:      "Command 'COPY' should match the case of the command majority (lowercase)",
				Line:        4,
				Level:       1,
			},
		},
	})

	dockerfile = []byte(`
# warning: 'from' should match command majority's casing (uppercase)
from scratch
COPY Dockerfile /foo
COPY Dockerfile /bar
COPY Dockerfile /baz
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "ConsistentInstructionCasing",
				Description: "All commands within the Dockerfile should use the same casing (either upper or lower)",
				URL:         "https://docs.docker.com/go/dockerfile/rule/consistent-instruction-casing/",
				Detail:      "Command 'from' should match the case of the command majority (uppercase)",
				Line:        3,
				Level:       1,
			},
		},
	})

	dockerfile = []byte(`
# warning: 'FROM' should match command majority's casing (lowercase)
FROM scratch
copy Dockerfile /foo
copy Dockerfile /bar
copy Dockerfile /baz
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "ConsistentInstructionCasing",
				Description: "All commands within the Dockerfile should use the same casing (either upper or lower)",
				URL:         "https://docs.docker.com/go/dockerfile/rule/consistent-instruction-casing/",
				Detail:      "Command 'FROM' should match the case of the command majority (lowercase)",
				Line:        3,
				Level:       1,
			},
		},
	})

	dockerfile = []byte(`
from scratch
copy Dockerfile /foo
copy Dockerfile /bar
`)
	checkLinterWarnings(t, sb, &lintTestParams{Dockerfile: dockerfile})

	dockerfile = []byte(`
FROM scratch
COPY Dockerfile /foo
COPY Dockerfile /bar
`)
	checkLinterWarnings(t, sb, &lintTestParams{Dockerfile: dockerfile})

	dockerfile = []byte(fmt.Sprintf(
		`
FROM %s
RUN <<'EOT'
%s
EOT
`,
		integration.UnixOrWindows("alpine", "nanoserver"),
		integration.UnixOrWindows("env", "set"),
	))
	checkLinterWarnings(t, sb, &lintTestParams{Dockerfile: dockerfile})
}

func testDuplicateStageName(t *testing.T, sb integration.Sandbox) {
	dockerfile := []byte(`
FROM scratch AS b
FROM scratch AS b
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "DuplicateStageName",
				Description: "Stage names should be unique",
				URL:         "https://docs.docker.com/go/dockerfile/rule/duplicate-stage-name/",
				Detail:      "Duplicate stage name \"b\", stage names should be unique",
				Level:       1,
				Line:        3,
			},
		},
	})

	dockerfile = []byte(`
FROM scratch AS b1
FROM scratch AS b2
`)
	checkLinterWarnings(t, sb, &lintTestParams{Dockerfile: dockerfile})
}

func testReservedStageName(t *testing.T, sb integration.Sandbox) {
	dockerfile := []byte(`
FROM scratch AS scratch
FROM scratch AS context
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "ReservedStageName",
				Description: "Reserved words should not be used as stage names",
				URL:         "https://docs.docker.com/go/dockerfile/rule/reserved-stage-name/",
				Detail:      "Stage name should not use the same name as reserved stage \"scratch\"",
				Level:       1,
				Line:        2,
			},
			{
				RuleName:    "ReservedStageName",
				Description: "Reserved words should not be used as stage names",
				URL:         "https://docs.docker.com/go/dockerfile/rule/reserved-stage-name/",
				Detail:      "Stage name should not use the same name as reserved stage \"context\"",
				Level:       1,
				Line:        3,
			},
		},
	})

	// Using a reserved name as the base without a set name
	// or a non-reserved name shouldn't trigger a lint warning.
	dockerfile = []byte(`
FROM scratch
FROM scratch AS a
`)
	checkLinterWarnings(t, sb, &lintTestParams{Dockerfile: dockerfile})
}

func testJSONArgsRecommended(t *testing.T, sb integration.Sandbox) {
	dockerfile := []byte(`
FROM scratch
CMD mycommand
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "JSONArgsRecommended",
				Description: "JSON arguments recommended for ENTRYPOINT/CMD to prevent unintended behavior related to OS signals",
				URL:         "https://docs.docker.com/go/dockerfile/rule/json-args-recommended/",
				Detail:      "JSON arguments recommended for CMD to prevent unintended behavior related to OS signals",
				Level:       1,
				Line:        3,
			},
		},
	})

	dockerfile = []byte(`
FROM scratch
ENTRYPOINT mycommand
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "JSONArgsRecommended",
				URL:         "https://docs.docker.com/go/dockerfile/rule/json-args-recommended/",
				Description: "JSON arguments recommended for ENTRYPOINT/CMD to prevent unintended behavior related to OS signals",
				Detail:      "JSON arguments recommended for ENTRYPOINT to prevent unintended behavior related to OS signals",
				Level:       1,
				Line:        3,
			},
		},
	})

	dockerfile = []byte(`
FROM scratch
SHELL ["/usr/bin/customshell"]
CMD mycommand
`)
	checkLinterWarnings(t, sb, &lintTestParams{Dockerfile: dockerfile})

	dockerfile = []byte(`
FROM scratch
SHELL ["/usr/bin/customshell"]
ENTRYPOINT mycommand
`)
	checkLinterWarnings(t, sb, &lintTestParams{Dockerfile: dockerfile})

	dockerfile = []byte(`
FROM scratch AS base
SHELL ["/usr/bin/customshell"]

FROM base
CMD mycommand
`)
	checkLinterWarnings(t, sb, &lintTestParams{Dockerfile: dockerfile})

	dockerfile = []byte(`
FROM scratch AS base
SHELL ["/usr/bin/customshell"]

FROM base
ENTRYPOINT mycommand
`)
	checkLinterWarnings(t, sb, &lintTestParams{Dockerfile: dockerfile})
}

func testMaintainerDeprecated(t *testing.T, sb integration.Sandbox) {
	dockerfile := []byte(`
FROM scratch
MAINTAINER me@example.org
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "MaintainerDeprecated",
				Description: "The MAINTAINER instruction is deprecated, use a label instead to define an image author",
				URL:         "https://docs.docker.com/go/dockerfile/rule/maintainer-deprecated/",
				Detail:      "Maintainer instruction is deprecated in favor of using label",
				Level:       1,
				Line:        3,
			},
		},
	})

	dockerfile = []byte(`
FROM scratch
LABEL org.opencontainers.image.authors="me@example.org"
`)
	checkLinterWarnings(t, sb, &lintTestParams{Dockerfile: dockerfile})
}

func testWarningsBeforeError(t *testing.T, sb integration.Sandbox) {
	dockerfile := []byte(`
FROM scratch AS BadStageName
MAINTAINER me@example.org
BADCMD
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "StageNameCasing",
				Description: "Stage names should be lowercase",
				URL:         "https://docs.docker.com/go/dockerfile/rule/stage-name-casing/",
				Detail:      "Stage name 'BadStageName' should be lowercase",
				Line:        2,
				Level:       1,
			},
			{
				RuleName:    "MaintainerDeprecated",
				Description: "The MAINTAINER instruction is deprecated, use a label instead to define an image author",
				URL:         "https://docs.docker.com/go/dockerfile/rule/maintainer-deprecated/",
				Detail:      "Maintainer instruction is deprecated in favor of using label",
				Level:       1,
				Line:        3,
			},
		},
		StreamBuildErr:    "failed to solve: dockerfile parse error on line 4: unknown instruction: BADCMD",
		UnmarshalBuildErr: "dockerfile parse error on line 4: unknown instruction: BADCMD",
		BuildErrLocation:  4,
	})
}

func testUndeclaredArg(t *testing.T, sb integration.Sandbox) {
	dockerfile := []byte(`
ARG base=scratch
FROM $base
COPY Dockerfile .
`)
	checkLinterWarnings(t, sb, &lintTestParams{Dockerfile: dockerfile})

	dockerfile = []byte(`
ARG BUILDPLATFORM=linux/amd64
FROM --platform=$BUILDPLATFORM scratch
COPY Dockerfile .
`)
	checkLinterWarnings(t, sb, &lintTestParams{Dockerfile: dockerfile})

	dockerfile = []byte(`
FROM --platform=$BUILDPLATFORM scratch
COPY Dockerfile .
`)
	checkLinterWarnings(t, sb, &lintTestParams{Dockerfile: dockerfile})

	dockerfile = []byte(`
FROM --platform=$BULIDPLATFORM scratch
COPY Dockerfile .
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "UndefinedArgInFrom",
				Description: "FROM command must use declared ARGs",
				URL:         "https://docs.docker.com/go/dockerfile/rule/undefined-arg-in-from/",
				Detail:      "FROM argument 'BULIDPLATFORM' is not declared (did you mean BUILDPLATFORM?)",
				Level:       1,
				Line:        2,
			},
		},
		StreamBuildErr:    "failed to solve: empty platform value from expression $BULIDPLATFORM (did you mean BUILDPLATFORM?)",
		UnmarshalBuildErr: "empty platform value from expression $BULIDPLATFORM (did you mean BUILDPLATFORM?)",
		BuildErrLocation:  2,
	})

	osName := integration.UnixOrWindows("linux", "windows")
	baseImg := integration.UnixOrWindows("busybox", "nanoserver")
	dockerfile = []byte(fmt.Sprintf(
		`
ARG MY_OS=%s
ARG MY_ARCH=amd64
FROM --platform=%s/${MYARCH} %s
COPY Dockerfile .
	`,
		osName, osName, baseImg))

	osStr := integration.UnixOrWindows("linux", "windows")
	streamBuildErr := fmt.Sprintf(
		"failed to solve: failed to parse platform %s/${MYARCH}: \"\" is an invalid component of \"%s/\": platform specifier component must match \"^[A-Za-z0-9_-]+$\": invalid argument (did you mean MY_ARCH?)",
		osStr, osStr)
	unmarshalBuildErr := fmt.Sprintf(
		"failed to parse platform %s/${MYARCH}: \"\" is an invalid component of \"%s/\": platform specifier component must match \"^[A-Za-z0-9_-]+$\": invalid argument (did you mean MY_ARCH?)",
		osStr, osStr)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "UndefinedArgInFrom",
				Description: "FROM command must use declared ARGs",
				URL:         "https://docs.docker.com/go/dockerfile/rule/undefined-arg-in-from/",
				Detail:      "FROM argument 'MYARCH' is not declared (did you mean MY_ARCH?)",
				Level:       1,
				Line:        4,
			},
		},
		StreamBuildErr:    streamBuildErr,
		UnmarshalBuildErr: unmarshalBuildErr,
		BuildErrLocation:  4,
	})

	dockerfile = []byte(fmt.Sprintf(
		`
ARG tag=latest
FROM %s:${tag}${version} AS b
COPY Dockerfile .
`,
		baseImg))
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "UndefinedArgInFrom",
				Description: "FROM command must use declared ARGs",
				URL:         "https://docs.docker.com/go/dockerfile/rule/undefined-arg-in-from/",
				Detail:      "FROM argument 'version' is not declared",
				Level:       1,
				Line:        3,
			},
		},
	})
}

func testWorkdirRelativePath(t *testing.T, sb integration.Sandbox) {
	dockerfile := []byte(`
FROM scratch
WORKDIR app/
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "WorkdirRelativePath",
				Description: "Relative workdir without an absolute workdir declared within the build can have unexpected results if the base image changes",
				URL:         "https://docs.docker.com/go/dockerfile/rule/workdir-relative-path/",
				Detail:      "Relative workdir \"app/\" can have unexpected results if the base image changes",
				Level:       1,
				Line:        3,
			},
		},
	})

	dockerfile = []byte(`
FROM scratch AS a
WORKDIR /app

FROM a AS b
WORKDIR subdir/
`)
	checkLinterWarnings(t, sb, &lintTestParams{Dockerfile: dockerfile})
}

func testUnmatchedVars(t *testing.T, sb integration.Sandbox) {
	dockerfile := []byte(`
FROM scratch
ARG foo
COPY Dockerfile${foo} .
`)
	checkLinterWarnings(t, sb, &lintTestParams{Dockerfile: dockerfile})

	baseImg := integration.UnixOrWindows("alpine", "nanoserver")
	dockerfile = []byte(fmt.Sprintf(
		`
FROM %s AS base
ARG foo=Dockerfile

FROM base
COPY $foo .
`,
		baseImg))
	checkLinterWarnings(t, sb, &lintTestParams{Dockerfile: dockerfile})

	dockerfile = []byte(fmt.Sprintf(
		`
FROM %s
RUN echo $PATH
`,
		baseImg))
	checkLinterWarnings(t, sb, &lintTestParams{Dockerfile: dockerfile})

	dockerfile = []byte(fmt.Sprintf(
		`
FROM %s
COPY $foo .
ARG foo=bar
RUN echo $foo
`,
		baseImg))
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "UndefinedVar",
				Description: "Variables should be defined before their use",
				URL:         "https://docs.docker.com/go/dockerfile/rule/undefined-var/",
				Detail:      "Usage of undefined variable '$foo'",
				Level:       1,
				Line:        3,
			},
		},
	})

	dockerfile = []byte(fmt.Sprintf(
		`
FROM %s
ARG DIR_BINARIES=binaries/
ARG DIR_ASSETS=assets/
ARG DIR_CONFIG=config/
COPY $DIR_ASSET .
	`,
		baseImg))
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "UndefinedVar",
				Description: "Variables should be defined before their use",
				URL:         "https://docs.docker.com/go/dockerfile/rule/undefined-var/",
				Detail:      "Usage of undefined variable '$DIR_ASSET' (did you mean $DIR_ASSETS?)",
				Level:       1,
				Line:        6,
			},
		},
	})

	dockerfile = []byte(fmt.Sprintf(
		`
FROM %s
ENV PATH=$PAHT:/tmp/bin
		`,
		baseImg))
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "UndefinedVar",
				Description: "Variables should be defined before their use",
				URL:         "https://docs.docker.com/go/dockerfile/rule/undefined-var/",
				Detail:      "Usage of undefined variable '$PAHT' (did you mean $PATH?)",
				Level:       1,
				Line:        3,
			},
		},
	})
}

func testMultipleInstructionsDisallowed(t *testing.T, sb integration.Sandbox) {
	makeLintWarning := func(instructionName string, line int) expectedLintWarning {
		return expectedLintWarning{
			RuleName:    "MultipleInstructionsDisallowed",
			Description: "Multiple instructions of the same type should not be used in the same stage",
			URL:         "https://docs.docker.com/go/dockerfile/rule/multiple-instructions-disallowed/",
			Detail:      fmt.Sprintf("Multiple %s instructions should not be used in the same stage because only the last one will be used", instructionName),
			Level:       1,
			Line:        line,
		}
	}

	dockerfile := []byte(`
FROM scratch
ENTRYPOINT ["/myapp"]
ENTRYPOINT ["/myotherapp"]
CMD ["/myapp"]
CMD ["/myotherapp"]
HEALTHCHECK CMD ["/myapp"]
HEALTHCHECK CMD ["/myotherapp"]
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			makeLintWarning("ENTRYPOINT", 3),
			makeLintWarning("CMD", 5),
			makeLintWarning("HEALTHCHECK", 7),
		},
	})

	// Still a linter warning even when broken up with another
	// command. Entrypoint is only used by the resulting image.
	dockerfile = []byte(`
FROM scratch
ENTRYPOINT ["/myapp"]
CMD ["/myapp"]
HEALTHCHECK CMD ["/myapp"]
COPY <<EOF /a.txt
Hello, World!
EOF
ENTRYPOINT ["/myotherapp"]
CMD ["/myotherapp"]
HEALTHCHECK CMD ["/myotherapp"]
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			makeLintWarning("ENTRYPOINT", 3),
			makeLintWarning("CMD", 4),
			makeLintWarning("HEALTHCHECK", 5),
		},
	})

	dockerfile = []byte(`
FROM scratch AS a
ENTRYPOINT ["/myapp"]
CMD ["/myapp"]
HEALTHCHECK CMD ["/myapp"]

FROM a AS b
ENTRYPOINT ["/myotherapp"]
CMD ["/myotherapp"]
HEALTHCHECK CMD ["/myotherapp"]
`)
	checkLinterWarnings(t, sb, &lintTestParams{Dockerfile: dockerfile})
}

func testLegacyKeyValueFormat(t *testing.T, sb integration.Sandbox) {
	dockerfile := []byte(`
FROM scratch
ENV testkey value
LABEL key value
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "LegacyKeyValueFormat",
				Description: "Legacy key/value format with whitespace separator should not be used",
				URL:         "https://docs.docker.com/go/dockerfile/rule/legacy-key-value-format/",
				Detail:      "\"ENV key=value\" should be used instead of legacy \"ENV key value\" format",
				Line:        3,
				Level:       1,
			},
			{
				RuleName:    "LegacyKeyValueFormat",
				Description: "Legacy key/value format with whitespace separator should not be used",
				URL:         "https://docs.docker.com/go/dockerfile/rule/legacy-key-value-format/",
				Detail:      "\"LABEL key=value\" should be used instead of legacy \"LABEL key value\" format",
				Line:        4,
				Level:       1,
			},
		},
	})

	dockerfile = []byte(`
FROM scratch
ENV testkey=value
LABEL key=value
`)
	checkLinterWarnings(t, sb, &lintTestParams{Dockerfile: dockerfile})

	// Warnings only happen in unmarshal if the lint happens in a
	// stage that's not reachable.
	dockerfile = []byte(`
FROM scratch AS a

FROM a AS b
ENV testkey value
LABEL key value

FROM a AS c
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		UnmarshalWarnings: []expectedLintWarning{
			{
				RuleName:    "LegacyKeyValueFormat",
				Description: "Legacy key/value format with whitespace separator should not be used",
				URL:         "https://docs.docker.com/go/dockerfile/rule/legacy-key-value-format/",
				Detail:      "\"ENV key=value\" should be used instead of legacy \"ENV key value\" format",
				Line:        3,
				Level:       1,
			},
			{
				RuleName:    "LegacyKeyValueFormat",
				Description: "Legacy key/value format with whitespace separator should not be used",
				URL:         "https://docs.docker.com/go/dockerfile/rule/legacy-key-value-format/",
				Detail:      "\"LABEL key=value\" should be used instead of legacy \"LABEL key value\" format",
				Line:        4,
				Level:       1,
			},
		},
	})
}

func testRedundantTargetPlatform(t *testing.T, sb integration.Sandbox) {
	dockerfile := []byte(`
FROM --platform=$TARGETPLATFORM scratch
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "RedundantTargetPlatform",
				Description: "Setting platform to predefined $TARGETPLATFORM in FROM is redundant as this is the default behavior",
				URL:         "https://docs.docker.com/go/dockerfile/rule/redundant-target-platform/",
				Detail:      "Setting platform to predefined $TARGETPLATFORM in FROM is redundant as this is the default behavior",
				Line:        2,
				Level:       1,
			},
		},
	})

	dockerfile = []byte(`
FROM --platform=${TARGETPLATFORM} scratch
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "RedundantTargetPlatform",
				Description: "Setting platform to predefined $TARGETPLATFORM in FROM is redundant as this is the default behavior",
				URL:         "https://docs.docker.com/go/dockerfile/rule/redundant-target-platform/",
				Detail:      "Setting platform to predefined ${TARGETPLATFORM} in FROM is redundant as this is the default behavior",
				Line:        2,
				Level:       1,
			},
		},
	})
}

func testInvalidDefaultArgInFrom(t *testing.T, sb integration.Sandbox) {
	baseImg := integration.UnixOrWindows("busybox", "nanoserver")
	dockerfile := []byte(fmt.Sprintf(
		`
ARG VERSION
FROM %s:$VERSION
`,
		baseImg))
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		FrontendAttrs: map[string]string{
			"build-arg:VERSION": "latest",
		},
		Warnings: []expectedLintWarning{
			{
				RuleName:    "InvalidDefaultArgInFrom",
				Description: "Default value for global ARG results in an empty or invalid base image name",
				URL:         "https://docs.docker.com/go/dockerfile/rule/invalid-default-arg-in-from/",
				Detail: fmt.Sprintf(
					"Default value for ARG %s:$VERSION results in empty or invalid base image name",
					integration.UnixOrWindows("busybox", "nanoserver"),
				),
				Line:  3,
				Level: 1,
			},
		},
	})

	dockerfile = []byte(`
ARG IMAGE
FROM $IMAGE
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		FrontendAttrs: map[string]string{
			"build-arg:IMAGE": integration.UnixOrWindows("busybox:latest", "nanoserver:latest"),
		},
		Warnings: []expectedLintWarning{
			{
				RuleName:    "InvalidDefaultArgInFrom",
				Description: "Default value for global ARG results in an empty or invalid base image name",
				URL:         "https://docs.docker.com/go/dockerfile/rule/invalid-default-arg-in-from/",
				Detail:      "Default value for ARG $IMAGE results in empty or invalid base image name",
				Line:        3,
				Level:       1,
			},
		},
	})

	dockerfile = []byte(integration.UnixOrWindows(
		`
ARG SFX="box:"
FROM busy${SFX}
`,
		`
ARG SFX="server:"
FROM nano${SFX}
`,
	))
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		FrontendAttrs: map[string]string{
			"build-arg:SFX": integration.UnixOrWindows("box:latest", "server:latest"),
		},
		Warnings: []expectedLintWarning{
			{
				RuleName:    "InvalidDefaultArgInFrom",
				Description: "Default value for global ARG results in an empty or invalid base image name",
				URL:         "https://docs.docker.com/go/dockerfile/rule/invalid-default-arg-in-from/",
				Detail: fmt.Sprintf(
					"Default value for ARG %s${SFX} results in empty or invalid base image name",
					integration.UnixOrWindows("busy", "nano"),
				),
				Line:  3,
				Level: 1,
			},
		},
	})

	dockerfile = []byte(fmt.Sprintf(
		`
ARG VERSION="latest"
FROM %s:${VERSION}
`,
		baseImg))
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		FrontendAttrs: map[string]string{
			"build-arg:VERSION": "latest",
		},
	})

	dockerfile = []byte(integration.UnixOrWindows(
		`
ARG BUSYBOX_VARIANT=""
FROM busybox:stable${BUSYBOX_VARIANT}
`,
		`
ARG BUSYBOX_VARIANT=""
FROM nanoserver:plus${BUSYBOX_VARIANT}
`,
	))
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		FrontendAttrs: map[string]string{
			"build-arg:BUSYBOX_VARIANT": integration.UnixOrWindows("-musl", "-busybox"),
		},
	})

	dockerfile = []byte(integration.UnixOrWindows(
		`
	ARG BUSYBOX_VARIANT
	FROM busybox:stable${BUSYBOX_VARIANT}
	`,
		`
	ARG BUSYBOX_VARIANT
	FROM nanoserver:plus${BUSYBOX_VARIANT}
	`,
	))
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		FrontendAttrs: map[string]string{
			"build-arg:BUSYBOX_VARIANT": integration.UnixOrWindows("-musl", "-busybox"),
		},
	})
}

func testFromPlatformFlagConstDisallowed(t *testing.T, sb integration.Sandbox) {
	dockerfile := []byte(`
FROM --platform=linux/amd64 scratch
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
		Warnings: []expectedLintWarning{
			{
				RuleName:    "FromPlatformFlagConstDisallowed",
				Description: "FROM --platform flag should not use a constant value",
				URL:         "https://docs.docker.com/go/dockerfile/rule/from-platform-flag-const-disallowed/",
				Detail:      "FROM --platform flag should not use constant value \"linux/amd64\"",
				Line:        2,
				Level:       1,
			},
		},
	})

	dockerfile = []byte(`
FROM --platform=linux/amd64 scratch AS my_amd64_stage
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
	})

	dockerfile = []byte(`
FROM --platform=linux/amd64 scratch AS linux
`)
	checkLinterWarnings(t, sb, &lintTestParams{
		Dockerfile: dockerfile,
	})
}

func checkUnmarshal(t *testing.T, sb integration.Sandbox, lintTest *lintTestParams) {
	destDir, err := os.MkdirTemp("", "buildkit")
	require.NoError(t, err)
	defer os.RemoveAll(destDir)

	var warnings []expectedLintWarning
	if lintTest.UnmarshalWarnings != nil {
		warnings = lintTest.UnmarshalWarnings
	} else {
		warnings = lintTest.Warnings
	}

	called := false
	frontend := func(ctx context.Context, c gateway.Client) (*gateway.Result, error) {
		frontendOpts := map[string]string{
			"frontend.caps": "moby.buildkit.frontend.subrequests",
			"requestid":     "frontend.lint",
		}
		maps.Copy(frontendOpts, lintTest.FrontendAttrs)

		res, err := c.Solve(ctx, gateway.SolveRequest{
			FrontendOpt: frontendOpts,
			Frontend:    "dockerfile.v0",
		})
		if err != nil {
			return nil, err
		}

		lintResults, err := unmarshalLintResults(res)
		require.NoError(t, err)

		if lintTest.UnmarshalBuildErr == "" && lintTest.UnmarshalBuildErrRegexp == nil {
			require.Nil(t, lintResults.Error)
		} else {
			require.NotNil(t, lintResults.Error)
			if lintTest.UnmarshalBuildErr != "" {
				require.Equal(t, lintTest.UnmarshalBuildErr, lintResults.Error.Message)
			} else if !lintTest.UnmarshalBuildErrRegexp.MatchString(lintResults.Error.Message) {
				t.Fatalf("error %q does not match %q", lintResults.Error.Message, lintTest.UnmarshalBuildErrRegexp.String())
			}
			require.Greater(t, lintResults.Error.Location.SourceIndex, int32(-1))
			require.Less(t, lintResults.Error.Location.SourceIndex, int32(len(lintResults.Sources)))
		}

		require.Equal(t, len(warnings), len(lintResults.Warnings))

		sort.Slice(lintResults.Warnings, func(i, j int) bool {
			// sort by line number in ascending order
			firstRange := lintResults.Warnings[i].Location.Ranges[0]
			secondRange := lintResults.Warnings[j].Location.Ranges[0]
			if firstRange.Start.Line == secondRange.Start.Line {
				return lintResults.Warnings[i].Detail < lintResults.Warnings[j].Detail
			}
			return firstRange.Start.Line < secondRange.Start.Line
		})
		// Compare expectedLintWarning with actual lint results
		for i, w := range lintResults.Warnings {
			checkLintWarning(t, w, warnings[i])
		}
		called = true
		return nil, nil
	}

	_, err = lintTest.Client.Build(sb.Context(), client.SolveOpt{
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: lintTest.TmpDir,
			dockerui.DefaultLocalNameContext:    lintTest.TmpDir,
		},
	}, "", frontend, nil)
	require.NoError(t, err)
	require.True(t, called)
}

func checkProgressStream(t *testing.T, sb integration.Sandbox, lintTest *lintTestParams) {
	t.Helper()

	status := make(chan *client.SolveStatus)
	statusDone := make(chan struct{})
	done := make(chan struct{})

	var warnings []*client.VertexWarning

	go func() {
		defer close(statusDone)
		for {
			select {
			case st, ok := <-status:
				if !ok {
					return
				}
				warnings = append(warnings, st.Warnings...)
			case <-done:
				return
			}
		}
	}()

	f := getFrontend(t, sb)

	platformStr := integration.UnixOrWindows(
		"linux/amd64,linux/arm64",
		"windows/amd64",
	)
	attrs := lintTest.FrontendAttrs
	if attrs == nil {
		attrs = map[string]string{
			"platform": platformStr,
		}
	}

	_, err := f.Solve(sb.Context(), lintTest.Client, client.SolveOpt{
		FrontendAttrs: attrs,
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameDockerfile: lintTest.TmpDir,
			dockerui.DefaultLocalNameContext:    lintTest.TmpDir,
		},
	}, status)
	if lintTest.StreamBuildErr == "" && lintTest.StreamBuildErrRegexp == nil {
		require.NoError(t, err)
	} else {
		if lintTest.StreamBuildErr != "" {
			require.EqualError(t, err, lintTest.StreamBuildErr)
		} else if !lintTest.StreamBuildErrRegexp.MatchString(err.Error()) {
			t.Fatalf("error %q does not match %q", err.Error(), lintTest.StreamBuildErrRegexp.String())
		}
	}

	select {
	case <-statusDone:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for statusDone")
	}

	require.Equal(t, len(lintTest.Warnings), len(warnings))
	sort.Slice(warnings, func(i, j int) bool {
		w1 := warnings[i]
		w2 := warnings[j]
		if len(w1.Range) == 0 {
			return true
		} else if len(w2.Range) == 0 {
			return false
		}
		if w1.Range[0].Start.Line == w2.Range[0].Start.Line {
			return string(w1.Short) < string(w2.Short)
		}
		return w1.Range[0].Start.Line < w2.Range[0].Start.Line
	})
	for i, w := range warnings {
		checkVertexWarning(t, w, lintTest.Warnings[i])
	}
}

func checkLinterWarnings(t *testing.T, sb integration.Sandbox, lintTest *lintTestParams) {
	t.Helper()
	sort.Slice(lintTest.Warnings, func(i, j int) bool {
		if lintTest.Warnings[i].Line == lintTest.Warnings[j].Line {
			return lintTest.Warnings[i].Detail < lintTest.Warnings[j].Detail
		}
		return lintTest.Warnings[i].Line < lintTest.Warnings[j].Line
	})

	if lintTest.TmpDir == nil {
		testfiles := []fstest.Applier{
			fstest.CreateFile("Dockerfile", lintTest.Dockerfile, 0600),
		}
		if lintTest.DockerIgnore != nil {
			testfiles = append(testfiles, fstest.CreateFile(".dockerignore", lintTest.DockerIgnore, 0600))
		}
		lintTest.TmpDir = integration.Tmpdir(
			t,
			testfiles...,
		)
	}

	if lintTest.Client == nil {
		var err error
		lintTest.Client, err = client.New(sb.Context(), sb.Address())
		require.NoError(t, err)
		defer lintTest.Client.Close()
	}

	t.Run("warntype=progress", func(t *testing.T) {
		checkProgressStream(t, sb, lintTest)
	})

	t.Run("warntype=unmarshal", func(t *testing.T) {
		checkUnmarshal(t, sb, lintTest)
	})
}

func checkVertexWarning(t *testing.T, warning *client.VertexWarning, expected expectedLintWarning) {
	t.Helper()
	short := linter.LintFormatShort(expected.RuleName, expected.Detail, expected.Line)
	require.Equal(t, short, string(warning.Short))
	require.Equal(t, expected.Description, string(warning.Detail[0]))
	require.Equal(t, expected.URL, warning.URL)
	require.Equal(t, expected.Level, warning.Level)
}

func checkLintWarning(t *testing.T, warning lint.Warning, expected expectedLintWarning) {
	t.Helper()
	require.Equal(t, expected.RuleName, warning.RuleName)
	require.Equal(t, expected.Description, warning.Description)
	require.Equal(t, expected.URL, warning.URL)
	require.Equal(t, expected.Detail, warning.Detail)
}

func unmarshalLintResults(res *gateway.Result) (*lint.LintResults, error) {
	dt, ok := res.Metadata["result.json"]
	if !ok {
		return nil, errors.Errorf("missing frontend.outline")
	}
	var l lint.LintResults
	if err := json.Unmarshal(dt, &l); err != nil {
		return nil, err
	}
	return &l, nil
}

type expectedLintWarning struct {
	RuleName    string
	Description string
	Line        int
	Detail      string
	URL         string
	Level       int
}

type lintTestParams struct {
	Client                  *client.Client
	TmpDir                  *integration.TmpDirWithName
	Dockerfile              []byte
	DockerIgnore            []byte
	Warnings                []expectedLintWarning
	UnmarshalWarnings       []expectedLintWarning
	StreamBuildErr          string
	StreamBuildErrRegexp    *regexp.Regexp
	UnmarshalBuildErr       string
	UnmarshalBuildErrRegexp *regexp.Regexp
	BuildErrLocation        int32
	FrontendAttrs           map[string]string
}
