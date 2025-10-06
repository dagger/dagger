package io.dagger.codegen.introspection;

public class InputObject {

  private String name;
  private String description;
  private String defaultValue; // isDeprecated
  private TypeRef type;

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

  public String getDefaultValue() {
    return defaultValue;
  }

  public void setDefaultValue(String defaultValue) {
    this.defaultValue = defaultValue;
  }

  public TypeRef getType() {
    return type;
  }

  public void setType(TypeRef type) {
    this.type = type;
  }

  @Override
  public String toString() {
    return "InputValue{"
        + "name='"
        + name
        + '\''
        +
        // ", Description='" + Description + '\'' +
        ", defaultValue='"
        + defaultValue
        + '\''
        + ", type="
        + type
        + '}';
  }
}
