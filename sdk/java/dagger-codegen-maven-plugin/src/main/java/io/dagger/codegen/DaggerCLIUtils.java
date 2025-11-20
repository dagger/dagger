package io.dagger.codegen;

import com.ongres.process.FluentProcess;
import com.ongres.process.FluentProcessBuilder;
import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.time.Duration;
import java.time.temporal.ChronoUnit;
import java.util.Map;
import java.util.HashMap;
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

  public static InputStream query(InputStream query, String binPath) {
    ByteArrayOutputStream out = new ByteArrayOutputStream();

    // The introspection query response is >20k lines, save our
    // telemetry from the burden of sending/storing it by unsetting
    // these env vars
    Map<String, String> envVars = new HashMap<>(System.getenv());
    envVars.remove("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT");
    envVars.remove("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT");
    envVars.remove("OTEL_EXPORTER_OTLP_ENDPOINT");
    envVars.remove("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT");

    FluentProcessBuilder builder = new FluentProcessBuilder(binPath);
    builder
        .args("query", "-s", "-M")
        .noStderr()
        .clearEnvironment()
        .environment(envVars)
        .start()
        .withTimeout(Duration.of(60, ChronoUnit.SECONDS))
        .inputStream(query)
        .writeToOutputStream(out);
    return new ByteArrayInputStream(out.toByteArray());
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
