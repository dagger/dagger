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
Common labels
*/}}
{{- define "dagger.labels" -}}
helm.sh/chart: {{ include "dagger.chart" . }}
{{ include "dagger.selectorLabels" . }}
app.kubernetes.io/version: v{{ .Chart.Version }}
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
