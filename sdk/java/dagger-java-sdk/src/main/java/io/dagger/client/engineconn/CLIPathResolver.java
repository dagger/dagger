package io.dagger.client.engineconn;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;

class CLIPathResolver {

  String findDaggerCLI() throws IOException {
    String path = System.getenv("PATH");
    if (path == null) {
      throw new IOException("PATH is not set");
    }

    for (String candidate : daggerCLIPathCandidates(isWindows(), path, System.getenv("PATHEXT"))) {
      Path candidatePath = Path.of(candidate);
      if (!Files.isRegularFile(candidatePath) || !Files.isExecutable(candidatePath)) {
        continue;
      }
      if (!candidatePath.isAbsolute()) {
        throw new IOException(
            "cannot run dagger executable found relative to the current directory: " + candidate);
      }
      return candidatePath.toString();
    }

    throw new IOException("dagger executable was not found");
  }

  List<String> daggerCLIPathCandidates(boolean windows, String path, String pathExt) {
    String delimiter = windows ? ";" : ":";
    List<String> extensions = windows ? windowsExecutableExtensions(pathExt) : List.of("");
    List<String> candidates = new ArrayList<>();

    for (String directory : path.split(delimiter, -1)) {
      if (windows) {
        // Match PowerShell and Go's exec.LookPath behavior.
        if (directory.isEmpty()) {
          continue;
        }
        if (directory.length() >= 2 && directory.startsWith("\"") && directory.endsWith("\"")) {
          directory = directory.substring(1, directory.length() - 1);
        }
      } else if (directory.isEmpty()) {
        // Match Unix shell semantics.
        directory = ".";
      }

      for (String extension : extensions) {
        candidates.add(join(directory, "dagger" + extension, windows));
      }
    }
    return candidates;
  }

  private List<String> windowsExecutableExtensions(String pathExt) {
    String value = pathExt == null || pathExt.isEmpty() ? ".COM;.EXE;.BAT;.CMD" : pathExt;
    List<String> extensions = new ArrayList<>();
    for (String extension : value.toLowerCase(Locale.ROOT).split(";")) {
      if (extension.isEmpty()) {
        continue;
      }
      extensions.add(extension.startsWith(".") ? extension : "." + extension);
    }
    return extensions;
  }

  private String join(String directory, String fileName, boolean windows) {
    String separator = windows ? "\\" : "/";
    while (directory.endsWith("/") || directory.endsWith("\\")) {
      directory = directory.substring(0, directory.length() - 1);
    }
    return directory.isEmpty() ? separator + fileName : directory + separator + fileName;
  }

  private boolean isWindows() {
    return System.getProperty("os.name").toLowerCase(Locale.ROOT).contains("win");
  }
}
