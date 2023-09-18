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
Combined labels for deployment, pods, and pdb
*/}}
{{- define "elasticsearch-metrics-apiserver.combinedLabels" -}}
{{- $commonLabels := fromYaml (include "elasticsearch-metrics-apiserver.commonLabels" .) -}}
{{- $new := merge .Values.additionalLabels $commonLabels }}
{{- toYaml $new }}
{{- end -}}

{{/*
Combined Pod labels
*/}}
{{- define "elasticsearch-metrics-apiserver.combinedPodLabels" -}}
{{- $combinedLabels := fromYaml (include "elasticsearch-metrics-apiserver.combinedLabels" .) -}}
{{- $new := merge .Values.podLabels $combinedLabels }}
{{- toYaml $new }}
{{- end -}}
