/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package topology

const CoreTemplate = `---
# Logging section
logging:
  # Spec
  spec: {{ Topology.Logging.Spec }}
  otel:
    sanitize: {{ Topology.Logging.OtelSanitize }}
fsc:
  # The FSC id provides a name for this node instance and is used when
  # naming docker resources.
  id: {{ Peer.ID }}
  # Identity of this node, used to connect to other nodes
  identity:
    # X.509 certificate used as identity of this node
    cert:
      file: {{ .NodeLocalCertPath Peer }}
    # Private key matching the X.509 certificate
    key:
      file: {{ .NodeLocalPrivateKeyPath Peer }}
  # Client X.509 certificates
  client:
    certs:
    {{- range Peer.Admins }}
    - {{ . }} 
    {{- end }}
  # Server-side TLS defaults, inherited field by field by every listener this node owns
  # (fsc.grpc and fsc.web). A service's own tls: block overrides only the fields it
  # actually sets; everything else falls through to here.
  tls:
    # Require server-side TLS
    enabled: true
    # X.509 certificate used for TLS server
    cert:
      file: {{ .NodeLocalTLSDir Peer }}/server.crt
    # Private key used for TLS server
    key:
      file: {{ .NodeLocalTLSDir Peer }}/server.key
    # Require client certificates / mutual TLS for inbound connections. Overridden per
    # listener below where a suite needs it.
    clientAuthRequired: false
  grpc:
    enabled: true
    # This represents the endpoint to other FSC nodes in the same organization.
    address: 0.0.0.0:{{ .NodePort Replica "Listen" }}
    # TLS Settings
    # (We use here the same set of properties as Hyperledger Fabric)
    # enabled, cert and key are inherited from fsc.tls above.
    tls:
      # Require client certificates / mutual TLS for inbound connections.
      # Note that clients that are not configured to use a certificate will
      # fail to connect to the node.
      clientAuthRequired: {{ .ClientAuthRequired }}
      # If mutual TLS is enabled, clientRootCAs.files contains a list of additional root certificates
      # used for verifying certificates of client connections.
      {{- if .ClientAuthRequired }}
      clientRootCAs:
        files:
        - {{ .NodeLocalTLSDir Peer }}/ca.crt
      {{- end }}
  # P2P configuration
  p2p:
    type: {{ .P2PCommunicationType }}
    # Maximum allowed size (in bytes) for incoming P2P messages. Default: 10485760 (10 MiB)
    # This protects the node from memory exhaustion by rejecting oversized payloads before deserialization.
    # The limit cannot be disabled: zero or negative values fall back to the default.
    maxRecvMsgSize: 20485760
    # Maximum allowed size (in bytes) for outgoing P2P messages. Default: 10485760 (10 MiB)
    # The limit cannot be disabled: zero or negative values fall back to the default.
    maxSendMsgSize: 20485760
    # Listening address
    listenAddress: /ip4/0.0.0.0/tcp/{{ .NodePort Replica "P2P" }}
    opts:
      # Only needed when type == libp2p
      # If empty, this is a P2P bootstrap node. Otherwise, it contains the name of the FSC node that is a bootstrap node
      libp2p:
        bootstrapNode: {{ if eq .P2PCommunicationType "libp2p" }}{{ .BootstrapNode Peer }}{{ end}}
  persistences: {{ range $key, $value := Persistences }}
    {{ $key }}:
      type: {{ $value.Type }}
      opts:
        {{- if eq $value.Type "postgres" }}
        dataSource: {{ $value.SQL.DataSource }}
        tablePrefix:
        maxOpenConns: 90
        maxIdleConns: 3
        maxIdleTime: 45s
        skipCreateTable: false
        {{- else if eq $value.Type "sqlite" }}
        dataSource: {{ $value.SQL.DataSource }}
        tablePrefix:    
        maxOpenConns: 90
        maxIdleConns: 3
        maxIdleTime: 45s
        skipCreateTable: false
        skipPragmas: false
        {{- else if eq $value.Type "memory" }}
        # Memory has hard-coded opts
        {{- else }}
        # Unknown type {{ $value.Type }}
        {{- end }}
  {{- end }}
  # The Key-Value Store is used to store various information related to the FSC node
  kvs:
    cache:
        # Sets the maximum number of cached items 
        size: 200
  # HTML Server configuration for REST calls
  web:
    enabled: {{ WebEnabled }}
    # HTTPS server listener address
    address: 0.0.0.0:{{ .NodePort Replica "Web" }}
    # enabled, cert, key and clientAuthRequired:false are all inherited from fsc.tls.
    tls:
      # clientRootCAs without clientAuthRequired means: verify a client certificate if one
      # is offered, but do not demand one.
      clientRootCAs:
        files:
        - {{ .NodeLocalTLSDir Peer }}/ca.crt
  tracing:
    # Type of provider to be used: none (default), file, otlp, console
    provider: {{ Topology.Monitoring.TracingType }}
    # Tracer configuration when provider == 'file'
    file:
      # The file where the traces are going to be stored
      path: {{ .NodeDir Replica }}/trace.out
    # Tracer configuration when provider == 'otlp'
    otlp:
      # The address of collector where we should send the traces
      address: {{ TracingEndpoint }}
    sampling:
      ratio: {{ SamplingRatio }}
  metrics:
    # metrics provider is one of prometheus, or disabled
    provider: {{ Topology.Monitoring.MetricsType }}
    # Without an address the operations endpoints (/metrics, /logspec) are served on the
    # fsc.web listener and share its TLS. Setting one gives them a listener of their own,
    # and then fsc.metrics.tls applies to it — inheriting from fsc.tls like any other
    # listener. NWO leaves it unset so the endpoints stay reachable on the web client.
    #
    # Require a verified client certificate to scrape /metrics and /logspec. Separate from
    # any listener's TLS, and stricter than the web listener, which verifies a client
    # certificate only if one is offered. Replaces fsc.metrics.prometheus.tls, which was
    # never transport TLS despite the name.
    clientAuthRequired: {{ Topology.Monitoring.TLS }}

  # The endpoint section tells how to reach other FSC node in the network.
  # For each node, the name, the domain, the identity of the node, and its addresses must be specified.
  endpoint:
    resolvers: {{ range Resolvers }}
    - name: {{ .Name }}
      domain: {{ .Domain }}
      identity:
        path: {{ .Identity.Path }}
      addresses: {{ range $key, $value := .Addresses }}
         {{ $key }}: {{ $value }} 
      {{- end }}
      aliases: {{ range .Aliases }}
        - {{ . }} 
      {{- end }}
  {{- end }}

{{ range Extensions }}
{{.}}
{{- end }}
`
