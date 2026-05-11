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
  private List<TypeRef> interfaces;
  private List<TypeRef> possibleTypes;
  private List<Directive> directives;

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
  }

  public List<Field> getFields() {
    return fields;
  }

  public void setFields(List<Field> fields) {
    this.fields =
        fields == null ? null : fields.stream().sorted(comparing(Field::getName)).toList();
  }

  public List<TypeRef> getInterfaces() {
    return interfaces;
  }

  public void setInterfaces(List<TypeRef> interfaces) {
    this.interfaces = interfaces;
  }

  public List<TypeRef> getPossibleTypes() {
    return possibleTypes;
  }

  public void setPossibleTypes(List<TypeRef> possibleTypes) {
    this.possibleTypes = possibleTypes;
  }

  public List<Directive> getDirectives() {
    return directives;
  }

  public void setDirectives(List<Directive> directives) {
    this.directives = directives;
  }

  /**
   * Checks if this type has an "id" field. With unified IDs, the id field returns the unified ID
   * scalar. Falls back to legacy FooID check.
   */
  private static boolean filterIDField(Field f) {
    if (!"id".equals(f.getName())) {
      return false;
    }
    if (!f.getTypeRef().isScalar()) {
      return false;
    }
    // Unified ID: scalar name is "ID" — the id field itself may not carry @expectedType,
    // but the parent type name is implicit
    if ("ID".equals(f.getTypeRef().getTypeName())) {
      return true;
    }
    // Legacy: FooID scalar
    return f.getTypeRef().getTypeName().equals(f.getParentObject().getName() + "ID");
  }

  boolean providesId() {
    return fields != null && fields.stream().anyMatch(Type::filterIDField);
  }

  Field getIdField() {
    return fields.stream().filter(Type::filterIDField).findFirst().get();
  }

  /** Returns true if this type is a GraphQL INTERFACE. */
  public boolean isInterface() {
    return kind == TypeKind.INTERFACE;
  }

  /** Returns the list of interface names this object implements. */
  public List<String> getImplementedInterfaceNames() {
    if (interfaces == null) {
      return List.of();
    }
    return interfaces.stream().map(TypeRef::getTypeName).toList();
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
