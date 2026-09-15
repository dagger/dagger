package io.dagger.codegen.introspection;

import java.util.List;

public class Directive {

  private String name;
  private List<DirectiveArg> args;

  public String getName() {
    return name;
  }

  public void setName(String name) {
    this.name = name;
  }

  public List<DirectiveArg> getArgs() {
    return args;
  }

  public void setArgs(List<DirectiveArg> args) {
    this.args = args;
  }

  /**
   * Get the value of a directive argument by name.
   *
   * @return the raw JSON string value, or null if not found
   */
  public String getArgValue(String argName) {
    if (args == null) {
      return null;
    }
    for (DirectiveArg arg : args) {
      if (argName.equals(arg.getName())) {
        return arg.getValue();
      }
    }
    return null;
  }

  /**
   * Get the @expectedType name from a list of directives, if present. Returns the unquoted type
   * name from @expectedType(name: "Foo").
   */
  public static String getExpectedType(List<Directive> directives) {
    if (directives == null) {
      return null;
    }
    for (Directive d : directives) {
      if ("expectedType".equals(d.getName())) {
        String val = d.getArgValue("name");
        if (val != null) {
          // The value comes as a JSON-encoded string, e.g. "\"Container\""
          // Strip surrounding quotes if present
          if (val.startsWith("\"") && val.endsWith("\"")) {
            val = val.substring(1, val.length() - 1);
          }
          return val;
        }
      }
    }
    return null;
  }

  @Override
  public String toString() {
    return "Directive{" + "name='" + name + '\'' + ", args=" + args + '}';
  }
}
