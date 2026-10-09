package io.dagger.codegen.introspection;

import static java.util.Comparator.comparing;

import jakarta.json.bind.JsonbBuilder;
import jakarta.json.bind.annotation.JsonbProperty;
import jakarta.json.bind.annotation.JsonbTransient;
import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.util.List;
import java.util.Map;
import org.apache.maven.artifact.versioning.ComparableVersion;

public class Schema {

  private static final ComparableVersion NULLABLE_OBJECTS_VERSION =
      new ComparableVersion("1.0.0-beta.10");

  public static class SchemaContainer {

    @JsonbProperty("__schema")
    private Schema schema;

    /**
     * The words of every name in the schema, keyed by name. Only engine views v1.0.0-0 and above
     * write it; without it, codegen keeps converting names itself.
     */
    @JsonbProperty("__identifiers")
    private Map<String, List<IdentifierWord>> identifiers;

    protected SchemaContainer() {}

    public Schema getSchema() {
      return schema;
    }

    public void setSchema(Schema schema) {
      this.schema = schema;
    }

    public Map<String, List<IdentifierWord>> getIdentifiers() {
      return identifiers;
    }

    public void setIdentifiers(Map<String, List<IdentifierWord>> identifiers) {
      this.identifiers = identifiers;
    }
  }

  public static Schema initialize(InputStream in, String version) throws IOException {
    JsonbBuilder builder = JsonbBuilder.newBuilder();
    String str = new String(in.readAllBytes(), StandardCharsets.UTF_8);
    // System.out.println(str);
    SchemaContainer container = builder.build().fromJson(str, SchemaContainer.class);
    Schema schema = container.getSchema();
    schema.types.forEach(
        type -> {
          if (type.getFields() != null) {
            type.getFields().forEach(field -> field.setParentObject(type));
          }
        });
    schema.setIdentifiers(container.getIdentifiers());
    schema.version = version;
    return schema;
    // Json.createReader(schema.getJsonObject("__schema").)
  }

  private String version;

  private QueryType queryType;

  private List<Type> types;

  @JsonbTransient private Map<String, List<IdentifierWord>> identifiers;

  /**
   * Attaches the words of field, argument and input field names, so they can be formatted as Java
   * identifiers. A name without an entry keeps today's conversion.
   */
  void setIdentifiers(Map<String, List<IdentifierWord>> identifiers) {
    this.identifiers = identifiers;
    if (identifiers == null || types == null) {
      return;
    }
    for (Type type : types) {
      if (type.getFields() != null) {
        for (Field field : type.getFields()) {
          field.setWords(identifiers.get(field.getName()));
          if (field.getArgs() != null) {
            field.getArgs().forEach(arg -> arg.setWords(identifiers.get(arg.getName())));
          }
        }
      }
      if (type.getInputFields() != null) {
        type.getInputFields()
            .forEach(inputField -> inputField.setWords(identifiers.get(inputField.getName())));
      }
    }
  }

  /** Returns true if the schema JSON has identifier words (engine views v1.0.0-0 and above). */
  public boolean hasIdentifiers() {
    return identifiers != null;
  }

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
    if (version == null || version.isBlank()) {
      return true;
    }

    if (!version.matches("^v?\\d+\\.\\d+\\.\\d+.*$")) {
      return true;
    }

    String normalized = version.startsWith("v") ? version.substring(1) : version;
    return new ComparableVersion(normalized).compareTo(NULLABLE_OBJECTS_VERSION) >= 0;
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
