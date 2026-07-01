package io.dagger.client.engineconn;

class Provisioning {

  /**
   * Returns the Dagger version
   *
   * @return the Dagger version
   */
  static String getCLIVersion() {
    // Retrieve the version by refection to avoid package cyclic dependency
    try {
      return (String) Class.forName("io.dagger.client.Version").getField("VERSION").get(null);
    } catch (Exception e) {
      // Must not fail
      throw new IllegalStateException("Could not retrieve dagger version", e);
    }
  }

  /**
   * Returns the SDK version
   *
   * @return the SDK version
   */
  static String getSDKVersion() {
    // This value is replaced during build phase with the POM project.version property
    return "__PLACEHOLDER__";
  }
}
