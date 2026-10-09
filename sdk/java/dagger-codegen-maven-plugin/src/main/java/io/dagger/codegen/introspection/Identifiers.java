package io.dagger.codegen.introspection;

import java.util.List;
import java.util.Locale;

/**
 * Formats schema names from the words the engine writes to the schema JSON's {@code __identifiers}
 * map, so codegen doesn't have to guess word boundaries. See "Schema JSON words" in
 * hack/designs/identifier-casing.md; the engine's engine/naming/testdata/vectors.json pins the
 * output.
 */
public final class Identifiers {

  private Identifiers() {}

  /** The casings a name can be formatted in. */
  public enum Casing {
    PASCAL,
    CAMEL,
    SNAKE,
    SCREAMING_SNAKE,
    KEBAB,
    FLAT
  }

  /**
   * Formats words in a casing.
   *
   * @param capitalized write acronyms and terms in their CAPITALIZED form ({@code Http}, {@code
   *     Ipv6}) rather than UPPERCASE ({@code HTTP}, {@code IPv6}) where a word starts with a
   *     capital. Only PASCAL and CAMEL are affected.
   */
  public static String format(List<IdentifierWord> words, Casing casing, boolean capitalized) {
    StringBuilder out = new StringBuilder();
    for (int i = 0; i < words.size(); i++) {
      IdentifierWord w = words.get(i);
      switch (casing) {
        case PASCAL -> out.append(capitalizedForm(w, capitalized));
        case CAMEL -> out.append(i == 0 ? lower(w) : capitalizedForm(w, capitalized));
        case SNAKE -> out.append(i == 0 ? "" : "_").append(lower(w));
        case SCREAMING_SNAKE ->
            out.append(i == 0 ? "" : "_")
                .append((w.getText() + w.getSuffix()).toUpperCase(Locale.ROOT));
        case KEBAB -> out.append(i == 0 ? "" : "-").append(lower(w));
        case FLAT -> out.append(lower(w));
      }
    }
    return out.toString();
  }

  private static String lower(IdentifierWord w) {
    return (w.getText() + w.getSuffix()).toLowerCase(Locale.ROOT);
  }

  private static String capitalizedForm(IdentifierWord w, boolean capitalized) {
    if (capitalized || "WORD".equals(w.getKind())) {
      return w.getCapitalized() + w.getSuffix();
    }
    String text = w.getText();
    if (text.isEmpty()) {
      return w.getSuffix();
    }
    return text.substring(0, 1).toUpperCase(Locale.ROOT) + text.substring(1) + w.getSuffix();
  }
}
