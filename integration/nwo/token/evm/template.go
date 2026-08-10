/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package evm

// Extension is the per-node token configuration an EVM-backed TMS emits. It mirrors the fabric
// extension, which is almost entirely backend agnostic (the TMS coordinates, certification, storage
// and wallets are token-level concerns), and swaps the one block that is not: where fabric emits
// services.network.fabric, this emits services.network.evm.
//
// The evm block is the schema the driver's LoadConfig reads (design §10). The two must not drift, so
// a round-trip test renders this template and loads the result through the driver's own parser.
const Extension = `
token:
  tms:
    {{ TMSID }}:
      # Network identifier this TMS refers to
      network: {{ TMS.Network }}
      # EVM has no channel concept; the field stays empty
      channel: {{ TMS.Channel }}
      # Namespace identifier within the specified network
      namespace: {{ TMS.Namespace }}
      certification:
        {{ if TMS.Certifiers }} interactive:
          ids: {{ range TMS.Certifiers }}
          - {{ . }}{{ end }}{{ end }}
      services:
        network:
          evm:
            endpoint: {{ Endpoint }}
            chainID: {{ ChainID }}
            contracts:
              tokenState: {{ TokenState }}
              endorsementVerifier: {{ EndorsementVerifier }}
            finality:
              blockTag: {{ BlockTag }}
              pollInterval: {{ PollInterval }}
              timeout: {{ FinalityTimeout }}
              fromBlock: {{ FromBlock }}
            gas:
              strategy: estimate
              multiplier: 1.2
            {{ if IsEndorser }}
            endorser:
              enabled: true
              keystore: {{ EndorserKeystore }}
              address: {{ EndorserAddress }}
              fscIdentity: {{ NodeName }}
            {{ end }}
            {{ if HasSubmitter }}
            submitter:
              keystore: {{ SubmitterKeystore }}
              address: {{ SubmitterAddress }}
            {{ end }}
            endorsement:
              threshold: {{ Threshold }}
              allowlist: {{ range Allowlist }}
              - {{ . }}{{ end }}
              endorsers: {{ range Endorsers }}
              - address: {{ .Address }}
                fscIdentity: {{ .FSCIdentity }}{{ end }}
        storage:
          cleanup:
            enabled: true
            ttl: 1ms
            scanInterval: 1s
            batchSize: 100
            workerCount: 1
      {{ if Wallets }}
      wallets:
        defaultCacheSize: 3
        {{ if Wallets.Certifiers }}
        certifiers: {{ range Wallets.Certifiers }}
        - id: {{ .ID }}
          default: {{ .Default }}
          path: {{ .Path }}
        {{ end }}
        {{ end }}{{ if Wallets.Issuers }}
        issuers: {{ range Wallets.Issuers }}
        - id: {{ .ID }}
          default: {{ .Default }}
          path: {{ .Path }}
        {{ end }}
      {{ end }}{{ if Wallets.Owners }}
        owners: {{ range Wallets.Owners }}
        - id: {{ .ID }}
          default: {{ .Default }}
          path: {{ .Path }}
          {{ if .Type }}
          type: {{ .Type }}
          {{ end }}
        {{ end }}
      {{ end }}{{ if Wallets.Auditors }}
        auditors: {{ range Wallets.Auditors }}
        - id: {{ .ID }}
          default: {{ .Default }}
          path: {{ .Path }}
        {{ end }}
      {{ end }}
      {{ end }}
`
