package org.chelonix.dagger.codegen;

import com.ongres.process.FluentProcess;
import org.apache.maven.plugin.AbstractMojo;
import org.apache.maven.plugin.MojoExecutionException;
import org.apache.maven.plugin.MojoFailureException;
import org.apache.maven.plugins.annotations.LifecyclePhase;
import org.apache.maven.plugins.annotations.Mojo;
import org.apache.maven.plugins.annotations.Parameter;
import org.apache.maven.plugins.annotations.ResolutionScope;
import org.apache.maven.project.MavenProject;
import org.chelonix.dagger.codegen.introspection.CodegenVisitor;
import org.chelonix.dagger.codegen.introspection.Schema;
import org.chelonix.dagger.codegen.introspection.SchemaVisitor;
import org.chelonix.dagger.codegen.introspection.Type;

import java.io.*;
import java.net.URL;
import java.nio.charset.Charset;
import java.nio.file.Path;
import java.time.Duration;
import java.time.temporal.ChronoUnit;

@Mojo(name = "codegen",
        defaultPhase = LifecyclePhase.GENERATE_SOURCES,
        requiresDependencyResolution = ResolutionScope.COMPILE,
        threadSafe = true)
public class DaggerCodegenMojo extends AbstractMojo {

    /**
     * specify output file encoding; defaults to source encoding
     */
    @Parameter(property = "project.build.sourceEncoding")
    protected String outputEncoding;

    /**
     * The current Maven project.
     */
    @Parameter(property = "project", required = true, readonly = true)
    protected MavenProject project;

    @Parameter(property = "dagger.bin", defaultValue = "dagger")
    protected String bin;

    @Parameter(property = "dagger.version", required = true)
    protected String version;

    @Parameter(property = "dagger.introspectionQuertyURL")
    protected String introspectionQuertyURL;

    @Parameter(property = "dagger.introspectionQuertyPath")
    protected String introspectionQuertyPath;

    @Parameter(property = "dagger.regenerateSchema", defaultValue = "false")
    protected boolean online;


    /**
     * Specify output directory where the Java files are generated.
     */
    @Parameter(defaultValue = "${project.build.directory}/generated-sources/dagger")
    private File outputDirectory;

    @Override
    public void execute() throws MojoExecutionException, MojoFailureException {
        outputEncoding = validateEncoding(outputEncoding);

        // Ensure that the output directory path is all in tact so that
        // ANTLR can just write into it.
        //
        File outputDir = getOutputDirectory();

        if (!outputDir.exists()) {
            outputDir.mkdirs();
        }

        Path dest = outputDir.toPath();
        try (InputStream in = daggerSchema()) {
            Schema schema = Schema.initialize(in);
            SchemaVisitor codegen = new CodegenVisitor(schema, dest, Charset.forName(outputEncoding));
            schema.visit(new SchemaVisitor() {
                @Override
                public void visitScalar(Type type) {
                    getLog().info(String.format("Generating scala %s", type.getName()));
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

    private InputStream daggerSchema() throws IOException, InterruptedException, MojoFailureException {
        if (!online) {
            InputStream in = getClass().getClassLoader().getResourceAsStream(String.format("schemas/schema-v%s.json", version));
            if (in == null) {
                throw new MojoFailureException(String.format("GraphQL schema not available for version %s", version));
            }
            return in;
        }
        if (introspectionQuertyPath != null) {
            return new FileInputStream(introspectionQuertyPath);
        }
        InputStream in;
        if (introspectionQuertyPath != null) {
            return new FileInputStream(introspectionQuertyPath);
        } else if (introspectionQuertyURL == null) {
            in = new URL(String.format("https://raw.githubusercontent.com/dagger/dagger/v%s/codegen/introspection/introspection.graphql", version)).openStream();
        } else if (introspectionQuertyURL != null) {
            in = new URL(introspectionQuertyURL).openStream();
        } else {
            throw new MojoFailureException("Could not locate, download or generate GraphQL schema");
        }
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        FluentProcess.start(bin, "query")
                .withTimeout(Duration.of(60, ChronoUnit.SECONDS))
                .inputStream(in)
                .writeToOutputStream(out);
        return new ByteArrayInputStream(out.toByteArray());
    }

    public File getOutputDirectory() {
        return outputDirectory;
    }

    /**
     * Validates the given encoding.
     *
     * @return  the validated encoding. If {@code null} was provided, returns the platform default encoding.
     */
    private String validateEncoding(String encoding) {
        return (encoding == null) ? Charset.defaultCharset().name() : Charset.forName(encoding.trim()).name();
    }
}
