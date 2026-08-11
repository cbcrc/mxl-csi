{{- define "mxl-csi.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "mxl-csi.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "mxl-csi.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "mxl-csi.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "mxl-csi.labels" -}}
helm.sh/chart: {{ include "mxl-csi.chart" . }}
app.kubernetes.io/name: {{ include "mxl-csi.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "mxl-csi.controllerServiceAccountName" -}}
{{- if .Values.controller.serviceAccount.create -}}
{{- default (printf "%s-controller-sa" (include "mxl-csi.fullname" .)) .Values.controller.serviceAccount.name -}}
{{- else -}}
{{- required "controller.serviceAccount.name is required when controller.serviceAccount.create=false" .Values.controller.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "mxl-csi.nodeServiceAccountName" -}}
{{- if .Values.node.serviceAccount.create -}}
{{- default (printf "%s-node-sa" (include "mxl-csi.fullname" .)) .Values.node.serviceAccount.name -}}
{{- else -}}
{{- required "node.serviceAccount.name is required when node.serviceAccount.create=false" .Values.node.serviceAccount.name -}}
{{- end -}}
{{- end -}}
