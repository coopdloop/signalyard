{{- define "signalyard.name" -}}
{{- .Chart.Name -}}
{{- end -}}

{{- define "signalyard.fullname" -}}
{{- printf "%s" .Release.Name -}}
{{- end -}}

{{- define "signalyard.labels" -}}
app.kubernetes.io/part-of: signalyard
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "signalyard.image" -}}
{{- $registry := .Values.global.imageRegistry -}}
{{- if $registry }}{{ printf "%s/%s:%s" $registry .image .tag }}{{ else }}{{ printf "%s:%s" .image .tag }}{{ end -}}
{{- end -}}
