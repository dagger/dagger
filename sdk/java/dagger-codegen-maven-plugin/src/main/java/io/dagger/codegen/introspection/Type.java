package io.dagger.codegen.introspection;

import static java.util.Comparator.comparing;

import java.util.List;

public class Type {
  private TypeKind kind;
  private String name;
  private String description;
  private List<Field> fields;
  private List<InputObject> inputFields;
  private List<EnumValue> enumValues;

  public TypeKind getKind() {
    return kind;
  }

  public void setKind(TypeKind kind) {
    this.kind = kind;
  }

  public String getName() {
    return name;
  }

  public void setName(String name) {
    this.name = name;
  }

  public String getDescription() {
    return description;
  }

  public void setDescription(String description) {
    this.description = description.replace("\n", "<br/>");
  }

  public List<EnumValue> getEnumValues() {
    return enumValues;
  }

  public void setEnumValues(List<EnumValue> enumValues) {
    this.enumValues = enumValues;
  }

  public List<InputObject> getInputFields() {
    return inputFields;
  }

  public void setInputFields(List<InputObject> inputFields) {
    this.inputFields =
        inputFields == null
            ? null
            : inputFields.stream().sorted(comparing(InputObject::getName)).toList();
    // this.inputFields = inputFields;
  }

  public List<Field> getFields() {
    return fields;
  }

  public void setFields(List<Field> fields) {
    this.fields =
        fields == null ? null : fields.stream().sorted(comparing(Field::getName)).toList();
    // this.fields = fields;
  }

  private static boolean filterIDField(Field f) {
    return "id".equals(f.getName())
        && f.getTypeRef().isScalar()
        && f.getTypeRef().getTypeName().equals(f.getParentObject().getName() + "ID");
  }

  boolean providesId() {
    return fields.stream().filter(Type::filterIDField).count() > 0;
  }

  Field getIdField() {
    return fields.stream().filter(Type::filterIDField).findFirst().get();
  }

  @Override
  public String toString() {
    return "Type{"
        + "kind="
        + kind
        + ", name='"
        + name
        + '\''
        + ", description='"
        + description
        + '\''
        + ", fields="
        + fields
        + ", inputFields="
        + inputFields
        + ", enumValues="
        + enumValues
        + '}';
  }
}
