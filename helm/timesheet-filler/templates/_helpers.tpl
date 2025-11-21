{{/*
Expand the name of the chart.
*/}}
{{- define "timesheet-filler.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "timesheet-filler.fullname" -}}
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
{{- define "timesheet-filler.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "timesheet-filler.labels" -}}
helm.sh/chart: {{ include "timesheet-filler.chart" . }}
{{ include "timesheet-filler.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "timesheet-filler.selectorLabels" -}}
app.kubernetes.io/name: {{ include "timesheet-filler.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "timesheet-filler.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "timesheet-filler.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Return the proper image name
*/}}
{{- define "timesheet-filler.image" -}}
{{- printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion) }}
{{- end }}

{{/*
Return the proper image pull policy
*/}}
{{- define "timesheet-filler.imagePullPolicy" -}}
{{- .Values.image.pullPolicy | default "IfNotPresent" }}
{{- end }}

{{/*
Create the name of the email secret to use
*/}}
{{- define "timesheet-filler.emailSecretName" -}}
{{- if .Values.email.existingSecret }}
{{- .Values.email.existingSecret }}
{{- else }}
{{- printf "%s-email-credentials" (include "timesheet-filler.fullname" .) }}
{{- end }}
{{- end }}

{{/*
Return true if email is enabled and configured
*/}}
{{- define "timesheet-filler.emailEnabled" -}}
{{- if .Values.email.enabled }}
{{- if or .Values.email.existingSecret (and (eq .Values.email.provider "sendgrid") .Values.email.sendgrid.apiKey) (and (eq .Values.email.provider "ses") .Values.email.ses.accessKeyId .Values.email.ses.secretAccessKey) (and (eq .Values.email.provider "mailjet") .Values.email.mailjet.apiKey .Values.email.mailjet.secretKey) (and (eq .Values.email.provider "resend") .Values.email.resend.apiKey) (eq .Values.email.provider "oci") }}
{{- print "true" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Validate email configuration
*/}}
{{- define "timesheet-filler.validateEmail" -}}
{{- if .Values.email.enabled }}
{{- if not .Values.email.fromEmail }}
{{- fail "email.fromEmail is required when email is enabled" }}
{{- end }}
{{- if not (has .Values.email.provider (list "sendgrid" "ses" "oci" "mailjet" "resend")) }}
{{- fail (printf "email.provider must be one of: sendgrid, ses, oci, mailjet, resend. Got: %s" .Values.email.provider) }}
{{- end }}
{{- if eq .Values.email.provider "sendgrid" }}
{{- if and (not .Values.email.existingSecret) (not .Values.email.sendgrid.apiKey) }}
{{- fail "email.sendgrid.apiKey is required for SendGrid provider" }}
{{- end }}
{{- end }}
{{- if eq .Values.email.provider "ses" }}
{{- if and (not .Values.email.existingSecret) (or (not .Values.email.ses.accessKeyId) (not .Values.email.ses.secretAccessKey)) }}
{{- fail "email.ses.accessKeyId and email.ses.secretAccessKey are required for SES provider" }}
{{- end }}
{{- end }}
{{- if eq .Values.email.provider "mailjet" }}
{{- if and (not .Values.email.existingSecret) (or (not .Values.email.mailjet.apiKey) (not .Values.email.mailjet.secretKey)) }}
{{- fail "email.mailjet.apiKey and email.mailjet.secretKey are required for MailJet provider" }}
{{- end }}
{{- end }}
{{- if eq .Values.email.provider "resend" }}
{{- if and (not .Values.email.existingSecret) (not .Values.email.resend.apiKey) }}
{{- fail "email.resend.apiKey is required for Resend provider" }}
{{- end }}
{{- end }}
{{- end }}
{{- end }}
