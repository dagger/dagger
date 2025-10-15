My test `dagger --progress dots call test specific --pkg ./core/integration --run TestBlueprint/TestMultipleBlueprints --timeout 120s` is failing.
Fix the code so the test passes.
You should always tail the logs of the test to see the results. Dont bother with grep or head.
Never modify this test command.

## Context

This test is testing the case where we have multiple blueprint modules.
Blueprint modules are implemented in core/schema/modulesourc.go starting on line 2508.

What should happen is each blueprint module is added as a field to the main module.
In the implementation, they are first added to a shadowModule and then that is added to the main module.
Fields are automatically accessible as functions.

If I have a blueprint called Foo with a function called Bar, I can call Foo.Bar by running dagger call foo bar, since Foo has been added as a field to the main module.

Look at `git diff origin` to understand the changes made so far.

## Problem

Whats happening is the Foo module is being applied properly to the schema, so we can see the correct schema when looking at the module's API, however when we go to run Foo from the main module, we cannot find it and get null.

Note that the schema and runtime are loaded differently. We are already loading the schema correctly and the client sees the correct schema. What we are missing is the runtime is not there for the particular object when it is called.

This is likely because dependencies are run in separate dagql servers from the main module's server.
Have a look at core/moddeps.go, specifically the function `lazilyLoadSchema`. In that function, dependency modules are served in their own dagql server. For this to work, we may need forward the requests to the correct server because the module must have its correct runtime. Maybe we can create constructors on the main module that can construct the object from the correct runtime and route the client to the correct place. We know if a module is a blueprint module at this point in time by checking module.IsBlueprint.
Figure out why and fix it. The test should pass unmodified.
Do not try to go build or use go directly, only use the test for validation.

Note in the test case we run, defined in core/integration/blueprint_test.go, it calls `dagger call hello message`. So it is following the format `dagger call <blueprint name> <function>`.

## Understanding Errors

"<nil> does not match hello from blueprint" means that the runtime for the blueprint is missing and resolved to null.
"error 502 unexpected EOF" means the engine crashed. The crash logs are in the test output, but its very verbose, so look for a stack trace or panic.
'type "Foo" is already defined by module "daggercore"' means that Install was called twice somehow for the Foo object. Its called automatically somewhere for dependencies.
"failed to get mod type for type def" means the schema was not built correctly for an object.

## testing

The test command is `dagger --progress dots call test specific --pkg ./core/integration --run TestBlueprint/TestMultipleBlueprints --timeout 120s`
You can tail the last 50 or so lines of the output to see the specific test failure. Do not modify this command or run any other test command.
