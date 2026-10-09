package io.dagger.codegen.introspection;

/**
 * One word of a schema name, as the engine writes it to the schema JSON's {@code __identifiers} map
 * (engine views v1.0.0-0 and above).
 */
public class IdentifierWord {

  private String kind;
  private String text;
  private String suffix;
  private String capitalized;

  public IdentifierWord() {}

  public IdentifierWord(String kind, String text, String suffix, String capitalized) {
    this.kind = kind;
    this.text = text;
    this.suffix = suffix;
    this.capitalized = capitalized;
  }

  /** WORD, ACRONYM or TERM. */
  public String getKind() {
    return kind;
  }

  public void setKind(String kind) {
    this.kind = kind;
  }

  /** The word's spelling: lowercase for a WORD, the canonical spelling otherwise. */
  public String getText() {
    return text;
  }

  public void setText(String text) {
    this.text = text;
  }

  /** Lowercase plural "s" or digits glued to the word, written as is. */
  public String getSuffix() {
    return suffix == null ? "" : suffix;
  }

  public void setSuffix(String suffix) {
    this.suffix = suffix;
  }

  /** The word's CAPITALIZED-style form, without the suffix. */
  public String getCapitalized() {
    return capitalized;
  }

  public void setCapitalized(String capitalized) {
    this.capitalized = capitalized;
  }
}
