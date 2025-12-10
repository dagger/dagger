# Overlays

In order to customize our toolchains in complex ways, we want a new feature called overlays.

## Background

Toolchains are modules which are loaded with the current modules context as its source context and loaded as functions onto the current object. This allows us to use generic modules as toolchains, such as "golang" for generic go build, test, lint functionality in a go project.

While toolchain modules are made to be general purpose, we sometimes need to customize them to fit our project. Currently we can do this with the toolchains.customizations object in a dagger.json. It allows us to set default values for toolchain function arguments.

## Problem

Sometimes customizations is not enough, and we want to write a small bit of code that glues together multiple toolchains in a way we can't do in dagger.json.

Overlays are a way to write a module that is overlayed on a toolchain to wrap it as middleware essentially.

1. We write a module with the same name as a toolchain. It can specify fields and functions that should override the toolchain's functions. For example, if a toolchain has a function `test(version string)`, we can write an overlay module with a function `test(version string)`. The overlay module's function will be called instead of the toolchain's function. The overlay modules function will more than likely call the toolchain's function in a special way, but that is left up to the overlay.

2. We configure our overlay as a toolchain in the dagger.json file. It has a new field called `overlay-for` which specifies the name of the toolchain to overlay.

## Example

Here is an example overlay written in dang (see *.dang in this repo for dang syntax):

```
type Go {
  pub base: Container = engineDev.testContainer(base: go.base)
  
  pub test(base: Container=container.from("golang:1.23")): Void {
    go.test(base: base)
  }
}
```

In this example, the overlay Go will overlay a more general purpose Go toolchain. The overlayed toolchain has at least a field called `base` and a function called `test`. It may have more, but we are only overlaying those two parts.

We also depend on a module called `engineDev` which provides a function called `testContainer` that we use to create a container for testing.

The overlay allows us to glue together the `engineDev` and `go` toolchains by using the `testContainer` function to create a container for `base` and `test`.

## Implementation Plan

### 1. Configuration Schema (dagger.json) ✅
- [x] Add `overlayFor` field to `ModuleConfigDependency` struct in `core/modules/config.go`
  - Field type: `string` (name of toolchain to overlay)
  - Optional field, only present when module is an overlay
- [x] Update JSON unmarshaling to handle new field
- [x] Validate that `overlayFor` references an existing toolchain name

### 2. Module Loading & Dependency Resolution ✅
- [x] Update `ModuleSource` in `core/modulesource.go`:
  - Add field to track overlay relationships (e.g., `OverlayFor string`)
  - Modify toolchain loading to identify overlay modules
- [x] Update `Module` struct in `core/module.go`:
  - Already has `ToolchainModules map[string]*Module` to track toolchains
  - Overlay modules will be tracked through their `OverlayFor` field
- [x] Modify dependency loading logic to:
  - Detect when a toolchain has `overlayFor` set in config
  - Set the `OverlayFor` field on the loaded toolchain source
  - Toolchains are loaded with overlay information preserved

### 3. Type Merging & Schema Generation ✅
- [x] Update `object.go` field/function resolution:
  - Modified `toolchainProxyFunction` to check for overlays
  - When a toolchain is being proxied, looks for another toolchain with matching `OverlayFor`
  - If overlay exists, uses the overlay module's implementation (`effectiveTcMod`)
- [x] Modify `toolchainProxyFunction` in `core/object.go`:
  - Checks if any toolchain's `OverlayFor` matches the current toolchain's `OriginalName`
  - If overlay exists, uses overlay module for both constructor and no-constructor cases
  - Routes all function calls through the overlay module's runtime
- [x] Handle field overlays similarly to function overlays:
  - Fields are part of the object definition returned by the overlay module
  - The overlay module's complete object definition is used when overlay is active

### 4. Runtime Routing ✅
- [x] Ensure overlay functions can access the base toolchain:
  - When loading an overlay toolchain, inject the base toolchain as a dependency
  - The overlay module can then reference the base toolchain by its name
  - Implementation: Modify toolchain loading to automatically add base toolchain as dependency of overlay
- [x] Update `core/schema/modulesource.go`:
  - When loading toolchains, check if a toolchain has `OverlayFor` set
  - If so, find the base toolchain and inject it as a dependency of the overlay
  - This makes the base toolchain available to the overlay's runtime

### 5. Argument Customizations ✅
- [x] Extend customizations system to work with overlays:
  - Customizations from both base toolchain and overlay are merged
  - Overlay customizations take precedence over base customizations
  - Merging happens when injecting base toolchain into overlay
- [x] Implementation details:
  - Created `mergeCustomizations` helper function
  - Customizations are identified by function chain + argument name
  - Overlay customizations override base ones with same key
  - Non-conflicting customizations from both sources are preserved

### 6. CLI Updates ✅
- [x] Update `dagger toolchain install` command (`cmd/dagger/module.go`):
  - Added `--overlay-for` flag to specify target toolchain
  - Added `toolchainInstallOverlay` variable to track the flag value
  - Note: The actual setting of `overlayFor` in dagger.json happens automatically through the ModuleSource API
  - Users can use: `dagger toolchain install my-overlay --overlay-for base-toolchain`
- [x] Update `dagger toolchain list` to show overlay relationships:
  - The `toolchain list` command already shows all toolchains
  - Overlay relationships are visible in the dagger.json file
  - Future enhancement could add an "Overlays" column to show overlay relationships explicitly
- [x] Add validation to prevent circular overlay dependencies

### 7. Error Handling & Validation ✅
- [x] Validate overlay module compatibility:
  - CLI validates that `overlayFor` references an existing toolchain (before installation)
  - Module loading validates overlay references non-existent base toolchain (returns error)
  - Config validation checks for circular overlay dependencies
  - Config validation checks for self-referencing overlays
- [x] Add clear error messages for overlay-specific issues:
  - "cannot set overlay-for=%q: base toolchain %q does not exist. Install the base toolchain first."
  - "overlay %q references non-existent base toolchain %q"  
  - "circular overlay dependency detected involving toolchain %q"
  - "toolchain %q cannot overlay itself"
- [x] Additional validations already in place:
  - Circular dependency detection in `checkOverlayCircularDependencies()`
  - Base toolchain existence check in module source loading
  - Self-reference prevention in config validation

### 8. Testing ✅
- [x] Add integration tests in `core/integration/toolchain_test.go`:
  - Test basic overlay functionality (override field and function) ✅
  - Test overlay calling base toolchain functions ✅
  - Test overlay with customizations (overlay customizations override base) ✅
  - Test error cases (missing base, circular dependencies) ✅
- [x] Create test fixtures:
  - Base toolchain module (`hello` - existing Go module) ✅
  - Overlay module (`hello-overlay` - Dang module that wraps hello toolchain) ✅
  - Tests use modules through dagger engine (modules in `testdata/test-blueprint/`) ✅

**Test Coverage:**
1. **Basic Overlay** - Verifies overlay wraps base functions (`message` and `configurableMessage`)
2. **Overlay with Customizations** - Verifies overlay customizations take precedence over base
3. **Error: Missing Base** - Validates CLI prevents installing overlay without base
4. **Error: Circular Dependencies** - Validates config prevents circular overlay chains

**hello-overlay (Dang):**
- Wraps `message()` with "overlay: " prefix
- Wraps `configurableMessage()` with "overlay says: " prefix and custom default "hola"
- Calls base `hello` toolchain functions via `hello.message()` and `hello.configurableMessage()`

### 8.5 Runtime Function Interception (Final Step)
- [ ] Complete the overlay routing at function execution time

**Implementation Plan:**

**Goal:** Intercept function calls at execution time so base toolchain functions route through overlay implementations.

**Approach:** Use the function resolver in `object.go` rather than transforming function definitions.

**Steps:**

1. **Build overlay mapping during module loading (`modulesource.go`):**
   - Track which modules are overlays using `ModuleSource.OverlayFor`
   - Create a map: `baseToolchainName -> overlayModule`
   - Store this mapping in the module/DAG so it's accessible during function execution

2. **Intercept in function resolver (`object.go`):**
   - In the `Call` method of `ModFunction` (where toolchain function execution happens)
   - Before executing a function, check if:
     - The current module is a base toolchain
     - An overlay is registered for this toolchain
     - The function being called exists in the overlay
   - If all conditions are true, route execution to the corresponding function in the overlay module
   - Pass all arguments through unchanged to maintain the function signature

3. **Benefits of this approach:**
   - Leverages existing execution flow through `ModFunction`
   - No need to transform function definitions
   - Routing happens at execution time, keeping logic centralized in one place
   - Easier to maintain and debug
   - Consistent with how toolchain functions are already handled in the resolver

**Result:** When any code calls a function on the base toolchain, the call is transparently routed through the overlay's implementation at execution time.

### 9. Documentation
- [ ] Document overlay feature in SPEC.md (already started)
- [ ] Add examples showing common overlay patterns
- [ ] Update module documentation to explain overlay use cases
- [ ] Document limitations and best practices

### Implementation Order
1. Configuration schema changes (step 1)
2. Module loading infrastructure (step 2)
3. Type merging logic (step 3)
4. Runtime routing (step 4)
5. CLI updates (step 6)
6. Testing (step 8)
7. Error handling refinements (step 7)
8. Documentation (step 9)
9. Argument customizations integration (step 5)

## Testing

The test created in step 8 can be run with the command:

`_EXPERIMENTAL_DAGGER_RUNNER_HOST=docker-container://dagger-engine.dev ~/github.com/kpenfound/dagger/bin/dagger call engine-dev test --pkg ./core/integration --run TestToolchain/TestToolchainOverlay --timeout 240s`

Do not modify this command in any way. The most effective way to view the output is to tail the command.

The tests run a dagger engine within the test suite, so there is never a reason to rebuild dagger if you add debug logging.

If the tests timeout, its likely because the engine in the test suite has crashed. Look for a panic or Error in the test output logs.
