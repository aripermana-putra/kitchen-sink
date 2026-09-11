# Runbook — Crossplane XRD Versioned Schema and Provisioning PoC

Operational instructions for running this PoC locally. For what this PoC proves, see
`${DOCS_ROOT}/pocs/crossplane-xrd-versioned-schema-provisioning/`.

## 1. One-time environment setup

### 1.1 Start a local Crossplane cluster (Colima + k3s)

```bash
colima start ucp-crossplane --cpu 2 --memory 4 --disk 20 --kubernetes
```

`--kubernetes` gives Colima's own embedded k3s. This merges a `colima-ucp-crossplane` context into
your default `~/.kube/config`. Extract it into this PoC's own kubeconfig file so it never depends
on your default context being selected:

```bash
kubectl config view --context=colima-ucp-crossplane --minify --flatten > kubeconfig-crossplane.yaml
```

`kubeconfig-crossplane.yaml` is `.gitignore`d — it embeds real client cert/key material for the VM.
Never commit it.

### 1.2 Install Crossplane

```bash
export KUBECONFIG=$(pwd)/kubeconfig-crossplane.yaml

helm repo add crossplane-stable https://charts.crossplane.io/stable
helm repo update
helm install crossplane crossplane-stable/crossplane \
  --namespace crossplane-system --create-namespace

kubectl wait --for=condition=Available deployment/crossplane -n crossplane-system --timeout=120s
```

### 1.3 Apply the PoC's dummy XRDs

```bash
kubectl apply -f crossplane/xrd/
kubectl get compositeresourcedefinitions
```

Every XRD here carries `catalog.ucp.io/enabled: "true"` — that's the label the poller's `LIST`
filters on (`internal/xrdclient/xrdclient.go`'s `xrdGVR` list call). An XRD without that label is
invisible to the PoC entirely.

### 1.4 Start the apiserver

```bash
go run ./cmd/apiserver
```

Env vars (all optional, defaults shown):

| Var | Default | Purpose |
|---|---|---|
| `KUBECONFIG_PATH` | `kubeconfig-crossplane.yaml` | kubeconfig for the Crossplane cluster |
| `LISTEN_ADDR` | `:8081` | HTTP listen address |
| `POLL_INTERVAL` | `10s` | how often the poller re-lists XRDs |
| `POLL_TIMEOUT` | `5s` | per-poll `LIST` call timeout |

Confirm it's up:

```bash
curl -s localhost:8081/health/ready -w '\n%{http_code}\n'
```

`200` once the first poll has succeeded; `503` before that or if the last poll failed (cache is
served stale on failure, per `internal/catalog/poller.go`).

## 2. Adding or modifying an XRD, then testing it via the API

### 2.1 Author or edit the XRD YAML

Every catalog-eligible XRD needs, at minimum:

```yaml
apiVersion: apiextensions.crossplane.io/v2
kind: CompositeResourceDefinition
metadata:
  name: x<plural>.<service-id>.catalog.ucp.io
  labels:
    catalog.ucp.io/enabled: "true"
  annotations:
    catalog.ucp.io/service-id: <service-id>
    catalog.ucp.io/name: <display name>
    catalog.ucp.io/provider: GCP | ROC
    catalog.ucp.io/category: compute | database | ...
    catalog.ucp.io/description: <description>
    catalog.ucp.io/roc-subscription-name: ""
    catalog.ucp.io/resource-graph: '[{"name":"<item>","type":"<Type>","dependsOn":["<other-item>"]}]'
    catalog.ucp.io/supported-regions: "region1,region2"
spec:
  group: <service-id>.catalog.ucp.io
  names:
    kind: X<Kind>
    plural: x<plural>
  scope: Namespaced
  versions:
    - name: v1alpha1
      served: true
      referenceable: true   # exactly one version at a time
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                parameters:
                  type: object
                  properties:
                    shared:      # reserved group — cross-item parameters
                      type: object
                      properties: {...}
                    <item-name>: # matches a resource-graph entry's "name"
                      type: object
                      properties: {...}
```

Rules that matter for this PoC's derivation logic (`deriveEntrySchema` in `internal/xrdclient/xrdclient.go`):

- `served: true` is what makes a version appear in `EntrySchema.Versions` at all — multiple
  versions can be `served: true` simultaneously.
- Exactly one version must be `referenceable: true` — that becomes `EntrySchema.StorageVersion`,
  the only version `describe`/`template` ever render. Marking two (or zero) breaks Kubernetes'
  own CRD validation on apply, not just this PoC.
- There is no literal `storage:` field on an XRD's own `spec.versions[]` entries — Crossplane maps
  `referenceable: true` onto the generated CRD's `spec.versions[*].storage` field itself.
- `spec.group`, `spec.names.kind`, `spec.names.plural` are read once per XRD (not per version) into
  `EntrySchema.Group`/`Kind`/`Resource` — these drive the `apiVersion`/`kind`/`GroupVersionResource`
  that `provision` applies the XR against. Getting `plural` wrong here breaks `provision`, not
  `describe`/`template` (which never touch these three fields).
- A `None`-safe change (new optional field, new default, widened constraint) → bump to a new
  version name inside the same XRD, keep the old version `served: true` forever. A breaking change
  → new XRD (new `service-id`), not a new version — Crossplane's only supported conversion strategy
  between versions is `None` (no transformation).

### 2.2 Apply it

```bash
kubectl apply -f crossplane/xrd/<your-file>.xrd.yaml
kubectl get crd <plural>.<service-id>.catalog.ucp.io \
  -o jsonpath='{.spec.versions[*].name}{"\n"}{.spec.versions[*].storage}{"\n"}'
```

The second line confirms which version Kubernetes marked as the storage version (`true`) —
should match whichever XRD version you set `referenceable: true` on.

To see the full, current-in-cluster value of the XRD itself (not the generated CRD) after an
apply:

```bash
# full YAML as stored in the cluster right now
kubectl get compositeresourcedefinition <plural>.<service-id>.catalog.ucp.io -o yaml
# "xrd" works as a shorthand alias for compositeresourcedefinition
kubectl get xrd <plural>.<service-id>.catalog.ucp.io -o yaml

# just the versions block (name/served/referenceable), without the rest of the schema
kubectl get xrd <plural>.<service-id>.catalog.ucp.io \
  -o jsonpath='{range .spec.versions[*]}{.name}{"\tserved="}{.served}{"\treferenceable="}{.referenceable}{"\n"}{end}'
```

The `referenceable` field lives on the XRD object itself; `storage` (checked above) lives on the
*generated* CRD — two different objects, only one of which you author directly.

If a schema change to an *existing* version doesn't seem to take effect (rare, but happens if the
Crossplane controller cached the old CRD shape), restart the Crossplane pod:

```bash
kubectl delete pod -n crossplane-system -l app=crossplane
kubectl wait --for=condition=Ready pod -n crossplane-system -l app=crossplane --timeout=60s
```

### 2.3 Wait for the PoC's poller to pick it up

No restart of the `apiserver` process is needed — the poller re-`LIST`s on its own
`POLL_INTERVAL` (default `10s`). Wait a poll cycle, then check readiness:

```bash
sleep 12
curl -s localhost:8081/health/ready -w '\n%{http_code}\n'
```

### 2.4 Test `describe`

```bash
curl -s localhost:8081/catalog/<service-id> | jq .
```

Confirm `storageVersion` matches the version you marked `referenceable: true`, and
`parametersSchema` only shows that version's fields — an older `served: true` version's
schema must never appear here.

### 2.5 Test `template`

```bash
curl -s localhost:8081/catalog/<service-id>/template
```

Renders a fillable YAML stamped with `schemaVersion: <storageVersion>`, formatted to match
MCUCP-145's worked `--generate-template` example: `shared` first (if present), then one group per
resource-graph item in graph order; each field's comment shows `(required|optional)`, its
description, `(default: <value>)` if the schema has one, and a second `#   example: <value>` line
if the schema has one; every value is emitted blank (`""`) regardless of default. Fill in the
blanks for the next step.

### 2.6 Test `provision`

```bash
curl -s -X POST localhost:8081/catalog/<service-id>/provision \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "<xr-name>",
    "schemaVersion": "<version>",
    "parameters": { "shared": {...}, "<item-name>": {...} }
  }' -w '\n%{http_code}\n'
```

`schemaVersion` can be any currently `served: true` version, not just the current storage
version — that's the core claim this PoC exists to prove. `201 Created` on success; `400` with a
JSON Schema validation error if `parameters` doesn't match that version's schema; `404` if
`schemaVersion` isn't a version this XRD currently serves.

Confirm the XR was actually applied:

```bash
kubectl get x<plural> -n default
kubectl get x<plural> <xr-name> -n default -o yaml
```

Expect `SYNCED: False` with reason `cannot select Composition: no compatible Compositions found`
— this PoC never binds a `Composition`, by design (no real provisioning backend).

### 2.7 Test an old version still works after a newer one becomes current

Add a new version to the same XRD, mark it `referenceable: true`, demote the previous one to
`referenceable: false`, re-apply (2.2), wait a poll (2.3), then repeat 2.6 with
`schemaVersion` set to the *old* version. It should still return `201 Created` — Kubernetes'
built-in multi-version storage/serving round-trip, not anything this PoC's code does specially.

### 2.8 Checking whether a small edit is safe to make in place, without bumping a version

MCUCP-145's TRD rule is that a served version's schema is never edited in place — any change is
either a new version or a new XRD (2.1's `None`-safe test). Not every edit carries the same risk,
though, and it's worth checking rather than assuming:

- **`description`-only edits are always safe to edit in place.** Neither Kubernetes' structural
  schema validation nor this PoC's JSON Schema validator (`santhosh-tekuri/jsonschema`) uses
  `description` for validation — it's inert documentation. Editing it in place can't change what
  a payload validates against.
- **`minimum`/`maximum`/`enum`/`required` edits can change validation outcomes, and direction
  matters:**
  - *Widening* (lower a `minimum`, raise a `maximum`, add an `enum` option, drop a `required`
    entry) never invalidates anything that validated before — `None`-safe, per 2.1's test.
  - *Narrowing* (raise a `minimum`, lower a `maximum`, remove an `enum` option, add a new
    `required` entry) can turn a previously-valid payload invalid — not `None`-safe, and per the
    TRD's rule should be a new XRD, not even a new version of the same one.

To check a specific edit rather than guess:

```bash
# 1. Snapshot the version's schema before your edit
kubectl get crd <plural>.<service-id>.catalog.ucp.io \
  -o jsonpath='{.spec.versions[?(@.name=="<version>")].schema.openAPIV3Schema}' > /tmp/before.json

# 2. See exactly what Kubernetes will change, before committing
kubectl diff -f crossplane/xrd/<your-file>.xrd.yaml

# 3. Apply
kubectl apply -f crossplane/xrd/<your-file>.xrd.yaml

# 4. Snapshot after, diff the two
kubectl get crd <plural>.<service-id>.catalog.ucp.io \
  -o jsonpath='{.spec.versions[?(@.name=="<version>")].schema.openAPIV3Schema}' > /tmp/after.json
diff <(jq . /tmp/before.json) <(jq . /tmp/after.json)

# 5. The real test — does an already-applied XR of that version still validate
#    under the now-current schema?
kubectl get <kind> <existing-xr-name> -n default -o yaml | kubectl apply --dry-run=server -f -
```

Step 5 is the one that actually answers the question — a server-side dry-run validate of a real,
already-existing object against whatever schema is live right now. A failure there means the edit
broke something for existing data and should have been a new version (or new XRD), no matter how
small it looked in the YAML diff. You can run the same check against a `/template` output
generated before the edit — POST it to `/provision` again afterward; a `400` there means the same
thing step 5 does.

## 3. Cleanup

```bash
kubectl delete -f crossplane/xrd/<your-file>.xrd.yaml   # removes XRD + its XRs
helm uninstall crossplane -n crossplane-system            # if tearing down Crossplane entirely
colima stop ucp-crossplane                                 # stop the VM
colima delete ucp-crossplane                                # delete the VM entirely
```
