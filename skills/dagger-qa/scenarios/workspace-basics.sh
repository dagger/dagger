# QA Scenario: Workspace Basics
# Tests: init workspace, create module with checks, list checks, run checks
#
# Run via:
#   PLAYGROUND_TIMEOUT=900 bash skills/engine-dev-testing/with-playground.sh \
#     "$(cat .claude/skills/dagger-qa/scenarios/workspace-basics.sh)"

RESULTS=""
step() {
  STEP_NAME="$1"; shift
  echo "── $STEP_NAME ──"
  OUTPUT=$("$@" 2>&1)
  EC=$?
  echo "$OUTPUT"
  if [ $EC -eq 0 ]; then
    RESULTS="${RESULTS}| ${STEP_NAME} | PASS | exit=$EC |\n"
  else
    RESULTS="${RESULTS}| ${STEP_NAME} | FAIL | exit=$EC |\n"
  fi
  echo "EXIT: $EC"
  echo ""
  return $EC
}

mkdir -p /tmp/demo && cd /tmp/demo
git init -q .
git config user.email "qa@test" && git config user.name "QA"

# Step 1: Init workspace
step "1-workspace-init" dagger workspace init

# Step 1b: Verify workspace info
step "1b-workspace-info" dagger workspace info

# Step 2: Create module (auto-installs into workspace config)
step "2-module-init" dagger module init --sdk=go ci

# Step 2b: Write module source with +check annotations
mkdir -p .dagger/modules/ci
cat > .dagger/modules/ci/main.go <<'GOEOF'
package main

import (
	"context"
	"dagger/ci/internal/dagger"
)

type Ci struct{}

// Verify the project builds
// +check
func (m *Ci) Build(ctx context.Context) error {
	_, err := dag.Container().
		From("alpine:3").
		WithExec([]string{"echo", "build ok"}).
		Sync(ctx)
	return err
}

// Run linter
// +check
func (m *Ci) Lint(ctx context.Context) error {
	_, err := dag.Container().
		From("alpine:3").
		WithExec([]string{"echo", "lint ok"}).
		Sync(ctx)
	return err
}

// Run unit tests
// +check
func (m *Ci) Test() *dagger.Container {
	return dag.Container().
		From("alpine:3").
		WithExec([]string{"echo", "tests passed"})
}
GOEOF
RESULTS="${RESULTS}| 2b-write-module-src | PASS | manual |\n"
echo "Module source written"
echo ""

# Step 3: Verify config has module installed
step "3-workspace-config" dagger workspace config

# Step 4: List checks
step "4-list-checks" dagger check -l

# Step 5: Run all checks
step "5-run-checks" dagger --progress=report check

# ── Report ──
echo ""
echo "=============================="
echo "QA REPORT: Workspace Basics"
echo "=============================="
echo "| Step | Status | Detail |"
echo "|------|--------|--------|"
printf "$RESULTS"
echo "=============================="
