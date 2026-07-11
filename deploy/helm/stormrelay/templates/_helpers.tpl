{{- define "stormrelay.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "stormrelay.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "stormrelay.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "stormrelay.labels" -}}
helm.sh/chart: {{ include "stormrelay.chart" . }}
{{ include "stormrelay.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "stormrelay.selectorLabels" -}}
app.kubernetes.io/name: {{ include "stormrelay.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "stormrelay.componentLabels" -}}
{{ include "stormrelay.selectorLabels" . }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{- define "stormrelay.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "stormrelay.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- required "serviceAccount.name is required when serviceAccount.create=false" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "stormrelay.serverImage" -}}
{{- printf "%s:%s" .Values.server.image.repository (default .Chart.AppVersion .Values.server.image.tag) -}}
{{- end -}}

{{- define "stormrelay.workerImage" -}}
{{- printf "%s:%s" .Values.worker.image.repository (default .Chart.AppVersion .Values.worker.image.tag) -}}
{{- end -}}

{{- define "stormrelay.applicationSecret" -}}
{{- required "secrets.existingSecret is required; the chart never generates application secrets" .Values.secrets.existingSecret -}}
{{- end -}}

{{- define "stormrelay.databaseEnv" -}}
- name: STORMRELAY_DATABASE_URL
{{- if .Values.database.existingSecret }}
  valueFrom:
    secretKeyRef:
      name: {{ .Values.database.existingSecret | quote }}
      key: {{ .Values.database.urlKey | quote }}
{{- else if .Values.database.url }}
  value: {{ .Values.database.url | quote }}
{{- else }}
{{- fail "set database.existingSecret or database.url" }}
{{- end }}
{{- end -}}

{{- define "stormrelay.natsEnv" -}}
- name: STORMRELAY_NATS_URL
{{- if .Values.demoDependencies.enabled }}
  value: {{ printf "nats://%s-nats:4222" (include "stormrelay.fullname" .) | quote }}
{{- else if .Values.nats.existingSecret }}
  valueFrom:
    secretKeyRef:
      name: {{ .Values.nats.existingSecret | quote }}
      key: {{ .Values.nats.urlKey | quote }}
{{- else if .Values.nats.url }}
  value: {{ .Values.nats.url | quote }}
{{- else }}
{{- fail "set nats.existingSecret or nats.url, or enable demoDependencies" }}
{{- end }}
{{- end -}}

{{- define "stormrelay.commonEnv" -}}
- name: STORMRELAY_MASTER_KEY
  valueFrom:
    secretKeyRef:
      name: {{ include "stormrelay.applicationSecret" . | quote }}
      key: {{ .Values.secrets.masterKeyKey | quote }}
- name: STORMRELAY_BOOTSTRAP_API_KEY
  valueFrom:
    secretKeyRef:
      name: {{ include "stormrelay.applicationSecret" . | quote }}
      key: {{ .Values.secrets.bootstrapAPIKeyKey | quote }}
{{ include "stormrelay.databaseEnv" . }}
{{ include "stormrelay.natsEnv" . }}
- name: STORMRELAY_PUBLIC_BASE_URL
  value: {{ .Values.config.publicBaseURL | quote }}
- name: STORMRELAY_DEFAULT_TENANT_ID
  value: {{ .Values.config.defaultTenantID | quote }}
- name: STORMRELAY_WORKER_CONCURRENCY
  value: {{ .Values.config.workerConcurrency | quote }}
- name: STORMRELAY_RUNBOOK_CONCURRENCY
  value: {{ .Values.config.runbookConcurrency | quote }}
- name: STORMRELAY_EVENT_MAX_DELIVERIES
  value: {{ .Values.config.eventMaxDeliveries | quote }}
- name: STORMRELAY_EVENT_RETRY_BASE_DELAY
  value: {{ .Values.config.eventRetryBaseDelay | quote }}
- name: STORMRELAY_EVENT_RETRY_MAX_DELAY
  value: {{ .Values.config.eventRetryMaxDelay | quote }}
- name: STORMRELAY_RUNBOOK_HTTP_ALLOWED_HOSTS
  value: {{ .Values.config.runbookHTTPAllowedHosts | quote }}
- name: STORMRELAY_PLUGIN_ALLOWED_HOSTS
  value: {{ .Values.config.pluginAllowedHosts | quote }}
- name: STORMRELAY_ALLOW_UNAUTHENTICATED_SOURCES
  value: {{ .Values.config.allowUnauthenticatedSources | quote }}
- name: OTEL_EXPORTER_OTLP_TRACES_ENDPOINT
  value: {{ .Values.config.otlpTraceEndpoint | quote }}
- name: STORMRELAY_OTEL_TRACE_SAMPLE_RATIO
  value: {{ .Values.config.traceSampleRatio | quote }}
- name: STORMRELAY_OTEL_EXPORT_TIMEOUT
  value: {{ .Values.config.traceExportTimeout | quote }}
{{- with .Values.config.extraEnv }}
{{ toYaml . }}
{{- end }}
{{- end -}}
