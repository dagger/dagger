package io.dagger.client.engineconn;

import java.io.*;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.security.DigestInputStream;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.HashMap;
import java.util.HexFormat;
import java.util.Map;
import org.apache.commons.compress.archivers.ArchiveEntry;
import org.apache.commons.compress.archivers.ArchiveInputStream;
import org.apache.commons.compress.archivers.tar.TarArchiveInputStream;
import org.apache.commons.compress.archivers.zip.ZipArchiveInputStream;
import org.apache.commons.compress.compressors.gzip.GzipCompressorInputStream;
import org.freedesktop.BaseDirectory;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

class CLIDownloader {

  static final Logger LOG = LoggerFactory.getLogger(CLIDownloader.class);

  private static final File CACHE_DIR =
      Paths.get(BaseDirectory.get(BaseDirectory.XDG_CACHE_HOME), "dagger").toFile();

  private static final String DAGGER_CLI_BIN_PREFIX = "dagger-";

  private final FileFetcher fetcher;

  public CLIDownloader(FileFetcher fetcher) {
    this.fetcher = fetcher;
  }

  public CLIDownloader() {
    this(url -> new URL(url).openStream());
  }

  public String downloadCLI(String version) throws IOException {
    CACHE_DIR.mkdirs();
    CACHE_DIR.setExecutable(true, true);
    CACHE_DIR.setReadable(true, true);
    CACHE_DIR.setWritable(true, true);

    String binName = DAGGER_CLI_BIN_PREFIX + version;
    if (isWindows()) {
      binName += ".exe";
    }
    Path binPath = Paths.get(CACHE_DIR.getPath(), binName);

    if (!binPath.toFile().exists()) {
      downloadCLI(version, binPath);
    }

    return binPath.toString();
  }

  public String downloadCLI() throws IOException {
    return downloadCLI(Provisioning.getCLIVersion());
  }

  private void downloadCLI(String version, Path binPath) throws IOException {
    String binName = binPath.getFileName().toString();
    Path tmpBin = Files.createTempFile(CACHE_DIR.toPath(), "tmp-" + binName, null);
    try {
      String archiveName = getDefaultCLIArchiveName(version);
      String expectedChecksum = expectedChecksum(version, archiveName);
      if (expectedChecksum == null) {
        throw new IOException("Could not find checksum for " + archiveName);
      }
      String actualChecksum = extractCLI(archiveName, version, tmpBin);
      if (!actualChecksum.equals(expectedChecksum)) {
        throw new IOException("Checksum validation failed");
      }
      tmpBin.toFile().setExecutable(true);
      Files.move(tmpBin, binPath);
    } finally {
      Files.deleteIfExists(tmpBin);
    }
  }

  private String expectedChecksum(String version, String archiveName) throws IOException {
    Map<String, String> checksums = fetchChecksumMap(version);
    return checksums.get(archiveName);
  }

  private String getDefaultCLIArchiveName(String version) {
    String ext = isWindows() ? "zip" : "tar.gz";
    return String.format("dagger_v%s_%s_%s.%s", version, getOS(), getArch(), ext);
  }

  private Map<String, String> fetchChecksumMap(String version) throws IOException {
    Map<String, String> checksums = new HashMap<>();
    String checksumMapURL =
        String.format("https://dl.dagger.io/dagger/releases/%s/checksums.txt", version);
    try (BufferedInputStream in = new BufferedInputStream(fetcher.fetch(checksumMapURL))) {
      ByteArrayOutputStream out = new ByteArrayOutputStream();
      byte[] dataBuffer = new byte[1024];
      int bytesRead;
      while ((bytesRead = in.read(dataBuffer, 0, 1024)) != -1) {
        out.write(dataBuffer, 0, bytesRead);
      }
      BufferedReader reader =
          new BufferedReader(new StringReader(out.toString(StandardCharsets.UTF_8)));
      String line;
      while ((line = reader.readLine()) != null) {
        String[] arr = line.split("\\s+");
        checksums.put(arr[1], arr[0]);
      }
      return checksums;
    }
  }

  private String extractCLI(String archiveName, String version, Path dest) throws IOException {
    String cliArchiveURL =
        String.format("https://dl.dagger.io/dagger/releases/%s/%s", version, archiveName);
    LOG.info("Downloading Dagger CLI from " + cliArchiveURL);
    MessageDigest sha256;
    try {
      sha256 = MessageDigest.getInstance("SHA-256");
    } catch (NoSuchAlgorithmException nsae) {
      throw new IOException("Could not instantiate SHA-256 digester", nsae);
    }
    LOG.info("Extracting archive...");
    try (InputStream in =
        new BufferedInputStream(new DigestInputStream(fetcher.fetch(cliArchiveURL), sha256))) {
      if (isWindows()) {
        extractZip(in, dest);
      } else {
        extractTarGZ(in, dest);
      }
      byte[] checksum = sha256.digest();
      return HexFormat.of().formatHex(checksum);
    }
  }

  private void extractZip(InputStream in, Path dest) throws IOException {
    try (ZipArchiveInputStream zipIn = new ZipArchiveInputStream(in)) {
      extractCLIBin(zipIn, "dagger.exe", dest);
    }
  }

  private static void extractCLIBin(ArchiveInputStream in, String binName, Path dest)
      throws IOException {
    boolean found = false;
    ArchiveEntry entry;
    while ((entry = in.getNextEntry()) != null) {
      if (entry.isDirectory() || !binName.equals(entry.getName())) {
        continue;
      }
      int count;
      byte[] data = new byte[4096];
      FileOutputStream fos = new FileOutputStream(dest.toFile());
      try (BufferedOutputStream out = new BufferedOutputStream(fos, 4096)) {
        while ((count = in.read(data, 0, 4096)) != -1) {
          out.write(data, 0, count);
        }
      }
      found = true;
      break;
    }
    if (!found) {
      throw new IOException("Could not find dagger binary in CLI archive");
    }
  }

  private void extractTarGZ(InputStream in, Path dest) throws IOException {
    boolean found = false;
    try (GzipCompressorInputStream gzipIn = new GzipCompressorInputStream(in);
        TarArchiveInputStream tarIn = new TarArchiveInputStream(gzipIn)) {
      extractCLIBin(tarIn, "dagger", dest);
    }
  }

  private static boolean isWindows() {
    return System.getProperty("os.name").toLowerCase().contains("win");
  }

  private static String getOS() {
    String os = System.getProperty("os.name").toLowerCase();
    if (os.contains("win")) {
      return "windows";
    } else if (os.contains("linux")) {
      return "linux";
    } else if (os.contains("darwin") || os.contains("mac")) {
      return "darwin";
    } else {
      return "unknown";
    }
  }

  private static String getArch() {
    String arch = System.getProperty("os.arch").toLowerCase();
    if (arch.contains("x86_64") || arch.contains("amd64")) {
      return "amd64";
    } else if (arch.contains("x86")) {
      return "x86";
    } else if (arch.contains("arm")) {
      return "armv7";
    } else if (arch.contains("aarch64")) {
      return "arm64";
    } else {
      return "unknown";
    }
  }
}
