Prism.languages.cue = Prism.languages.extend("clike", {
  // https://github.com/PrismJS/prism/blob/master/components/prism-swift.js
  string: {
    pattern: /(["'`])(?:\\[\s\S]|(?!\1)[^\\])*\1/,
    greedy: true,
    inside: {
      interpolation: {
        pattern: /\\\#*\((?:[^()]|\([^)]+\))+\)/,
        inside: {
          delimiter: {
            pattern: /^\\\#*\(|\)$/,
            alias: "variable",
          },
        },
      },
    },
  },

  // https://cuelang.org/docs/references/spec/#values
  keyword: /\b(?:package|import|if|else|for|in|let)\b/,
  boolean: /\b(?:true|false)\b/,
  constant: /\b(?:_\|_|_)\b/,

  // https://github.com/PrismJS/prism/blob/master/components/prism-go.js
  number: /(?:\b0x[a-f\d]+|(?:\b\d+(?:\.\d*)?|\B\.\d+)(?:e[-+]?\d+)?)i?/i,
  operator:
    /[*\/%^!=]=?|\+[=+]?|-[=-]?|\|[=|]?|&(?:=|&|\^=?)?|>(?:>=?|=)?|<(?:<=?|=|-)?|:=|\.\.\./,

  // https://cuelang.org/docs/references/spec/#predeclared-identifiers
  builtin:
    /\b(?:len|null|bool|int|float|string|bytes|number|u?int(?:8|16|32|64|128)?|rune|float(?:32|64))\b/,
});
