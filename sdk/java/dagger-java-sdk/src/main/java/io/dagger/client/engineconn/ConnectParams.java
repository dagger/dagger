package io.dagger.client.engineconn;

class ConnectParams {
  private int port;

  private String sessionToken;

  public ConnectParams(int port, String sessionToken) {
    this.port = port;
    this.sessionToken = sessionToken;
  }

  public int getPort() {
    return port;
  }

  @Override
  public String toString() {
    return "ConnectParams{" + "port=" + port + ", sessionToken='" + sessionToken + '\'' + '}';
  }

  public void setPort(int port) {
    this.port = port;
  }

  public String getSessionToken() {
    return sessionToken;
  }

  public void setSessionToken(String sessionToken) {
    this.sessionToken = sessionToken;
  }
}
