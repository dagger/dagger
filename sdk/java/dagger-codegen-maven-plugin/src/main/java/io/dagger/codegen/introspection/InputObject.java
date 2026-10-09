package io.dagger.codegen.introspection;

import jakarta.json.bind.annotation.JsonbTransient;
import java.util.List;

public class InputObject {

  private String name;
  private String description;
  private String defaultValue; // isDeprecated
  private TypeRef type;
  private List<Directive> directives;

  /** The name's words from the schema JSON's __identifiers, or null. */
  @JsonbTransient private List<IdentifierWord> words;

  public List<IdentifierWord> getWords() {
    return words;
  }

  public void setWords(List<IdentifierWord> words) {
    this.words = words;
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

  public List<Directive> getDirectives() {
    return directives;
  }

  public void setDirectives(List<Directive> directives) {
    this.directives = directives;
  }

  /** Returns the @expectedType name for this argument, if present. */
  public String getExpectedType() {
    return Directive.getExpectedType(directives);
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
