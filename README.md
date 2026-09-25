# kitchen-sink

Personal playground for trying out libraries, patterns, and tools.

## Contents

| Directory | What it is |
|-----------|-----------|
| [`go-metrics-library-comparison`](./go-metrics-library-comparison) | Side-by-side comparison of 5 Go metrics libraries: prometheus/client_golang, OTel SDK (push + pull), hashicorp/go-metrics, VictoriaMetrics/metrics |
| [`go-http-framework-comparison`](./go-http-framework-comparison) | Side-by-side comparison of echo/chi/gin in standard (manual) and strict (oapi-codegen) mode, with k6 load test and error handler consistency test |
| [`go-di-comparison`](./go-di-comparison) | Side-by-side comparison of manual constructor injection, uber/fx, and google/wire applied to a UCP-like feature slices service with oapi-codegen |
| [`crossplane-xrd-catalog-source-poc`](./crossplane-xrd-catalog-source-poc) | Derives the UCP service catalog from Crossplane XRD metadata via a cross-cluster polling cache, instead of a hardcoded table |
| [`crossplane-xrd-versioned-schema-provisioning-poc`](./crossplane-xrd-versioned-schema-provisioning-poc) | Derives an XR provisioning target from a versioned XRD's `EntrySchema` |
| [`keycloak-session-revoke`](./keycloak-session-revoke) | Exercises Keycloak's admin API for revoking a user's active sessions |
| [`keycloak-audience-claim`](./keycloak-audience-claim) | Inspects the `aud`/`azp` claims on a QA Keycloak access token, to check whether enforcing `jwt.WithAudience("ucp-platform")` is feasible without a Keycloak config change |
| [`monaas-otel-collector-poc`](./monaas-otel-collector-poc) | Sandbox for a MonaaS-flavored OpenTelemetry Collector variant on GKE |
| [`cloud-monitoring-poc`](./cloud-monitoring-poc) | GCP Cloud Monitoring deployment sandbox |
| [`temporal-multi-cluster-replication-poc`](./temporal-multi-cluster-replication-poc) | Two-cluster Temporal Multi-Cluster Replication PoC — registration, namespace/history replication, both failover scenarios, rejoin, namespace handover, and adding a cluster to an already-populated deployment |
| [`terraform-gameday`](./terraform-gameday) | AWS gameday prep — Terraform VPC scenario plus up/down scripts for practicing incident scenarios |
| [`multi-cluster-provider-config-sync-poc`](./multi-cluster-provider-config-sync-poc) | MCUCP-306 — validates ArgoCD ApplicationSet matrix-generator fan-out of ExternalSecret+ProviderConfig across a growing set of local k3d Crossplane clusters |
| [`gcloud-netskope-tls-fix`](./gcloud-netskope-tls-fix) | Fixes `gcloud`/Python SSL failures caused by Netskope's non-RFC5280-compliant corporate CA certs, without disabling verification |
