/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package fabric

const (
	Extension = `
token:
  # TMS stands for Token Management Service. A TMS is uniquely identified by a network, channel, and 
  # namespace identifiers. The network identifier should refer to a configure network (Fabric, and so on).
  # The meaning of channel and namespace are network dependant. For Fabric, the meaning is clear.
  tms:
    {{ TMSID }}:
      # Network identifier this TMS refers to
      network: {{ TMS.Network }}
      # Channel identifier within the specified network
      channel: {{ TMS.Channel }}
      # Namespace identifier within the specified channel
      namespace: {{ TMS.Namespace }}
      certification: 
        {{ if TMS.Certifiers }} interactive:
          ids: {{ range TMS.Certifiers }}
          - {{ . }}{{ end }}{{ end }}
      services:
        network:
          fabric:
            {{ if Endorsement }}
            fsc_endorsement:
              endorser: {{ Endorser }}
              id: {{ EndorserID }}
              policy:
                type: 1outn
              endorsers: {{ range Endorsers }}
              - {{ . }}{{ end }}
            {{ end }}
        storage:
          cleanup:
            enabled: true
            ttl: 1ms
            scanInterval: 1s
            batchSize: 100
            workerCount: 1 
      {{ if Wallets }}
      # Wallets associated with this TMS
      wallets:
        defaultCacheSize: 3
        {{ if Wallets.Certifiers }}
        # Certifiers wallets are used to certify tokens
        certifiers: {{ range Wallets.Certifiers }}
        - id: {{ .ID }}
          default: {{ .Default }}
          path: {{ .Path }}
        {{ end }}
        {{ end }}{{ if Wallets.Issuers }}
        # Issuers wallets are used to issue tokens
        issuers: {{ range Wallets.Issuers }}
        - id: {{ .ID }}
          default: {{ .Default }}
          path: {{ .Path }}
          opts:
            BCCSP:
              Default: {{ .Opts.Default }}
              # Settings for the SW crypto provider (i.e. when DEFAULT: SW)
              SW:
                 Hash: {{ .Opts.SW.Hash }}
                 Security: {{ .Opts.SW.Security }}
              # Settings for the PKCS#11 crypto provider (i.e. when DEFAULT: PKCS11)
              PKCS11:
                 # Location of the PKCS11 module library
                 Library: {{ .Opts.PKCS11.Library }}
                 # Token Label
                 Label: {{ .Opts.PKCS11.Label }}
                 # User PIN
                 Pin: {{ .Opts.PKCS11.Pin }}
                 Hash: {{ .Opts.PKCS11.Hash }}
                 Security: {{ .Opts.PKCS11.Security }}
        {{ end }}
      {{ end }}{{ if Wallets.Owners }}
        # Owners wallets are used to own tokens
        owners: {{ range Wallets.Owners }}
        - id: {{ .ID }}
          default: {{ .Default }}
          path: {{ .Path }}
          {{ if .Type }}
          type: {{ .Type }}
          {{ end }}
        {{ end }}
      {{ end }}{{ if Wallets.Auditors }}
        # Auditors wallets are used to audit tokens
        auditors: {{ range Wallets.Auditors }}
        - id: {{ .ID }}
          default: {{ .Default }}
          path: {{ .Path }}
          opts:
            BCCSP:
              Default: {{ .Opts.Default }}
              # Settings for the SW crypto provider (i.e. when DEFAULT: SW)
              SW:
                 Hash: {{ .Opts.SW.Hash }}
                 Security: {{ .Opts.SW.Security }}
              # Settings for the PKCS#11 crypto provider (i.e. when DEFAULT: PKCS11)
              PKCS11:
                 # Location of the PKCS11 module library
                 Library: {{ .Opts.PKCS11.Library }}
                 # Token Label
                 Label: {{ .Opts.PKCS11.Label }}
                 # User PIN
                 Pin: {{ .Opts.PKCS11.Pin }}
                 Hash: {{ .Opts.PKCS11.Hash }}
                 Security: {{ .Opts.PKCS11.Security }}
        {{ end }}
      {{ end }}
      {{ end }}
  `
)
