package io.dagger.annotation.processor;

final class Dang {

  private Dang() {}

  /**
   * Quote a value as a Dang string literal.
   *
   * <p>Nothing parses the generated Dang before the engine loads the module, so an unescaped quote
   * or newline, which Javadoc descriptions routinely hold, fails far from its cause.
   */
  static String quote(String value) {
    StringBuilder out = new StringBuilder(value.length() + 2).append('"');
    for (int i = 0; i < value.length(); i++) {
      char c = value.charAt(i);
      switch (c) {
        case '"' -> out.append("\\\"");
        case '\\' -> out.append("\\\\");
        case '\n' -> out.append("\\n");
        case '\r' -> out.append("\\r");
        case '\t' -> out.append("\\t");
        default -> {
          if (c < 0x20) {
            out.append("\\u%04x".formatted((int) c));
          } else {
            out.append(c);
          }
        }
      }
    }
    return out.append('"').toString();
  }

  static String indent(String value, int spaces) {
    String pad = " ".repeat(spaces);
    return pad + value.replace("\n", "\n" + pad);
  }
}
