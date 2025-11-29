// This is the entry point for the module, called from the dagger engine.
// Editing this file is highly discouraged and may stop your module from functioning entirely.

Dagger.ModuleRuntime.Entrypoint.ConfigureLogging(true);
return await Dagger.ModuleRuntime.Entrypoint.RunAsync(args);
