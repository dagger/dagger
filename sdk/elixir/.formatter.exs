# Used by "mix format"
[
  inputs: ["{mix,.formatter}.exs", "{config,lib,test}/**/*.{ex,exs}"],
  export: [
    locals_without_parens: [
      defn: 2,
      field: 2,
      field: 3
    ]
  ]
]
