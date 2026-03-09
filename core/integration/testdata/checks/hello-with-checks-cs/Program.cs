// This is the entry point for the module, called from the dagger engine.
// Editing this file is highly discouraged and may stop your module from functioning entirely.
//
// Debugging:
//   To enable debug logging, add this before RunAsync:
//     Dagger.ModuleRuntime.Entrypoint.ConfigureLogging(true);
//
//   Then run your module with:
//     dagger call --progress=plain <your-function>
//
//   Debug logs will appear in:
//     - Terminal stderr (with --progress=plain)
//     - /tmp/dagger-csharp-debug.log (inside the container)

return await Dagger.ModuleRuntime.Entrypoint.RunAsync(args);
