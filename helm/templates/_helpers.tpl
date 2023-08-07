{{- define "yamlHash" -}}
  {{  . | toYaml | trim | sha256sum | substr 0 10 }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "elasticsearch-metrics-apiserver.commonLabels" -}}
app: elasticsearch-metrics-apiserver
app.kubernetes.io/name: elasticsearch-metrics-apiserver
{{- end }}

{{/*
Deployment labels
*/}}
{{- define "elasticsearch-metrics-apiserver.deploymentLabels" -}}
{{- $commonLabels := fromYaml (include "elasticsearch-metrics-apiserver.commonLabels" .) -}}
{{- $new := merge .Values.additionalLabels $commonLabels }}
{{- toYaml $new }}
{{- end -}}

{{/*
Combined labels of Common,Deployment,values.additionalLabels,values.podLabels
*/}}
{{- define "elasticsearch-metrics-apiserver.combinedLabels" -}}
{{- $combinedLabels := fromYaml (include "elasticsearch-metrics-apiserver.deploymentLabels" .) -}}
{{- $new := merge .Values.podLabels $combinedLabels }}
{{- toYaml $new }}
{{- end }}
