package io.dagger.client.engineconn;

import java.io.IOException;

class CLIReleaseUnavailableException extends IOException {

  CLIReleaseUnavailableException(String message) {
    super(message);
  }
}
