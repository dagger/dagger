import type * as PrismNamespace from "prismjs";

export default function registerDang(
  PrismObject: typeof PrismNamespace,
): void {
  if (PrismObject.languages.dang) {
    return;
  }

  PrismObject.languages.dang = {
    "triple-quoted-string": {
      pattern: /"""[\s\S]*?"""/,
      greedy: true,
      alias: "string",
    },
    "template-string": {
      pattern: /(`{3,})(?:[A-Za-z][A-Za-z0-9_-]*)?[\s\S]*?\1|`(?:\\[\s\S]|\$\{(?:[^{}]|\{[^{}]*\})*\}|[^`\\])*`/,
      greedy: true,
      alias: "string",
      inside: {
        "template-language": {
          pattern: /^(`{3,})[A-Za-z][A-Za-z0-9_-]*/,
          lookbehind: true,
          alias: "symbol",
        },
        interpolation: {
          pattern: /((?:^|[^\\])(?:\\\\)*)\$\{(?:[^{}]|\{[^{}]*\})*\}/,
          lookbehind: true,
          inside: {
            "interpolation-punctuation": {
              pattern: /^\$\{|\}$/,
              alias: "punctuation",
            },
          },
        },
        "string-punctuation": {
          pattern: /^`+|`+$/,
          alias: "punctuation",
        },
      },
    },
    string: {
      pattern: /"(?:\\["\\nrt]|\{\{[\s\S]*?\}\}|[^"\\])*"/,
      greedy: true,
      inside: {
        escape: /\\["\\nrt]/,
        interpolation: {
          pattern: /\{\{[\s\S]*?\}\}/,
          inside: {
            "interpolation-punctuation": {
              pattern: /^\{\{|\}\}$/,
              alias: "punctuation",
            },
          },
        },
      },
    },
    comment: {
      pattern: /#.*/,
      greedy: true,
    },
    directive: {
      pattern: /@[A-Za-z_][A-Za-z0-9_]*/,
      alias: "function",
    },
    keyword:
      /\b(?:and|assert|break|case|catch|continue|directive|else|enum|if|implements|import|interface|let|new|on|or|pub|raise|return|scalar|try|type|union)\b/,
    builtin: /\b(?:loop|print)\b/,
    self: {
      pattern: /\bself\b/,
      alias: "variable",
    },
    boolean: /\b(?:false|true)\b/,
    null: {
      pattern: /\bnull\b/,
      alias: "constant",
    },
    number: /\b\d+(?:\.\d+)?\b/,
    property: {
      pattern: /(^|[({,\[\s])([A-Za-z_][A-Za-z0-9_]*)(?=\s*:)/m,
      lookbehind: true,
    },
    function: {
      pattern: /(\.)[A-Za-z_][A-Za-z0-9_]*/,
      lookbehind: true,
    },
    "class-name": /\b[A-Z][A-Za-z0-9_]*\b/,
    operator: /::|\?\?|=>|==|!=|<=|>=|\+=|[!&|=<>+\-*/%]/,
    punctuation: /[{}()[\],.;:]/,
  };
}
