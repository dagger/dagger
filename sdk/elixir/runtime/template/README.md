# {{ .ModName }}

This is a README for you module. Feel free to edit it.

## Quickstart

Once this module is initialized, it can run an example from generated module by:

```
$ dagger call container-echo --string-arg=hello stdout
Hello
```

## The project structure

The module is just a regular Elixir application. The structure is looks like:

```
.
├── lib
│   └── {{ .AppName }}.ex
├── mix.exs
└── README.md
```

The `lib` is the Elixir source code while the `{{ .AppName }}.ex` is the main Dagger module.
