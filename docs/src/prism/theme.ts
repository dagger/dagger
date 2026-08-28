// Syntax highlighting for the Dagger docs.
//
// Every colour is a CSS variable rather than a literal, so a single theme
// object serves both light and dark. The two palettes live together in
// custom.scss under "Syntax palette" and flip with html[data-theme], which
// keeps them next to the design tokens they are derived from.

type PrismThemeColors = {
  background: string;
  text: string;
  muted: string;
  punctuation: string;
  keyword: string;
  operator: string;
  string: string;
  function: string;
  builtin: string;
  type: string;
  property: string;
  constant: string;
  directive: string;
  inserted: string;
  deleted: string;
};

const themedColors: PrismThemeColors = {
  // The code block's own surface and radius come from custom.scss; leaving
  // this transparent stops Prism painting a second background inside the
  // rounded border, which showed as a square fringe at the corners.
  background: "transparent",
  text: "var(--syntax-text)",
  muted: "var(--syntax-muted)",
  punctuation: "var(--syntax-punctuation)",
  keyword: "var(--syntax-keyword)",
  operator: "var(--syntax-operator)",
  string: "var(--syntax-string)",
  function: "var(--syntax-function)",
  builtin: "var(--syntax-builtin)",
  type: "var(--syntax-type)",
  property: "var(--syntax-property)",
  constant: "var(--syntax-constant)",
  directive: "var(--syntax-directive)",
  inserted: "var(--syntax-inserted)",
  deleted: "var(--syntax-deleted)",
};

function createPrismTheme(colors: PrismThemeColors) {
  return {
    plain: {
      color: colors.text,
      backgroundColor: colors.background,
    },
    styles: [
      {
        types: ["comment", "prolog", "doctype", "cdata"],
        style: {
          color: colors.muted,
          fontStyle: "italic",
        },
      },
      {
        types: ["punctuation"],
        style: {
          color: colors.punctuation,
        },
      },
      {
        types: ["property", "attr-name", "parameter"],
        style: {
          color: colors.property,
        },
      },
      {
        types: ["tag", "deleted"],
        style: {
          color: colors.deleted,
        },
      },
      {
        types: ["symbol"],
        style: {
          color: colors.constant,
        },
      },
      {
        types: ["boolean", "constant", "number"],
        style: {
          color: colors.constant,
        },
      },
      {
        types: ["selector", "char", "inserted"],
        style: {
          color: colors.inserted,
        },
      },
      {
        types: ["builtin"],
        style: {
          color: colors.builtin,
        },
      },
      {
        types: ["string", "entity", "url", "attr-value"],
        style: {
          color: colors.string,
        },
      },
      {
        types: ["operator"],
        style: {
          color: colors.operator,
        },
      },
      {
        types: ["atrule", "keyword"],
        style: {
          color: colors.keyword,
        },
      },
      {
        types: ["function"],
        style: {
          color: colors.function,
        },
      },
      {
        types: ["regex", "important", "variable"],
        style: {
          color: colors.builtin,
        },
      },
      {
        types: ["class-name"],
        style: {
          color: colors.type,
        },
      },
      {
        types: ["directive"],
        style: {
          color: colors.directive,
        },
      },
      {
        types: ["bold"],
        style: {
          fontWeight: "bold",
        },
      },
      {
        types: ["italic"],
        style: {
          fontStyle: "italic",
        },
      },
    ],
  };
}

export const daggerPrismTheme = createPrismTheme(themedColors);

/** @deprecated The theme is no longer dark-only; use daggerPrismTheme. */
export const daggerDarkPrismTheme = daggerPrismTheme;
