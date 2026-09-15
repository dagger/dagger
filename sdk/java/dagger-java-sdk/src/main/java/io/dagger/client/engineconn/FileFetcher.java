package io.dagger.client.engineconn;

import java.io.IOException;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URL;

@FunctionalInterface
interface FileFetcher {

  Response fetch(String url) throws IOException;

  static Response fetchURL(String url) throws IOException {
    HttpURLConnection connection = (HttpURLConnection) new URL(url).openConnection();
    return fetchConnection(connection);
  }

  static Response fetchConnection(HttpURLConnection connection) throws IOException {
    InputStream body = null;
    try {
      connection.setInstanceFollowRedirects(true);
      int statusCode = connection.getResponseCode();
      String statusMessage = connection.getResponseMessage();
      body =
          statusCode == HttpURLConnection.HTTP_OK
              ? connection.getInputStream()
              : connection.getErrorStream();
      if (body == null) {
        body = InputStream.nullInputStream();
      }
      return new Response(statusCode, statusMessage, body, connection);
    } catch (IOException | RuntimeException error) {
      if (body != null) {
        try {
          body.close();
        } catch (IOException closeError) {
          error.addSuppressed(closeError);
        }
      }
      try {
        connection.disconnect();
      } catch (RuntimeException disconnectError) {
        error.addSuppressed(disconnectError);
      }
      throw error;
    }
  }

  final class Response implements AutoCloseable {
    private final int statusCode;
    private final String statusMessage;
    private final InputStream body;
    private final HttpURLConnection connection;

    Response(int statusCode, String statusMessage, InputStream body) {
      this(statusCode, statusMessage, body, null);
    }

    private Response(
        int statusCode, String statusMessage, InputStream body, HttpURLConnection connection) {
      this.statusCode = statusCode;
      this.statusMessage = statusMessage;
      this.body = body;
      this.connection = connection;
    }

    int statusCode() {
      return statusCode;
    }

    String status() {
      return statusMessage == null || statusMessage.isBlank()
          ? Integer.toString(statusCode)
          : statusCode + " " + statusMessage;
    }

    InputStream body() {
      return body;
    }

    @Override
    public void close() throws IOException {
      try {
        body.close();
      } finally {
        if (connection != null) {
          connection.disconnect();
        }
      }
    }
  }
}
