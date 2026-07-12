{{- define "training-platform.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "training-platform.fullname" -}}
{{- printf "%s" (default .Release.Name (.Values.fullnameOverride | default "")) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "training-platform.labels" -}}
app.kubernetes.io/name: {{ include "training-platform.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "training-platform.selectorLabels" -}}
app.kubernetes.io/name: {{ include "training-platform.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "training-platform.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "training-platform.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "training-platform.image" -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) -}}
{{- end -}}
