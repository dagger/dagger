package io.dagger.codegen;

import com.ongres.process.FluentProcess;
import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.time.Duration;
import java.time.temporal.ChronoUnit;
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
    // HACK: for some reason writing to stderr just causes it to hang since
    // we're not reading from stderr, so we redirect it to /dev/null.
    FluentProcess.start("sh", "-c", "$0 query 2>/dev/null", binPath)
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
