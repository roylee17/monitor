{{/*
Expand the name of the chart.
*/}}
{{- define "ics-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
For cleaner names, we just use the release name (alice, bob, charlie)
*/}}
{{- define "ics-operator.fullname" -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "ics-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "ics-operator.labels" -}}
helm.sh/chart: {{ include "ics-operator.chart" . }}
{{ include "ics-operator.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "ics-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "ics-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "ics-operator.serviceAccountName" -}}
{{- if .Values.rbac.create }}
{{- default (include "ics-operator.fullname" .) .Values.rbac.serviceAccountName }}
{{- else }}
{{- default "default" .Values.rbac.serviceAccountName }}
{{- end }}
{{- end }}

{{/*
Get the HD path for key derivation
*/}}
{{- define "ics-operator.hdPath" -}}
{{- if .Values.keys.hdPath }}
{{- .Values.keys.hdPath }}
{{- else }}
{{- printf "m/44'/118'/%d'/0/0" (int .Values.validator.index) }}
{{- end }}
{{- end }}

{{/*
Get persistent peers as a comma-separated string
*/}}
{{- define "ics-operator.persistentPeers" -}}
{{- join "," .Values.peers.persistent }}
{{- end }}

{{/*
Get seeds as a comma-separated string
*/}}
{{- define "ics-operator.seeds" -}}
{{- join "," .Values.peers.seeds }}
{{- end }}

{{/*
Get provider endpoints as a comma-separated string
*/}}
{{- define "ics-operator.providerEndpoints" -}}
{{- join "," .Values.monitor.providerEndpoints }}
{{- end }}

{{/*
Determine the RPC endpoint for internal use
*/}}
{{- define "ics-operator.rpcEndpoint" -}}
{{- printf "http://%s-validator:26657" (include "ics-operator.fullname" .) }}
{{- end }}

{{/*
Pre-calculated addresses for standard test mnemonic
These are the addresses for the mnemonic:
"guard cream sadness conduct invite crumble clock pudding hole grit liar hotel maid produce squeeze return argue turtle know drive eight casino maze host"
*/}}
{{- define "ics-operator.knownAddresses" -}}
{{- $addresses := dict }}
{{- $_ := set $addresses "0" "cosmos1zaavvzxez0elundtn32qnk9lkm8kmcszzsv80v" }}
{{- $_ := set $addresses "1" "cosmos1yxgfnpk2u6h9prhhukcunszcwc277s2cwpds6u" }}
{{- $_ := set $addresses "2" "cosmos1pz2trypc6e25hcwzn4h7jyqc57cr0qg4wrua28" }}
{{- $addresses | toJson }}
{{- end }}

{{/*
Get the address for a validator (if using known test mnemonic)
*/}}
{{- define "ics-operator.getAddress" -}}
{{- $knownAddresses := include "ics-operator.knownAddresses" . | fromJson }}
{{- $index := .Values.validator.index | toString }}
{{- if hasKey $knownAddresses $index }}
{{- index $knownAddresses $index }}
{{- else }}
{{- printf "cosmos1_derived_address_index_%d" .Values.validator.index }}
{{- end }}
{{- end }}