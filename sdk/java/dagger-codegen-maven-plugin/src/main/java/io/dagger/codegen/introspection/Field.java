package io.dagger.codegen.introspection;

import jakarta.json.bind.annotation.JsonbProperty;
import jakarta.json.bind.annotation.JsonbTransient;
import java.util.List;
import java.util.NoSuchElementException;

public class Field {
  private String name;
  private String description;

  @JsonbProperty("type")
  private TypeRef typeRef;

  private List<InputObject> args;

  @JsonbProperty("isDeprecated")
  private boolean deprecated; // isDeprecated

  private String DeprecationReason;

  @JsonbTransient private List<InputObject> optionalArgs;

  private Type parentObject;

  private List<Directive> directives;

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
    this.description = "<p>" + description.replace("\n", "<br/>") + "</p>";
  }

  public TypeRef getTypeRef() {
    return typeRef;
  }

  public void setTypeRef(TypeRef typeRef) {
    this.typeRef = typeRef;
  }

  public List<InputObject> getArgs() {
    return args;
  }

  public void setArgs(List<InputObject> args) {
    this.args = args;
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

  public Type getParentObject() {
    return parentObject;
  }

  public void setParentObject(Type parentObject) {
    this.parentObject = parentObject;
  }

  boolean hasArgs() {
    return getArgs().size() > 0;
  }

  boolean hasOptionalArgs() {
    return getArgs().stream().filter(arg -> arg.getType().isOptional()).count() > 0;
  }

  public List<Directive> getDirectives() {
    return directives;
  }

  public void setDirectives(List<Directive> directives) {
    this.directives = directives;
  }

  public boolean isExperimental() {
    return getDirectives().stream()
        .anyMatch(directive -> directive.getName().equals("experimental"));
  }

  public String experimentalReason() {
    try {
      return getDirectives().stream()
          .filter(directive -> directive.getName().equals("experimental"))
          .findFirst()
          .orElseThrow()
          .getArgs()
          .stream()
          .filter(arg -> arg.getName().equals("reason"))
          .findFirst()
          .orElseThrow()
          .getValue()
          .replaceFirst("^\"", "")
          .replaceAll("\"$", "");
    } catch (NoSuchElementException e) {
      return "";
    }
  }

  /** Returns the list of optional argument of this field */
  List<InputObject> getOptionalArgs() {
    if (optionalArgs == null) {
      optionalArgs = args.stream().filter(arg -> arg.getType().isOptional()).toList();
    }
    return optionalArgs;
  }

  List<InputObject> getRequiredArgs() {
    return args.stream().filter(arg -> !arg.getType().isOptional()).toList();
  }

  @Override
  public String toString() {
    return "Field{"
        + "name='"
        + name
        + '\''
        + ", typeRef="
        + typeRef
        + ", args="
        + args
        + ", deprecated="
        + deprecated
        + ", directives="
        + directives
        + ", optionalArgs="
        + optionalArgs
        + ", parentObject="
        + (parentObject != null ? parentObject.getName() : "null")
        + '}';
  }
}
