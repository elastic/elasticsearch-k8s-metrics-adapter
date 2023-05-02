{{- define "yamlHash" -}}
  {{  . | toYaml | trim | sha256sum | substr 0 10 }}
{{- end }}