package io.dagger.client.engineconn;

class Provisioning {
  static String getCLIVersion() {
    // This value is replaced during build phase with the POM daggerengine.version property
    return "__PLACEHOLDER__";
  }
}
