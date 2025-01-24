package io.dagger.codegen;

import com.ongres.process.FluentProcess;
import io.dagger.codegen.introspection.CodegenVisitor;
import io.dagger.codegen.introspection.Schema;
import io.dagger.codegen.introspection.SchemaVisitor;
import io.dagger.codegen.introspection.Type;
import java.io.*;
import java.nio.charset.Charset;
import java.nio.file.Path;
import java.time.Duration;
import java.time.temporal.ChronoUnit;
import java.util.List;
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

  @Parameter(property = "dagger.introspectionJson")
  protected String introspectionJson;

  /** Specify output directory where the Java files are generated. */
  @Parameter(defaultValue = "${project.build.directory}/generated-sources/dagger")
  private File outputDirectory;

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

    Path dest = outputDir.toPath();
    try (InputStream in = getInstrospectionJson()) {
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

            @Override
            public void visitIDAbles(List<Type> types) {
              getLog().info(String.format("Generate helpers for IDAbles"));
              codegen.visitIDAbles(types);
            }
          });
    } catch (IOException | InterruptedException e) {
      throw new MojoFailureException(e);
    }

    if (project != null) {
      // Tell Maven that there are some new source files underneath the output directory.
      project.addCompileSourceRoot(getOutputDirectory().getPath());
    }
  }

  private InputStream getInstrospectionJson()
      throws IOException, MojoFailureException, InterruptedException {
    if (this.introspectionJson != null && !this.introspectionJson.isEmpty()) {
      File f = new File(this.introspectionJson);
      if (f.exists()) {
        return new FileInputStream(f);
      }
    }
    this.bin = DaggerCLIUtils.getBinary(this.bin);
    return daggerSchema();
  }

  private InputStream queryForSchema(InputStream introspectionQuery) {
    ByteArrayOutputStream out = new ByteArrayOutputStream();
    FluentProcess.start(bin, "query")
        .withTimeout(Duration.of(60, ChronoUnit.SECONDS))
        .inputStream(introspectionQuery)
        .writeToOutputStream(out);
    return new ByteArrayInputStream(out.toByteArray());
  }

  private InputStream daggerSchema()
      throws IOException, InterruptedException, MojoFailureException {
    String actualVersion = DaggerCLIUtils.getVersion(this.bin);
    if ("local".equalsIgnoreCase(version) || "devel".equalsIgnoreCase(version)) {
      getLog()
          .info(String.format("Querying local dagger CLI for schema (version=%s)", actualVersion));
      this.version = actualVersion;
      return DaggerCLIUtils.query(
          getClass().getClassLoader().getResourceAsStream("introspection/introspection.graphql"),
          this.bin);
    } else {
      if (!actualVersion.equals(version)) {
        throw new MojoFailureException(
            String.format(
                "Actual dagger CLI version (%s) mismatches expected version (%s)",
                actualVersion, version));
      }
      InputStream in =
          getClass()
              .getClassLoader()
              .getResourceAsStream(String.format("schemas/schema-%s.json", version));
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
