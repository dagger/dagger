package io.dagger.codegen;

import com.ongres.process.FluentProcess;
import io.dagger.codegen.introspection.*;
import java.io.*;
import java.nio.charset.Charset;
import java.nio.file.Path;
import java.time.Duration;
import java.time.temporal.ChronoUnit;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import org.apache.maven.plugin.AbstractMojo;
import org.apache.maven.plugin.MojoExecutionException;
import org.apache.maven.plugin.MojoFailureException;
import org.apache.maven.plugins.annotations.LifecyclePhase;
import org.apache.maven.plugins.annotations.Mojo;
import org.apache.maven.plugins.annotations.Parameter;
import org.apache.maven.plugins.annotations.ResolutionScope;
import org.apache.maven.project.MavenProject;

@Mojo(
    name = "codegen",
    defaultPhase = LifecyclePhase.GENERATE_SOURCES,
    requiresDependencyResolution = ResolutionScope.COMPILE,
    threadSafe = true)
public class DaggerCodegenMojo extends AbstractMojo {

  /** specify output file encoding; defaults to source encoding */
  @Parameter(property = "project.build.sourceEncoding")
  protected String outputEncoding;

  /** The current Maven project. */
  @Parameter(property = "project", required = true, readonly = true)
  protected MavenProject project;

  @Parameter(property = "dagger.bin")
  protected String bin;

  @Parameter(property = "dagger.version", required = true)
  protected String version;

  /** Specify output directory where the Java files are generated. */
  @Parameter(defaultValue = "${project.build.directory}/generated-sources/dagger")
  private File outputDirectory;

  private static boolean isStandardVersionFormat(String input) {
    String pattern = "v\\d+\\.\\d+\\.\\d+"; // Le motif regex pour le format attendu
    Pattern regex = Pattern.compile(pattern);
    Matcher matcher = regex.matcher(input);
    return matcher.matches();
  }

  @Override
  public void execute() throws MojoExecutionException, MojoFailureException {
    outputEncoding = validateEncoding(outputEncoding);

    // Ensure that the output directory path is all intact so that
    // we can just write into it.
    //
    File outputDir = getOutputDirectory();

    if (!outputDir.exists()) {
      outputDir.mkdirs();
    }

    setCLIBinary();

    Path dest = outputDir.toPath();
    try (InputStream in = daggerSchema()) {
      Schema schema = Schema.initialize(in, version);
      SchemaVisitor codegen = new CodegenVisitor(schema, dest, Charset.forName(outputEncoding));
      schema.visit(
          new SchemaVisitor() {
            @Override
            public void visitScalar(Type type) {
              getLog().info(String.format("Generating scalar %s", type.getName()));
              codegen.visitScalar(type);
            }

            @Override
            public void visitObject(Type type) {
              getLog().info(String.format("Generating object %s", type.getName()));
              codegen.visitObject(type);
            }

            @Override
            public void visitInput(Type type) {
              getLog().info(String.format("Generating input %s", type.getName()));
              codegen.visitInput(type);
            }

            @Override
            public void visitEnum(Type type) {
              getLog().info(String.format("Generating enum %s", type.getName()));
              codegen.visitEnum(type);
            }

            @Override
            public void visitVersion(String version) {
              getLog().info(String.format("Generating interface Version"));
              codegen.visitVersion(version);
            }
          });
    } catch (IOException ioe) {
      throw new MojoFailureException(ioe);
    } catch (InterruptedException ie) {
      throw new MojoFailureException(ie);
    }

    if (project != null) {
      // Tell Maven that there are some new source files underneath the output directory.
      project.addCompileSourceRoot(getOutputDirectory().getPath());
    }
  }

  private void setCLIBinary() {
    if (this.bin == null) {
      this.bin = System.getenv("_EXPERIMENTAL_DAGGER_CLI_BIN");
      if (this.bin == null) {
        this.bin = "dagger";
      }
    }
  }

  private InputStream queryForSchema(InputStream introspectionQuery) {
    getLog().info("Querying local dagger CLI for schema");
    ByteArrayOutputStream out = new ByteArrayOutputStream();
    FluentProcess.start(bin, "query")
        .withTimeout(Duration.of(60, ChronoUnit.SECONDS))
        .inputStream(introspectionQuery)
        .writeToOutputStream(out);
    return new ByteArrayInputStream(out.toByteArray());
  }

  private String getCLIVersion() {
    ByteArrayOutputStream out = new ByteArrayOutputStream();
    String output =
        FluentProcess.start(bin, "version").withTimeout(Duration.of(60, ChronoUnit.SECONDS)).get();
    String version = output.split("\\s")[1];
    return isStandardVersionFormat(version) ? version.substring(1) : version;
  }

  private InputStream daggerSchema()
      throws IOException, InterruptedException, MojoFailureException {
    if ("local".equalsIgnoreCase(version)) {
      version = getCLIVersion();
      return queryForSchema(
          getClass().getClassLoader().getResourceAsStream("introspection/introspection.graphql"));
    } else {
      InputStream in =
          getClass()
              .getClassLoader()
              .getResourceAsStream(String.format("schemas/schema-v%s.json", version));
      if (in == null) {
        throw new MojoFailureException(
            String.format("GraphQL schema for version %s not found", version));
      }
      return in;
    }
  }

  public File getOutputDirectory() {
    return outputDirectory;
  }

  /**
   * Validates the given encoding.
   *
   * @return the validated encoding. If {@code null} was provided, returns the platform default
   *     encoding.
   */
  private String validateEncoding(String encoding) {
    return (encoding == null)
        ? Charset.defaultCharset().name()
        : Charset.forName(encoding.trim()).name();
  }
}
