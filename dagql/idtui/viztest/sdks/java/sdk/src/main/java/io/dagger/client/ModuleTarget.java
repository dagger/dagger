package io.dagger.client;

/**
 * The module a generated client was generated against.
 *
 * @param address the module's workspace-absolute path, or its git reference
 * @param pin the commit a git reference resolved to at generation, or null for a path
 */
public record ModuleTarget(String address, String pin) {

  public static ModuleTarget local(String address) {
    return new ModuleTarget(address, null);
  }

  public static ModuleTarget git(String address, String pin) {
    return new ModuleTarget(address, pin);
  }
}
