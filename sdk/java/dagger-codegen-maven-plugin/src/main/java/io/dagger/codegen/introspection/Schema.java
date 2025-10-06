package io.dagger.codegen.introspection;

import static java.util.Comparator.comparing;

import jakarta.json.bind.JsonbBuilder;
import jakarta.json.bind.annotation.JsonbProperty;
import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.util.List;

public class Schema {

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
            type.getFields().stream().forEach(field -> field.setParentObject(type));
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
            t ->
                !List.of("Boolean", "String", "Float", "Int", "DateTime", "ID")
                    .contains(t.getName()))
        .forEach(visitor::visitScalar);

    filteredTypes.stream()
        .filter(t -> t.getKind() == TypeKind.INPUT_OBJECT)
        .forEach(visitor::visitInput);

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
