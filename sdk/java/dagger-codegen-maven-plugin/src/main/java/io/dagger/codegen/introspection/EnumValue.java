package io.dagger.codegen.introspection;

import jakarta.json.bind.annotation.JsonbProperty;
import jakarta.json.bind.annotation.JsonbTransient;

public class EnumValue {

  private String name;
  private String description;

  @JsonbProperty("isDeprecated")
  private boolean deprecated; // isDeprecated

  private String DeprecationReason;

  /** The Java constant name the engine formatted for this value, or null. */
  @JsonbTransient private String formattedName;

  public String getFormattedName() {
    return formattedName;
  }

  public void setFormattedName(String formattedName) {
    this.formattedName = formattedName;
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

  public boolean isDeprecated() {
    return deprecated;
  }

  public void setDeprecated(boolean deprecated) {
    this.deprecated = deprecated;
  }

  public String getDeprecationReason() {
    return DeprecationReason;
  }

  public void setDeprecationReason(String deprecationReason) {
    DeprecationReason = deprecationReason;
  }

  @Override
  public String toString() {
    return "EnumValue{"
        + "name='"
        + name
        + '\''
        +
        // ", Description='" + Description + '\'' +
        ", deprecated="
        + deprecated
        +
        // ", DeprecationReason='" + DeprecationReason + '\'' +
        '}';
  }
}
