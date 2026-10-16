# horizon-workflow-approval-gate-poc

Spike for MCUCP-304: validates whether Horizon Workflow (OneCloud's WaaS
platform) can drive a two-layer approval chain — first the submitting
tenant's admins, then a fixed team (`clsd-ucp-team`) standing in for a real
network-team approver group.

The resume callback (Part B of the design) is deferred: every real callback
URL found in the research is an internal `r-local.net` service, and it's not
yet confirmed whether Workflow's QA backend can reach anything this script
could stand up. See the design doc for details.

Full design: `docs/projects/universal-control-plane-ucp/pocs/horizon-workflow-approval-gate/design.md`
in the UCP documentation repository. Evidence from real runs (both modes,
all three scenarios) is written up in `implementation.md` alongside it.

## Prerequisites

- VPN access to reach `qa-horizon-workflow-api.r-local.net` and the OneCloud
  QA Keycloak realm.
- Admin access to a QA OneCloud tenant (used as the submitting tenant for
  layer 1), and Admin access to the `clsd-ucp-team` Team (used for layer 2).
- The Workflow service subscribed, and **both** the `admin` and `creator`
  service roles granted to your account on that tenant (self-service via
  Tenant Management — see the Workflow onboarding guides). `admin` is
  required for Kind/definition management and for creating test workflows;
  `creator` is separately required to create **production** workflows
  (`MODE=production`) — `admin` alone is not enough for that.

## Running

```sh
KEYCLOAK_ISSUER=https://qa2-accounts-onecloud.rakuten-it.com/auth/realms/roc \
KEYCLOAK_CLIENT_ID=rns:roc:portal \
WORKFLOW_BASE_URL=https://qa-horizon-workflow-api.r-local.net \
TENANT_RNS=rns:roc:iam::<your-tenant> \
go run .
```

The script runs non-interactively and prints every request and response it
makes. Set `TOKEN` to a bearer token to skip the interactive Keycloak login.

### Modes (`MODE` env var)

- `test` (default): drives a **test workflow**, via the tenant-scoped
  `test-workflows` endpoints. No publishing required, and no real
  tenant-admin/team-admin role propagation is needed since test workflows
  support impersonation. Never appears in the OneCloud Portal.
- `production`: publishes the definition draft, creates a real
  **production workflow**, and drives it via the ABAC-scoped `/jobs`
  resource — the same resource the OneCloud Portal's Approvals/My Requests
  tabs are backed by. This is also the resource a UCP-native interface
  (e.g. a CLI) would call directly, using a bearer token from the same
  shared Keycloak realm, instead of requiring a Portal login.

### Scenarios (`SCENARIO` env var), same for both modes

- `happy` (default): approve layer 1, then approve layer 2 — ends `Successful`.
- `reject-layer1`: reject at layer 1 — ends `Rejected`, layer 2 never reached.
- `cancel-layer1`: cancel at layer 1 — ends `Canceled`, layer 2 never reached.

### Variants (`VARIANT` env var, comma separated)

Optional definition features, combined with any mode and scenario. Results
from real runs of each are in `implementation.md` of the design docs.

| Variant | What it changes |
|---|---|
| `vlan` | Layer 2 is skipped when the request carries `params.vlan` (set `VLAN`); otherwise the network team completes it with a `vlan` value. `skip_if` must evaluate to a boolean. |
| `service-role` | Layer 1 uses a `service_resource_role` group (`UCP_SERVICE_RNS`, `UCP_RESOURCE_RNS`, `UCP_ROLE`) instead of the `tenant` kind. With an empty `UCP_RESOURCE_RNS` the group resolves to nothing. |
| `user-groups` | Layer 1 uses one `user` group per email in `APPROVER_EMAILS` (5 slots, `none` for an empty list), with the fixed team as backup. |
| `email` | An `email` callback node sends a custom message to `APPROVER_EMAILS` before layer 1 (`RESOURCE_ID` appears in the subject). |
| `no-successful` | The approve path ends in `Completed`, so no path sets `Successful`; used to see whether the built-in "ask for review" email is suppressed. |
| `custom-kind` | Layer 1 uses a custom HTTP user group kind that reads a tenant's members from Core Data (see below). Production mode only. |
| `fingerprint` | The definition sets `fingerprint_fields` (`resource_type`, `resource_id`, `tenant_name`). Creates a workflow, a duplicate (`409 WF0011`), a different one, one missing a field (`400 WF0040`), a repeat after cancelling the first, a workflow on a newly published version, and five simultaneous creations. Production mode only. |
| `fingerprint-race` | Same fingerprint; creates pairs of one payload with the second delayed by 0, 100, 300 and 1000 ms (3 pairs each) and prints how many of each pair were accepted. Production mode only. |
| `fingerprint-types` | Same fingerprint plus a number, boolean, object, array, long string and an HTML string. Creates one workflow, repeats it, and waits 60 s so the review email can be read before cancelling. Production mode only. |
| `idempotency` | Replays one `X-Request-ID` across creations and calls `POST /jobs:check-duplicate` at several delays after a creation. Production mode only. |

The four fingerprint-related variants cancel every workflow they create. Use
them with `user-groups` and `APPROVER_EMAILS=<your email>` so that only the
operator is emailed; the default `tenant` group emails every admin of the
tenant.

### Custom user group kind (`VARIANT=custom-kind`)

Teaches Workflow to resolve "UCP `tenant-admin` holders of tenant X" from
Core Data, with a UCP-owned service client, so the approver group follows the
request's tenant without that tenant being a OneCloud tenant.

```mermaid
sequenceDiagram
    participant S as Script (operator token)
    participant W as Workflow API
    participant K as Keycloak
    participant C as Core Data
    S->>W: POST /secrets (client_secret), POST /config (keycloak_url)
    S->>W: POST /user-group-kinds (http spec, oauth2_client_credentials, processor)
    S->>W: publish definition, create workflow {tenant_name}
    W->>K: client credentials (secret + keycloak_url from tenant secret/config)
    W->>C: GET /tenants/rns:roc:iam::{tenant_name}/members?roles=true
    W->>W: processor keeps members with the UCP role
    S->>W: GET /jobs/{id} (resolved approvers), then cancel
```

```sh
CLIENT_SECRET=<secret of the UCP service client> \
VARIANT=custom-kind MODE=production TENANT_NAME=clsd-ucp \
KEYCLOAK_ISSUER=... KEYCLOAK_CLIENT_ID=rns:roc:portal \
WORKFLOW_BASE_URL=https://qa-horizon-workflow-api.r-local.net \
TENANT_RNS=rns:roc:iam::clsd-ucp go run .

# remove the kind, tenant secret and tenant config afterwards
CLEANUP=1 TENANT_RNS=rns:roc:iam::clsd-ucp TOKEN=... \
WORKFLOW_BASE_URL=https://qa-horizon-workflow-api.r-local.net go run .
```

Optional: `CLIENT_ID` (default `rns:roc:ucp-ops`), `KEYCLOAK_URL` (QA token
endpoint), `CORE_DATA_BASE_URL` (QA), `CUSTOM_KIND_ROLE` (default
`tenant-admin`), `CUSTOM_KIND_ID` (default `tenant-ucp-admins`).
`CLIENT_SECRET` is read from the environment only and is masked when the
secret is stored. The processor uses a single `&` for AND and puts filters
after a concrete path, the only forms the registration endpoint accepts; the
URL needs `?roles=true` or Core Data omits the roles.

## What each scenario actually runs

### `test` + `happy`

```mermaid
sequenceDiagram
    participant Op as Operator (script)
    participant WF as Horizon Workflow API

    Op->>WF: POST /tenants/{rns}/kinds (register Kind)
    Op->>WF: POST /tenants/{rns}/kinds/{kind}/definitions (create draft)
    Op->>WF: POST /tenants/{rns}/kinds/{kind}/test-workflows (create instance)
    Op->>WF: POST .../test-workflows/{id}/process
    Note over WF: advances Start -> tenant_admin_approval (no automated nodes to cross)
    Op->>WF: GET .../test-workflows/{id}
    WF-->>Op: execution_status: waiting, current step: tenant_admin_approval
    Op->>WF: POST .../test-workflows/{id}/triggers {"trigger":"approve"}
    Note over WF: tenant_admin_approval resolves users dynamically from {{ params.tenant_rns }}; transitions to network_team_approval
    Op->>WF: GET .../test-workflows/{id}
    WF-->>Op: execution_status: waiting, current step: network_team_approval
    Op->>WF: POST .../test-workflows/{id}/triggers {"trigger":"approve"}
    Note over WF: network_team_approval resolves users from fixed team clsd-ucp-team; transitions to end
    Op->>WF: GET .../test-workflows/{id}
    WF-->>Op: execution_status: completed, result: Successful
    Op->>WF: GET .../test-workflows/{id}/states (full history, for evidence)
```

No `process` call happens after a trigger — each trigger's transition is
immediate and complete on its own; `process` is only for advancing through
automated nodes (callback/adapter), which this definition has none of.

### `test` + `reject-layer1` (same shape for `cancel-layer1`, action swapped)

```mermaid
sequenceDiagram
    participant Op as Operator (script)
    participant WF as Horizon Workflow API

    Op->>WF: POST /tenants/{rns}/kinds (register Kind, idempotent)
    Op->>WF: POST /tenants/{rns}/kinds/{kind}/definitions (create draft)
    Op->>WF: POST /tenants/{rns}/kinds/{kind}/test-workflows (create instance)
    Op->>WF: POST .../test-workflows/{id}/process
    Op->>WF: GET .../test-workflows/{id}
    WF-->>Op: execution_status: waiting, current step: tenant_admin_approval
    Op->>WF: POST .../test-workflows/{id}/triggers {"trigger":"reject"}
    Note over WF: transitions straight to end with result Rejected -- network_team_approval step is never created
    Op->>WF: GET .../test-workflows/{id}
    WF-->>Op: execution_status: completed, result: Rejected
    Op->>WF: GET .../test-workflows/{id}/states (confirms only 3 states exist: Started, Submitting tenant admin approval, Completed)
```

### `production` + `happy`

```mermaid
sequenceDiagram
    participant Op as Operator (script)
    participant WF as Horizon Workflow API

    Op->>WF: POST /tenants/{rns}/kinds (register Kind)
    Op->>WF: POST /tenants/{rns}/kinds/{kind}/definitions (create draft)
    Op->>WF: PUT .../definitions/{number}/publish
    WF-->>Op: version_major
    Op->>WF: POST /tenants/{rns}/kinds/{kind}/versions/{version}/workflows (create production workflow)
    Note over WF: processes automatically -- no "process" call exists for production workflows
    Op->>WF: GET /jobs/{workflow_id}
    WF-->>Op: execution_status: waiting, current step: tenant_admin_approval
    Op->>WF: POST /jobs/{workflow_id}/approve
    Op->>WF: GET /jobs/{workflow_id}
    WF-->>Op: execution_status: waiting, current step: network_team_approval
    Op->>WF: POST /jobs/{workflow_id}/approve
    Op->>WF: GET /jobs/{workflow_id}
    WF-->>Op: execution_status: completed, result: Successful
    Op->>WF: GET /jobs?created_by={operator email}
    Op->>WF: GET /jobs?approver={operator email}
    Note over Op,WF: same /jobs resource + filters a UCP CLI would call directly, instead of the OneCloud Portal
```

The `production` mode is otherwise identical in shape to `test` — the only
differences are: a publish step happens first, workflow creation targets a
published `version` instead of a definition draft number, there's no
`process` call at all, and every action/status call targets `/jobs` instead
of the tenant-scoped `test-workflows` endpoints.

## Known gaps

- This script always acts as the logged-in operator for both approval
  layers (no impersonation), on the assumption that the operator is
  genuinely both the submitting tenant's admin and `clsd-ucp-team`'s admin.
  If that's not the case for a given run, impersonation support would need
  to be added.
- `production` mode creates a real, permanent Workflow Kind version and
  real workflow instances — these are not cleaned up by the script. The
  `custom-kind` variant cancels its workflow but leaves the kind, secret and
  config until `CLEANUP=1` is run.
- The `custom-kind` variant has been built and vetted but not yet re-run end
  to end from this code; the same flow was verified with throwaway calls (see
  `implementation.md`).
- The fingerprint variants publish the definition again on every run, and
  `fingerprint` publishes a further version for its cross-version case, so
  production mode accumulates Kind versions.
