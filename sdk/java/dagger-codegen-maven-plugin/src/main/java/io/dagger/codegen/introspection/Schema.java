package io.dagger.codegen.introspection;

import static java.util.Comparator.comparing;

import jakarta.json.bind.JsonbBuilder;
import jakarta.json.bind.annotation.JsonbProperty;
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
    SchemaContainer container = builder.build().fromJson(str, SchemaContainer.class);
    Schema schema = container.getSchema();
    schema.types.forEach(
        type -> {
          if (type.getFields() != null) {
            type.getFields().forEach(field -> field.setParentObject(type));
          }
        });
    schema.version = version;
    // Class names follow the schema until formatted names are set.
    Helpers.setJavaTypeNames(null);
    return schema;
    // Json.createReader(schema.getJsonObject("__schema").)
  }

  private String version;

  private QueryType queryType;

  private List<Type> types;

  /** Whether enum constants follow Java's convention, from formatted names. */
  private boolean formattedEnumValues;

  /**
   * Attaches the Java names the engine formatted (see {@link FormattedNames}), keyed by format and
   * then by schema name: member names to fields, arguments and input fields, class names to types,
   * and constant names to enum values. A name without an entry, a format without names, or a null
   * map (the schema has no Query.formatIdentifiers) keeps the schema name.
   */
  public void setFormattedNames(Map<String, Map<String, String>> formats) {
    if (formats == null || types == null) {
      return;
    }
    Map<String, String> members = formats.getOrDefault(FormattedNames.MEMBERS, Map.of());
    Map<String, String> typeNames = formats.get(FormattedNames.TYPES);
    Map<String, String> enumValues = formats.get(FormattedNames.ENUM_VALUES);
    for (Type type : types) {
      if (type.getFields() != null) {
        for (Field field : type.getFields()) {
          field.setFormattedName(members.get(field.getName()));
          if (field.getArgs() != null) {
            field.getArgs().forEach(arg -> arg.setFormattedName(members.get(arg.getName())));
          }
        }
      }
      if (type.getInputFields() != null) {
        type.getInputFields()
            .forEach(inputField -> inputField.setFormattedName(members.get(inputField.getName())));
      }
      if (enumValues != null && type.getEnumValues() != null) {
        for (EnumValue value : type.getEnumValues()) {
          value.setFormattedName(enumValues.get(value.getName()));
        }
      }
    }
    Helpers.setJavaTypeNames(typeNames);
    formattedEnumValues = enumValues != null;
  }

  /**
   * Whether enum constants follow Java's convention (SCREAMING_SNAKE_CASE, carrying their schema
   * values), rather than the schema's values.
   */
  public boolean formatsEnumValues() {
    return formattedEnumValues;
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
