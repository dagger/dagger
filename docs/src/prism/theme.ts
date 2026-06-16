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

const lightColors: PrismThemeColors = {
  background: "var(--color-backgroundSecondary)",
  text: "var(--color-gray-800)",
  muted: "var(--color-gray-500)",
  punctuation: "var(--color-gray-500)",
  keyword: "var(--color-purple-700)",
  operator: "var(--color-purple-700)",
  string: "var(--color-teal-700)",
  function: "var(--color-blue-700)",
  builtin: "var(--color-cyan-700)",
  type: "var(--color-orange-700)",
  property: "var(--color-blue-700)",
  constant: "var(--color-yellow-700)",
  directive: "var(--color-pink-700)",
  inserted: "var(--color-green-700)",
  deleted: "var(--color-red-700)",
};

const darkColors: PrismThemeColors = {
  background: "var(--color-backgroundSecondaryDark)",
  text: "var(--color-gray-100)",
  muted: "var(--color-gray-400)",
  punctuation: "var(--color-gray-400)",
  keyword: "var(--color-purple-200)",
  operator: "var(--color-purple-200)",
  string: "var(--color-teal-200)",
  function: "var(--color-blue-200)",
  builtin: "var(--color-cyan-200)",
  type: "var(--color-orange-200)",
  property: "var(--color-blue-200)",
  constant: "var(--color-yellow-200)",
  directive: "var(--color-pink-200)",
  inserted: "var(--color-green-200)",
  deleted: "var(--color-red-200)",
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

export const daggerLightPrismTheme = createPrismTheme(lightColors);
export const daggerDarkPrismTheme = createPrismTheme(darkColors);
