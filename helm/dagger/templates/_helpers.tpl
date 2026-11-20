{{/*
Expand the name of the chart.
*/}}
{{- define "dagger.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "dagger.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "dagger.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Engine version for the chart version. Build metadata, such as the "+<digest>"
that Flux appends to charts from OCI repositories, is not part of the engine's
release tag and is not allowed in image tags.
*/}}
{{- define "dagger.engineVersion" -}}
{{- regexReplaceAll "\\+.*$" .Chart.Version "" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "dagger.labels" -}}
helm.sh/chart: {{ include "dagger.chart" . }}
{{ include "dagger.selectorLabels" . }}
app.kubernetes.io/version: v{{ include "dagger.engineVersion" . | trunc 62 | trimSuffix "-" }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: {{ template "dagger.name" . }}
{{- if .Values.engine.labels }}
{{ toYaml .Values.engine.labels }}
{{- end }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "dagger.selectorLabels" -}}
app.kubernetes.io/name: {{ include "dagger.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "dagger.serviceAccountName" -}}
{{- if .Values.engine.newServiceAccount.create }}
{{- include "dagger.fullname" . }}
{{- else }}
{{- default "default" .Values.engine.existingServiceAccount.name }}
{{- end }}
{{- end }}

{{/*
The engine's stop budget, passed as --graceful-stop-timeout. The engine has to
finish its stop, including marking its cache as cleanly shut down, before the
kubelet kills it at terminationGracePeriodSeconds; otherwise the next start
discards the whole local cache. Empty when the chart does not pass the flag.
*/}}
{{- define "dagger.gracefulStopTimeout" -}}
{{- $timeout := toString (.Values.engine.gracefulStopTimeout | default "") -}}
{{- if eq $timeout "auto" -}}
{{- $setByUser := contains "gracefulStopTimeout" (toString (.Values.engine.configJson | default "")) -}}
{{- range .Values.engine.args -}}
{{- if hasPrefix "--graceful-stop-timeout" (toString .) -}}
{{- $setByUser = true -}}
{{- end -}}
{{- end -}}
{{- if not (or $setByUser .Values.engine.image.ref) -}}
{{- $budget := sub (int .Values.engine.terminationGracePeriodSeconds) (int .Values.engine.gracefulStopMarginSeconds) -}}
{{- if le $budget 0 -}}
{{- fail (printf "engine.terminationGracePeriodSeconds (%v) must be greater than engine.gracefulStopMarginSeconds (%v)" .Values.engine.terminationGracePeriodSeconds .Values.engine.gracefulStopMarginSeconds) -}}
{{- end -}}
{{- printf "%ds" $budget -}}
{{- end -}}
{{- else -}}
{{- $timeout -}}
{{- end -}}
{{- end }}
