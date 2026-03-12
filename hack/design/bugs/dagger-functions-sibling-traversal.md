# Bug Fix: `dagger functions` cannot traverse into sibling workspace modules

## Status: Implemented

## Problem

In a workspace with a blueprint module, `dagger functions python-sdk` fails:

```
Error: no function "python-sdk" in type "DaggerDev"
```

While `dagger call python-sdk --help` works correctly.

The root cause: `dagger functions` starts traversal from `MainObject` (the blueprint's
object, e.g. `DaggerDev`). Sibling workspace modules like `python-sdk` are not functions
on that object — they're Query-root functions from other modules. The traversal loop in
`funcListCmd` only searches the current function provider, so it can't find them.

The **display** already works: `siblingModuleEntrypoints()` is called at the top level to
include siblings in the listing. Only the **traversal** is broken.

`dagger call` avoids this because it builds a cobra command tree that explicitly includes
sibling commands via `addSiblingModuleCommands()`.

## Fix

### Change: Expand traversal to check sibling entrypoints (call.go)

In the traversal loop (`call.go:82-104`), when the first step fails to find a function on
`MainObject`, fall back to checking sibling module entrypoints.

Only the first step needs this: siblings are only visible at the top level, matching the
display behavior.

```go
for i, field := range functionPath {
    nextFunc, err := GetSupportedFunction(mod, o, field)
    if err != nil {
        // On the first step, the field may refer to a sibling workspace
        // module rather than a function on the default module's object.
        if i == 0 {
            if sf := findSiblingEntrypoint(mod, field); sf != nil {
                nextFunc = sf
                err = nil
            }
        }
        if err != nil {
            return err
        }
    }
    nextType := nextFunc.ReturnType
    if nextType.AsFunctionProvider() != nil {
        o = mod.GetFunctionProvider(nextType.Name())
        continue
    }
    return fmt.Errorf(...)
}
```

### Helper: findSiblingEntrypoint (call.go or module_inspect.go)

```go
func findSiblingEntrypoint(mod *moduleDef, name string) *modFunction {
    for _, fn := range mod.siblingModuleEntrypoints() {
        if fn.Name == name || fn.CmdName() == name {
            mod.LoadFunctionTypeDefs(fn)
            return fn
        }
    }
    return nil
}
```

### Why this works

Once we resolve the sibling function, its return type (e.g. `PythonSdk`) is already in
`mod.Objects` because `loadTypeDefs` populates all loaded modules' types. The existing
`mod.GetFunctionProvider(nextType.Name())` call finds it, and further traversal steps
(e.g. `dagger functions python-sdk python-310`) work naturally.
