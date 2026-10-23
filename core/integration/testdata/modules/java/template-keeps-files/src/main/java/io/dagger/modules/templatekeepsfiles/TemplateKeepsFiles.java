package io.dagger.modules.templatekeepsfiles;

import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;

// No pom.xml: the runtime adds the template's, but must keep this class
// rather than replace it with the template's class at the same path.
@Object
public class TemplateKeepsFiles {
  @Function
  public String ownFunction() {
    return "own function";
  }
}
