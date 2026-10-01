{{- define "umpp.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "umpp.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "umpp.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "umpp.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "umpp.commonLabels" -}}
helm.sh/chart: {{ include "umpp.chart" . }}
app.kubernetes.io/name: {{ include "umpp.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- with .Values.global.commonLabels }}
{{ toYaml . }}
{{- end }}
{{- end -}}

{{- define "umpp.selectorLabels" -}}
app.kubernetes.io/name: {{ include "umpp.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "umpp.componentLabels" -}}
{{ include "umpp.commonLabels" .root }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{- define "umpp.annotations" -}}
helm.sh/chart: {{ include "umpp.chart" . }}
{{- with .Values.global.commonAnnotations }}
{{ toYaml . }}
{{- end }}
{{- end -}}

{{- define "umpp.controlApi.fullname" -}}
{{- printf "%s-control-api" (include "umpp.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "umpp.dashboard.fullname" -}}
{{- printf "%s-dashboard" (include "umpp.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "umpp.postgres.fullname" -}}
{{- printf "%s-postgres" (include "umpp.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "umpp.redis.fullname" -}}
{{- printf "%s-redis" (include "umpp.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "umpp.nats.fullname" -}}
{{- printf "%s-nats" (include "umpp.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "umpp.relay.fullname" -}}
{{- printf "%s-relay" (include "umpp.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "umpp.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "umpp.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "umpp.image" -}}
{{- $registry := .root.Values.global.imageRegistry -}}
{{- if $registry -}}
{{- printf "%s/%s:%s" $registry .image.repository (default .root.Chart.AppVersion .image.tag) -}}
{{- else -}}
{{- printf "%s:%s" .image.repository (default .root.Chart.AppVersion .image.tag) -}}
{{- end -}}
{{- end -}}

{{- define "umpp.secretName" -}}
{{- default (include "umpp.fullname" .) .Values.secrets.existingSecret -}}
{{- end -}}

{{- define "umpp.externalDatabaseSecretName" -}}
{{- if .Values.postgres.enabled -}}
{{- include "umpp.secretName" . -}}
{{- else -}}
{{- default (include "umpp.secretName" .) .Values.externalDatabase.passwordSecret.name -}}
{{- end -}}
{{- end -}}

{{- define "umpp.externalDatabasePasswordKey" -}}
{{- if or .Values.postgres.enabled (not .Values.externalDatabase.passwordSecret.name) -}}
{{- .Values.secrets.keys.postgresPassword -}}
{{- else -}}
{{- .Values.externalDatabase.passwordSecret.key -}}
{{- end -}}
{{- end -}}

{{- define "umpp.secretChecksum" -}}
{{- if .Values.secrets.existingSecret -}}
{{- $existing := lookup "v1" "Secret" .Release.Namespace .Values.secrets.existingSecret -}}
{{- if $existing -}}
{{- $existing.metadata.resourceVersion | sha256sum -}}
{{- else -}}
{{- printf "external-secret-%s-unresolved" .Values.secrets.existingSecret | sha256sum -}}
{{- end -}}
{{- else -}}
{{- include (print .Template.BasePath "/secret.yaml") . | sha256sum -}}
{{- end -}}
{{- end -}}

{{- define "umpp.proxyPort" -}}
{{- regexFind "[0-9]+$" .Values.controlApi.proxy.listen | int -}}
{{- end -}}

{{- define "umpp.tlsPort" -}}
{{- regexFind "[0-9]+$" .Values.controlApi.proxy.tls.listen | int -}}
{{- end -}}

{{- define "umpp.postgresHost" -}}
{{- if .Values.postgres.enabled -}}
{{- include "umpp.postgres.fullname" . -}}
{{- else -}}
{{- .Values.externalDatabase.host -}}
{{- end -}}
{{- end -}}

{{- define "umpp.postgresPort" -}}
{{- if .Values.postgres.enabled -}}
{{- .Values.postgres.service.port -}}
{{- else -}}
{{- .Values.externalDatabase.port -}}
{{- end -}}
{{- end -}}

{{- define "umpp.postgresDatabase" -}}
{{- if .Values.postgres.enabled -}}
{{- .Values.postgres.database -}}
{{- else -}}
{{- .Values.externalDatabase.database -}}
{{- end -}}
{{- end -}}

{{- define "umpp.postgresUser" -}}
{{- if .Values.postgres.enabled -}}
{{- .Values.postgres.user -}}
{{- else -}}
{{- .Values.externalDatabase.user -}}
{{- end -}}
{{- end -}}

{{- define "umpp.podMetadata" -}}
{{- $root := .root -}}
annotations:
  checksum/config: {{ include (print $root.Template.BasePath "/configmap.yaml") $root | sha256sum }}
  checksum/secret: {{ include "umpp.secretChecksum" $root }}
  {{- with $root.Values.podAnnotations }}
  {{- toYaml . | nindent 2 }}
  {{- end }}
labels:
  {{- include "umpp.componentLabels" (dict "root" $root "component" .component) | nindent 2 }}
  {{- with $root.Values.podLabels }}
  {{- toYaml . | nindent 2 }}
  {{- end }}
{{- end -}}
