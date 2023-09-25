package io.dagger.codegen;

import java.io.*;
import java.nio.file.Path;
import org.apache.maven.plugin.AbstractMojo;
import org.apache.maven.plugin.MojoExecutionException;
import org.apache.maven.plugin.MojoFailureException;
import org.apache.maven.plugins.annotations.Mojo;
import org.apache.maven.plugins.annotations.Parameter;
import org.apache.maven.plugins.annotations.ResolutionScope;
import org.apache.maven.project.MavenProject;

/** Generate the API schama by querying the dagger CLI */
@Mojo(
    name = "generateSchema",
    requiresDependencyResolution = ResolutionScope.COMPILE,
    threadSafe = true)
public class DaggerSchemaGeneratorMojo extends AbstractMojo {
  /** The current Maven project. */
  @Parameter(property = "project", required = true, readonly = true)
  protected MavenProject project;

  @Parameter(property = "dagger.bin")
  protected String bin;

  /** Specify output directory where the Java files are generated. */
  @Parameter(defaultValue = "${project.build.directory}/generated-schema")
  private File outputDirectory;

  @Override
  public void execute() throws MojoExecutionException, MojoFailureException {
    File outputDir = getOutputDirectory();

    if (!outputDir.exists()) {
      outputDir.mkdirs();
    }

    this.bin = DaggerCLIUtils.getBinary(this.bin);
    getLog().info(String.format("Set Dagger CLI to %s", this.bin));

    Path dest = outputDir.toPath();
    try (InputStream query =
            getClass().getClassLoader().getResourceAsStream("introspection/introspection.graphql");
        OutputStream outputFile = new FileOutputStream(new File(outputDir, "schema.json"))) {
      getLog().info("Querying Dagger CLI for schema");
      InputStream schema = DaggerCLIUtils.query(query, this.bin);
      outputFile.write(schema.readAllBytes());
    } catch (Exception ioe) {
      throw new MojoExecutionException(ioe);
    }
  }

  public File getOutputDirectory() {
    return outputDirectory;
  }
}
