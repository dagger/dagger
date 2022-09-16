package playground

import (
	"html/template"
	"net/http"
	"net/url"
)

var page = template.Must(template.New("graphiql").Parse(`
<!DOCTYPE html>
<html>
  <head>
    <style>
      body {
        height: 100%;
        margin: 0;
        width: 100%;
        overflow: hidden;
      }

      #graphiql {
        height: 100vh;
      }
    </style>
	<link
		rel="stylesheet"
		href="https://cdn.jsdelivr.net/npm/graphiql@{{.version}}/graphiql.min.css"
		crossorigin="anonymous"
	/>
	<link
      rel="stylesheet"
	  crossorigin="anonymous"
      href="https://cdn.jsdelivr.net/npm/@graphiql/plugin-explorer@{{.explorerVersion}}/dist/style.css"
    />
  </head>

  <body>
    <div id="graphiql">Loading...</div>

	<script
		src="https://cdn.jsdelivr.net/npm/react@17/umd/react.production.min.js"
		crossorigin="anonymous"
	></script>
	<script
		src="https://cdn.jsdelivr.net/npm/react-dom@17/umd/react-dom.production.min.js"
		crossorigin="anonymous"
	></script>

	<script
	  src="https://cdn.jsdelivr.net/npm/graphiql@{{.version}}/graphiql.min.js"
	  crossorigin="anonymous"
	></script>
    <script
      crossorigin="anonymous"
      src="https://cdn.jsdelivr.net/npm/@graphiql/plugin-explorer@{{.explorerVersion}}/dist/graphiql-plugin-explorer.umd.js"
    ></script>

    <script>
{{- if .endpointIsAbsolute}}
      const url = {{.endpoint}};
      const subscriptionUrl = {{.subscriptionEndpoint}};
{{- else}}
      const url = location.protocol + '//' + location.host + {{.endpoint}};
      const wsProto = location.protocol == 'https:' ? 'wss:' : 'ws:';
      const subscriptionUrl = wsProto + '//' + location.host + {{.endpoint}};
{{- end}}
      var fetcher = GraphiQL.createFetcher({url, subscriptionUrl});

      function GraphiQLWithExplorer() {
        var [query, setQuery] = React.useState(` + "`" + `
# Welcome to Cloak's GraphQL explorer
#
# Keyboard shortcuts:
#
#   Prettify query:  Shift-Ctrl-P (or press the prettify button)
#
#  Merge fragments:  Shift-Ctrl-M (or press the merge button)
#
#        Run Query:  Ctrl-Enter (or press the play button)
#
#    Auto Complete:  Ctrl-Space (or just start typing)
#
# Here's a simple query to get you started, for more information visit
# https:\/\/github.com/dagger/cloak/blob/main/docs/unxpq-introduction.mdx
{
  core {
    image(ref: "alpine") {
      exec(input: {args: ["apk", "add", "curl"]}){
        fs {
          exec(input: {args: ["curl", "https://dagger.io"]}) {
            stdout(lines: 1)
          }
        }
      }
    }
  }
}
` + "`" + `);
        var explorerPlugin = GraphiQLPluginExplorer.useExplorerPlugin({
          query: query,
          onEdit: setQuery,
        });
        return React.createElement(GraphiQL, {
          fetcher: fetcher,
          defaultEditorToolsVisibility: true,
          plugins: [explorerPlugin],
          query: query,
          onEditQuery: setQuery,
        });
      }

      ReactDOM.render(
        React.createElement(GraphiQLWithExplorer),
        document.getElementById('graphiql'),
      );
    </script>
  </body>
</html>
`))

// Handler responsible for setting up the playground
func Handler(title string, endpoint string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "text/html; charset=UTF-8")
		err := page.Execute(w, map[string]interface{}{
			"title":                title,
			"endpoint":             endpoint,
			"endpointIsAbsolute":   endpointHasScheme(endpoint),
			"subscriptionEndpoint": getSubscriptionEndpoint(endpoint),
			"version":              "2.0.7",
			"explorerVersion":      "0.1.4",
		})
		if err != nil {
			panic(err)
		}
	}
}

// endpointHasScheme checks if the endpoint has a scheme.
func endpointHasScheme(endpoint string) bool {
	u, err := url.Parse(endpoint)
	return err == nil && u.Scheme != ""
}

// getSubscriptionEndpoint returns the subscription endpoint for the given
// endpoint if it is parsable as a URL, or an empty string.
func getSubscriptionEndpoint(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}

	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}

	return u.String()
}
