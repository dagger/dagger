package io.dagger.codegen;

import com.ongres.process.FluentProcess;
import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.io.UncheckedIOException;
import java.net.URISyntaxException;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.CodeSource;
import java.time.Duration;
import java.time.temporal.ChronoUnit;
import java.util.ArrayList;
import java.util.Enumeration;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.TimeUnit;
import java.util.jar.JarFile;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

public class DaggerCLIUtils {

  private DaggerCLIUtils() {}

  public static String getBinary(String defaultCLIPath) {
    String bin = defaultCLIPath;
    if (defaultCLIPath == null) {
      bin = System.getenv("_EXPERIMENTAL_DAGGER_CLI_BIN");
      if (bin == null) {
        bin = "dagger";
      }
    }
    return bin;
  }

  public static InputStream query(InputStream query, String binPath)
      throws IOException, InterruptedException {
    if (query == null) {
      throw new IOException("missing introspection query resource");
    }

    ByteArrayOutputStream out = new ByteArrayOutputStream();
    ByteArrayOutputStream err = new ByteArrayOutputStream();

    // The introspection query response is >20k lines, save our
    // telemetry from the burden of sending/storing it by unsetting
    // these env vars
    Map<String, String> envVars = new HashMap<>(System.getenv());
    envVars.remove("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT");
    envVars.remove("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT");
    envVars.remove("OTEL_EXPORTER_OTLP_ENDPOINT");
    envVars.remove("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT");

    ProcessBuilder builder = new ProcessBuilder(binPath, "query", "-s", "-M");
    builder.environment().clear();
    builder.environment().putAll(envVars);

    Process process = builder.start();
    CompletableFuture<Void> stdin =
        copyAsync(
            () -> {
              try (InputStream queryInput = query;
                  OutputStream processStdin = process.getOutputStream()) {
                queryInput.transferTo(processStdin);
              }
            });
    CompletableFuture<Void> stdout =
        copyAsync(
            () -> {
              try (InputStream processStdout = process.getInputStream()) {
                processStdout.transferTo(out);
              }
            });
    CompletableFuture<Void> stderr =
        copyAsync(
            () -> {
              try (InputStream processStderr = process.getErrorStream()) {
                processStderr.transferTo(err);
              }
            });

    boolean exited = process.waitFor(60, TimeUnit.SECONDS);
    if (!exited) {
      process.destroyForcibly();
      throw new IOException("dagger query timed out after 60 seconds");
    }

    waitForCopies(stdout, stderr);
    if (process.exitValue() != 0) {
      throw new IOException(
          "dagger query failed with exit code "
              + process.exitValue()
              + "\n"
              + formatProcessOutput("stderr", err)
              + "\n"
              + formatProcessOutput("stdout", out));
    }
    waitForCopies(stdin);

    return new ByteArrayInputStream(out.toByteArray());
  }

  public static InputStream introspectionQuery(Class<?> anchorClass) throws IOException {
    String resource = "introspection/introspection.graphql";
    InputStream stream = getResource(anchorClass.getClassLoader(), resource);
    if (stream != null) {
      return stream;
    }

    throw new IOException(
        "missing introspection query resource "
            + resource
            + "\n"
            + resourceDiagnostics(anchorClass, resource));
  }

  private static InputStream getResource(ClassLoader classLoader, String resource) {
    if (classLoader == null) {
      return null;
    }
    return classLoader.getResourceAsStream(resource);
  }

  private static String resourceDiagnostics(Class<?> anchorClass, String resource) {
    StringBuilder diagnostics = new StringBuilder();
    diagnostics.append("anchorClass=").append(anchorClass.getName()).append('\n');
    diagnostics.append("anchorCodeSource=").append(codeSource(anchorClass)).append('\n');
    diagnostics.append("utilsCodeSource=").append(codeSource(DaggerCLIUtils.class)).append('\n');
    diagnostics
        .append("anchorCodeSourceResourceEntries=")
        .append(codeSourceResourceEntries(anchorClass, resource))
        .append('\n');
    diagnostics
        .append("utilsCodeSourceResourceEntries=")
        .append(codeSourceResourceEntries(DaggerCLIUtils.class, resource))
        .append('\n');
    diagnostics
        .append("anchorTargetClassesResource=")
        .append(targetClassesResource(anchorClass, resource))
        .append('\n');
    diagnostics.append(
        describeClassLoader("anchorClassLoader", anchorClass.getClassLoader(), resource));
    diagnostics.append(
        describeClassLoader("utilsClassLoader", DaggerCLIUtils.class.getClassLoader(), resource));
    diagnostics.append(
        describeClassLoader(
            "threadContextClassLoader", Thread.currentThread().getContextClassLoader(), resource));
    return diagnostics.toString();
  }

  private static String describeClassLoader(String name, ClassLoader classLoader, String resource) {
    StringBuilder description = new StringBuilder();
    description.append(name).append('=').append(classLoader).append('\n');
    if (classLoader == null) {
      description.append(name).append(".resources=<bootstrap>\n");
      return description.toString();
    }
    try {
      List<String> resources = new ArrayList<>();
      Enumeration<URL> urls = classLoader.getResources(resource);
      while (urls.hasMoreElements()) {
        resources.add(urls.nextElement().toString());
      }
      description.append(name).append(".resources=").append(resources).append('\n');
    } catch (IOException e) {
      description.append(name).append(".resources=<failed: ").append(e).append(">\n");
    }
    return description.toString();
  }

  private static String codeSource(Class<?> clazz) {
    try {
      URL location = codeSourceLocation(clazz);
      if (location == null) {
        return "<none>";
      }
      return location.toString();
    } catch (SecurityException e) {
      return "<failed: " + e + ">";
    }
  }

  private static URL codeSourceLocation(Class<?> clazz) {
    CodeSource source = clazz.getProtectionDomain().getCodeSource();
    if (source == null) {
      return null;
    }
    return source.getLocation();
  }

  private static List<String> codeSourceResourceEntries(Class<?> clazz, String resource) {
    try {
      URL location = codeSourceLocation(clazz);
      if (location == null) {
        return List.of("<none>");
      }
      Path path = Path.of(location.toURI());
      if (Files.isDirectory(path)) {
        Path resourcePath = path.resolve(resource);
        if (!Files.exists(resourcePath)) {
          return List.of(resourcePath + " missing");
        }
        return List.of(resourcePath + " size=" + Files.size(resourcePath));
      }
      if (!Files.isRegularFile(path)) {
        return List.of(path + " is not a regular file");
      }
      try (JarFile jar = new JarFile(path.toFile())) {
        return jar.stream()
            .filter(entry -> entry.getName().startsWith("introspection/"))
            .map(entry -> entry.getName() + " size=" + entry.getSize())
            .toList();
      }
    } catch (IOException | IllegalArgumentException | SecurityException | URISyntaxException e) {
      return List.of("<failed: " + e + ">");
    }
  }

  private static String targetClassesResource(Class<?> clazz, String resource) {
    try {
      URL location = codeSourceLocation(clazz);
      if (location == null) {
        return "<none>";
      }
      Path codeSource = Path.of(location.toURI());
      Path target = codeSource.getParent();
      if (target == null) {
        return codeSource + " has no parent";
      }
      Path resourcePath = target.resolve("classes").resolve(resource);
      if (!Files.exists(resourcePath)) {
        return resourcePath + " missing";
      }
      return resourcePath + " size=" + Files.size(resourcePath);
    } catch (IOException | IllegalArgumentException | SecurityException | URISyntaxException e) {
      return "<failed: " + e + ">";
    }
  }

  private static CompletableFuture<Void> copyAsync(IoRunnable runnable) {
    return CompletableFuture.runAsync(
        () -> {
          try {
            runnable.run();
          } catch (IOException e) {
            throw new UncheckedIOException(e);
          }
        });
  }

  @SafeVarargs
  private static void waitForCopies(CompletableFuture<Void>... copies)
      throws IOException, InterruptedException {
    try {
      CompletableFuture.allOf(copies).get();
    } catch (ExecutionException e) {
      Throwable cause = e.getCause();
      if (cause instanceof UncheckedIOException) {
        throw ((UncheckedIOException) cause).getCause();
      }
      if (cause instanceof RuntimeException) {
        throw (RuntimeException) cause;
      }
      throw new IOException("failed to collect dagger query output", cause);
    }
  }

  private static String formatProcessOutput(String name, ByteArrayOutputStream stream) {
    String output = stream.toString(StandardCharsets.UTF_8).trim();
    if (output.isEmpty()) {
      return name + ": <empty>";
    }
    int maxLength = 8192;
    if (output.length() > maxLength) {
      output = output.substring(0, maxLength) + "\n... output truncated ...";
    }
    return name + ":\n" + output;
  }

  @FunctionalInterface
  private interface IoRunnable {
    void run() throws IOException;
  }

  private static boolean isStandardVersionFormat(String input) {
    String pattern = "v\\d+\\.\\d+\\.\\d+"; // Le motif regex pour le format attendu
    Pattern regex = Pattern.compile(pattern);
    Matcher matcher = regex.matcher(input);
    return matcher.matches();
  }

  /**
   * Gets the value given returned by "dagger version". If the version is of the form vX.Y.Z then
   * the "v" prefix is stripped
   *
   * @param binPath path the dagger bin
   * @return the version
   */
  public static String getVersion(String binPath) {
    ByteArrayOutputStream out = new ByteArrayOutputStream();
    String output =
        FluentProcess.start(binPath, "version")
            .withTimeout(Duration.of(60, ChronoUnit.SECONDS))
            .get();
    String version = output.split("\\s")[1];
    return isStandardVersionFormat(version) ? version.substring(1) : version;
  }
}
