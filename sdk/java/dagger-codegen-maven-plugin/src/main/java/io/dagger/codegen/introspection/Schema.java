package io.dagger.codegen.introspection;

import static java.util.Comparator.comparing;

import jakarta.json.bind.JsonbBuilder;
import jakarta.json.bind.annotation.JsonbProperty;
import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.util.List;
import org.apache.maven.artifact.versioning.ComparableVersion;

public class Schema {

  private static final ComparableVersion NULLABLE_OBJECTS_VERSION =
      new ComparableVersion("1.0.0-beta.10");

  /**
   * The first schema version whose SDKs load every ID-returning field carrying an expectedType
   * directive as the object it names. Older views only convert fields returning their parent's own
   * ID (the sync-like shape).
   */
  private static final ComparableVersion ID_HANDLES_VERSION =
      new ComparableVersion("1.0.0-beta.12");

  public static class SchemaContainer {

    @JsonbProperty("__schema")
    private Schema schema;

    protected SchemaContainer() {}

    public Schema getSchema() {
      return schema;
    }

    public void setSchema(Schema schema) {
      this.schema = schema;
    }
  }

  public static Schema initialize(InputStream in, String version) throws IOException {
    JsonbBuilder builder = JsonbBuilder.newBuilder();
    String str = new String(in.readAllBytes(), StandardCharsets.UTF_8);
    // System.out.println(str);
    Schema schema = builder.build().fromJson(str, SchemaContainer.class).getSchema();
    schema.types.forEach(
        type -> {
          if (type.getFields() != null) {
            type.getFields().forEach(field -> field.setParentObject(type));
          }
        });
    schema.version = version;
    return schema;
    // Json.createReader(schema.getJsonObject("__schema").)
  }

  private String version;

  private QueryType queryType;

  private List<Type> types;

  public QueryType getQueryType() {
    return queryType;
  }

  public void setQueryType(QueryType queryType) {
    this.queryType = queryType;
  }

  public List<Type> getTypes() {
    return types;
  }

  public void setTypes(List<Type> types) {
    this.types = types.stream().sorted(comparing(Type::getName)).toList();
  }

  public String getVersion() {
    return version;
  }

  /** Returns true if the named type is a GraphQL INTERFACE. */
  public boolean isInterface(String typeName) {
    return types != null
        && types.stream().anyMatch(t -> typeName.equals(t.getName()) && t.isInterface());
  }

  public boolean supportsNullableObjects() {
    return schemaVersionAtLeast(NULLABLE_OBJECTS_VERSION);
  }

  /** Whether every expectedType-annotated ID return is loaded as its object. */
  public boolean supportsIdHandles() {
    return schemaVersionAtLeast(ID_HANDLES_VERSION);
  }

  /**
   * Compares the schema version against a feature cutover. Unknown or non-semver versions
   * (development builds) get every feature.
   */
  private boolean schemaVersionAtLeast(ComparableVersion cutover) {
    if (version == null || version.isBlank()) {
      return true;
    }

    if (!version.matches("^v?\\d+\\.\\d+\\.\\d+.*$")) {
      return true;
    }

    String normalized = version.startsWith("v") ? version.substring(1) : version;
    return new ComparableVersion(normalized).compareTo(cutover) >= 0;
  }

  public Type query() {
    return types.stream()
        .filter(type -> queryType.getName().equals(type.getName()))
        .findFirst()
        .get();
  }

  public void visit(SchemaVisitor visitor) {
    List<Type> filteredTypes = types.stream().filter(t -> !t.getName().startsWith("_")).toList();

    filteredTypes.stream()
        .filter(t -> t.getKind() == TypeKind.SCALAR)
        .filter(
            t -> !List.of("Boolean", "String", "Float", "Int", "DateTime").contains(t.getName()))
        .forEach(visitor::visitScalar);

    filteredTypes.stream()
        .filter(t -> t.getKind() == TypeKind.INPUT_OBJECT)
        .forEach(visitor::visitInput);

    filteredTypes.stream()
        .filter(t -> t.getKind() == TypeKind.INTERFACE)
        .forEach(visitor::visitInterface);

    filteredTypes.stream()
        .filter(t -> t.getKind() == TypeKind.OBJECT)
        .forEach(visitor::visitObject);

    filteredTypes.stream().filter(t -> t.getKind() == TypeKind.ENUM).forEach(visitor::visitEnum);

    visitor.visitVersion(version);

    visitor.visitIDAbles(
        filteredTypes.stream()
            .filter(t -> t.getKind() == TypeKind.OBJECT && t.providesId())
            .toList());
  }

  @Override
  public String toString() {
    return "Schema{" + "queryType=" + queryType + ", types=" + types + '}';
  }
}
