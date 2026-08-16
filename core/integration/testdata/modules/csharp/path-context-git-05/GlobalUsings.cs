// Global using directives for Dagger modules (mirrors the SDK template):
// bring the static Dag client into scope and alias types that clash with System.IO.
global using static Dagger.Client;
global using Directory = Dagger.Directory;
global using File = Dagger.File;
