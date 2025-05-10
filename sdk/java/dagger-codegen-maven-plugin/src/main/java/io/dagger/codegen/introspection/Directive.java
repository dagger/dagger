package io.dagger.codegen.introspection;

import java.util.List;

public class Directive {
  private String name;
  private List<Arg> args;

  public String getName() {
    return name;
  }

  public void setName(String name) {
    this.name = name;
  }

  public List<Arg> getArgs() {
    return args;
  }

  public void setArgs(List<Arg> args) {
    this.args = args;
  }

  @Override
  public String toString() {
    return "Directive{" + "name='" + name + '\'' + ", args=" + args + '}';
  }
}
