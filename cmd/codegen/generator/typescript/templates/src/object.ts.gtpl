{{- /* Generate class from GraphQL struct query type. */ -}}
{{ define "object" }}
	{{- with . }}
		{{- if .Fields }}

			{{- /* Write description. */ -}}
			{{- if .Description }}
				{{- /* Split comment string into a slice of one line per element. */ -}}
				{{- $desc := CommentToLines .Description -}}
/**
				{{- range $desc }}
 * {{ . }}
				{{- end }}
 */
			{{- end }}
{{""}}

			{{- /* Write object name. */ -}}
export class {{ .Name | QueryToClient | FormatName }} extends BaseClient { {{- with .Directives.SourceMap }} // {{ .Module }} ({{ .Filelink | ModuleRelPath }}) {{- end }}
            {{- /* Write private temporary field */ -}}
            {{ range $field := .Fields }}
                {{- if $field.TypeRef.IsScalar }}
  private readonly _{{ $field.Name }}?: {{ $field.TypeRef | FormatOutputType }} = undefined
                {{- end }}
        	{{- end }}

        	{{- /* Create constructor for temporary field */ -}}
{{ "" }}

  /**
   * Constructor is used for internal usage only, do not create object from it.
   */
   constructor(
    ctx?: Context,
            {{- range $i, $field := .Fields }}
               {{- if $field.TypeRef.IsScalar }}
     _{{ $field.Name }}?: {{ $field.TypeRef | FormatOutputType }},
               {{- end }}
            {{- end }}
   ) {
     super(ctx)
{{ "" }}
            {{- range $i, $field := .Fields }}
               {{- if $field.TypeRef.IsScalar }}
     this._{{ $field.Name }} = _{{ $field.Name }}
               {{- end }}
            {{- end }}
   }

      {{- /* Add custom method to main Client */ -}}
      {{- if .Name | QueryToClient | FormatName | eq "Client" }}

  /**
   * Get the Raw GraphQL client.
   */
  public getGQLClient() {
    return this._ctx.getGQLClient()
  }
      {{- end }}

			{{- /* Write methods. */ -}}
			{{- "" }}{{ range $field := .Fields }}
				{{- if Solve . }}
					{{- template "method_solve" $field }}
				{{- else }}
					{{- template "method" $field }}
				{{- end }}
			{{- end }}

{{- if eq .Name "Span" }}

  public async run<T>(fn: (span: Span) => Promise<T>) {
    const started = await this.start()
    const spanIdHex = await started.internalId()

    // Get the current span context
    const currentSpan = opentelemetry.trace.getSpan(opentelemetry.context.active()) || undefined;
    const currentSpanContext = currentSpan?.spanContext();

    if (!currentSpanContext) {
      return await fn(this)
    }

    // Extract trace ID and other fields
    const traceId = currentSpanContext.traceId;
    const traceFlags = currentSpanContext.traceFlags;
    const traceState = currentSpanContext.traceState;

    // Construct the new SpanContext
    const newSpanContext: opentelemetry.SpanContext = {
      traceId,
      spanId: spanIdHex,
      traceFlags,
      isRemote: true,
      traceState,
    };

    // Bind the new context
    const newContext = opentelemetry.trace.setSpan(
      opentelemetry.context.active(),
      opentelemetry.trace.wrapSpanContext(newSpanContext),
    );

    let spanError: Error | undefined = undefined
    try {
      return await opentelemetry.context.with(newContext, fn, this, started)
    } catch (e) {
      spanError = dag.error(e.message)
      throw e
    } finally {
      await started.end({ error: spanError })
    }
  }
{{- end }}
{{- if . | IsSelfChainable }}
{{""}}
  /**
   * Call the provided function with current {{ .Name | QueryToClient }}.
   *
   * This is useful for reusability and readability by not breaking the calling chain.
   */
  with = (arg: (param: {{ .Name | QueryToClient | FormatName }}) => {{ .Name | QueryToClient | FormatName }}) => {
    return arg(this)
  }
{{- end }}
}
		{{- end }}
	{{- end }}
{{ end }}
