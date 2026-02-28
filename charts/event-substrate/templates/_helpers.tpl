{{/*
Expand the name of the chart.
*/}}
{{- define "event-substrate.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "event-substrate.fullname" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels applied to all resources.
*/}}
{{- define "event-substrate.labels" -}}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels for a given component.
Usage: include "event-substrate.selectorLabels" (dict "app" "api-gateway")
*/}}
{{- define "event-substrate.selectorLabels" -}}
app: {{ .app }}
{{- end }}
