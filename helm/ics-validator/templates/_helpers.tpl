{{/*
Expand the name of the chart.
*/}}
{{- define "ics-validator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
For cleaner names, we just use the release name (alice, bob, charlie)
*/}}
{{- define "ics-validator.fullname" -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "ics-validator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "ics-validator.labels" -}}
helm.sh/chart: {{ include "ics-validator.chart" . }}
{{ include "ics-validator.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "ics-validator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "ics-validator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "ics-validator.serviceAccountName" -}}
{{- if .Values.rbac.create }}
{{- default (include "ics-validator.fullname" .) .Values.rbac.serviceAccountName }}
{{- else }}
{{- default "default" .Values.rbac.serviceAccountName }}
{{- end }}
{{- end }}

{{/*
Get the HD path for key derivation
*/}}
{{- define "ics-validator.hdPath" -}}
{{- if .Values.keys.hdPath }}
{{- .Values.keys.hdPath }}
{{- else }}
{{- printf "m/44'/118'/%d'/0/0" (int .Values.validator.index) }}
{{- end }}
{{- end }}

{{/*
Get persistent peers as a comma-separated string
*/}}
{{- define "ics-validator.persistentPeers" -}}
{{- join "," .Values.peers.persistent }}
{{- end }}

{{/*
Get seeds as a comma-separated string
*/}}
{{- define "ics-validator.seeds" -}}
{{- join "," .Values.peers.seeds }}
{{- end }}

{{/*
Get provider endpoints as a comma-separated string
*/}}
{{- define "ics-validator.providerEndpoints" -}}
{{- join "," .Values.monitor.providerEndpoints }}
{{- end }}

{{/*
Determine the RPC endpoint for internal use
*/}}
{{- define "ics-validator.rpcEndpoint" -}}
{{- printf "http://%s-validator:26657" (include "ics-validator.fullname" .) }}
{{- end }}

{{/*
Pre-calculated addresses for standard test mnemonic
These are the addresses for the mnemonic:
"guard cream sadness conduct invite crumble clock pudding hole grit liar hotel maid produce squeeze return argue turtle know drive eight casino maze host"
*/}}
{{- define "ics-validator.knownAddresses" -}}
{{- $addresses := dict }}
{{- $_ := set $addresses "0" "cosmos1zaavvzxez0elundtn32qnk9lkm8kmcszzsv80v" }}
{{- $_ := set $addresses "1" "cosmos1yxgfnpk2u6h9prhhukcunszcwc277s2cwpds6u" }}
{{- $_ := set $addresses "2" "cosmos1pz2trypc6e25hcwzn4h7jyqc57cr0qg4wrua28" }}
{{- $addresses | toJson }}
{{- end }}

{{/*
Get the address for a validator (if using known test mnemonic)
*/}}
{{- define "ics-validator.getAddress" -}}
{{- $knownAddresses := include "ics-validator.knownAddresses" . | fromJson }}
{{- $index := .Values.validator.index | toString }}
{{- if hasKey $knownAddresses $index }}
{{- index $knownAddresses $index }}
{{- else }}
{{- printf "cosmos1_derived_address_index_%d" .Values.validator.index }}
{{- end }}
{{- end }}