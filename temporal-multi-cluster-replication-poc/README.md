# Temporal Multi-Cluster Replication PoC

Two independent Temporal clusters, MCR-registered with each other, run through: namespace
replication, workflow history replication, a manual flip while both are healthy, a forced
failover with one cluster actually stopped, rejoin after that outage, the internal
namespace-handover (graceful drain) workflow, an attempt (unsuccessful) to force real
replication lag via cluster metadata flags, and — as a separate, not-a-real-UCP-migration-path
scenario for team reference — adding a second cluster to an already-populated single-cluster
deployment and backfilling its pre-existing history with `force-replication`.

Full narrative and results: see the UCP docs repo,
`docs/projects/universal-control-plane-ucp/pocs/temporal-multi-cluster-replication/`
(`design.md`, `poc-report.md`, `implementation.md`). This file is the runnable playbook —
copy-pasteable commands, in order, with what to check at each step.

## Prerequisites

- Docker, `temporal` CLI on the host.
- Colima, with a dedicated profile for Cluster B:
  ```shell
  colima start --profile temporal-mcr --cpu 2 --memory 3
  ```
- Confirm which Docker context is currently active before bringing anything up —
  `docker compose up -d` attaches to whatever's active, not necessarily the bare host:
  ```shell
  docker context ls
  ```

## 1. Bring up both clusters

```shell
# Cluster A — bring up in whichever context is active (confirmed above)
cd cluster-a && docker compose up -d && cd ..

# Cluster B — explicitly target the temporal-mcr Colima context
docker context use colima-temporal-mcr   # or: DOCKER_HOST=$(colima status --profile temporal-mcr -j | jq -r .docker_socket_url)
cd cluster-b && docker compose up -d && cd ..
docker context use default               # switch back
```

Check both are healthy:

```shell
temporal --address 127.0.0.1:7233 operator cluster health   # cluster-a
temporal --address 127.0.0.1:8233 operator cluster health   # cluster-b
```

Both should report `SERVING`. If either fails to start, check for the archival config error
first (`config validation error: invalid history archival config`) — both
`config_template.yaml` files already carry the fix (full `archival.provider.filestore`
block), but if you're modifying them, don't drop it.

## 2. Reachability check (do this before registering anything)

```shell
# Host -> Cluster B, through Colima's port-forward
curl -sv telnet://127.0.0.1:8233 2>&1 | head -5

# Cluster B's container -> Cluster A, through Colima's guest gateway
docker --context colima-temporal-mcr exec -it cluster-b-temporal sh -c "nc -zv 192.168.5.2 7233"
```

`192.168.5.2` is Colima's `vz`-driver default gateway back to the host — confirm with
`colima ssh --profile temporal-mcr -- ip route show default` if it differs on your machine or
driver (`qemu` may expose a different address).

## 3. Register the clusters with each other

Each side registers the address *it* can reach — these are not the same string:

```shell
# cluster-a -> cluster-b: from inside cluster-a's own container, 127.0.0.1 is its own loopback,
# not the host's — use host.docker.internal
temporal --address 127.0.0.1:7233 operator cluster upsert \
  --frontend-address "host.docker.internal:8233" --enable-connection --enable-replication

# cluster-b -> cluster-a: from inside cluster-b's container, reach the host via Colima's gateway
temporal --address 127.0.0.1:8233 operator cluster upsert \
  --frontend-address "192.168.5.2:7233" --enable-connection --enable-replication
```

Verify both sides see each other:

```shell
temporal --address 127.0.0.1:7233 operator cluster list
temporal --address 127.0.0.1:8233 operator cluster list
```

(`IsReplicationEnabled` may display `false` here even with `--enable-replication` passed —
that's a display quirk in this CLI/server version; it doesn't block replication, confirmed by
step 5 below actually working.)

## 4. Create a global namespace and check it replicates

```shell
temporal --address 127.0.0.1:7233 operator namespace create \
  --global-namespace true --cluster cluster-a --cluster cluster-b mcr-poc

# Should appear on cluster-b within a few seconds, same FailoverVersion
temporal --address 127.0.0.1:8233 operator namespace describe mcr-poc
```

## 5. Start a workflow on A, check its history replicates to B

No worker needed — only the `WorkflowExecutionStarted` event has to exist and replicate:

```shell
temporal --address 127.0.0.1:7233 workflow start \
  --namespace mcr-poc --type SomeWorkflow --task-queue poc-tq \
  --workflow-id mcr-poc-wf-1 --execution-timeout 3600

# Poll from cluster-b until it shows up (a few seconds)
temporal --address 127.0.0.1:8233 workflow show --namespace mcr-poc --workflow-id mcr-poc-wf-1
```

## 6. Manual flip while Cluster A is still healthy

```shell
temporal --address 127.0.0.1:7233 operator namespace update \
  --namespace mcr-poc --active-cluster cluster-b

# Check immediately, repeatedly — the originating side (A) has zero lag on its own belief
temporal --address 127.0.0.1:7233 operator namespace describe mcr-poc
temporal --address 127.0.0.1:8233 operator namespace describe mcr-poc

# Confirm B now accepts new starts for this namespace
temporal --address 127.0.0.1:8233 workflow start \
  --namespace mcr-poc --type SomeWorkflow --task-queue poc-tq \
  --workflow-id mcr-poc-wf-2 --execution-timeout 3600
```

## 7. Forced failover — stop Cluster A, fail over from B

```shell
# Flip back to A first, confirm it took
temporal --address 127.0.0.1:7233 operator namespace update \
  --namespace mcr-poc --active-cluster cluster-a

# Actually stop cluster-a
cd cluster-a && docker compose stop && cd ..

# Confirm it's genuinely unreachable, not just flipped
temporal --address 127.0.0.1:7233 operator cluster health   # should fail/refuse

# Flip from B — the only reachable cluster now
temporal --address 127.0.0.1:8233 operator namespace update \
  --namespace mcr-poc --active-cluster cluster-b

# New workflow, standalone on B, with A fully down
temporal --address 127.0.0.1:8233 workflow start \
  --namespace mcr-poc --type SomeWorkflow --task-queue poc-tq \
  --workflow-id mcr-poc-wf-3 --execution-timeout 3600
```

## 8. Rejoin — restart A, watch it self-correct

```shell
cd cluster-a && docker compose start && cd ..

# No manual step beyond starting it. Poll until it matches B's state —
# expect this within ~10s (bounded by container boot time, not replication lag)
watch -n1 'temporal --address 127.0.0.1:7233 operator namespace describe mcr-poc'

# Confirm the workflow B created while A was down is now visible from A too
temporal --address 127.0.0.1:7233 workflow show --namespace mcr-poc --workflow-id mcr-poc-wf-3
```

## 9. Namespace handover (the internal graceful-drain workflow)

Not a documented CLI verb — it's an internal Temporal workflow (`namespace-handover`),
registered on Temporal's own `temporal-system` namespace, `default-worker-tq` task queue.
Invoked directly as a plain workflow start:

```shell
# Reset to a clean starting point first
temporal --address 127.0.0.1:7233 operator namespace update \
  --namespace mcr-poc --active-cluster cluster-a

temporal --address 127.0.0.1:7233 workflow start \
  --namespace temporal-system \
  --task-queue default-worker-tq \
  --type namespace-handover \
  --workflow-id mcr-poc-handover-1 \
  --input '{"Namespace":"mcr-poc","RemoteCluster":"cluster-b","AllowedLaggingSeconds":10,"AllowedLaggingTasks":0,"HandoverTimeoutSeconds":30}'

# Watch it complete (took ~2s in testing) and inspect its own history for the step sequence
temporal --address 127.0.0.1:7233 workflow show \
  --namespace temporal-system --workflow-id mcr-poc-handover-1

# Confirm the flip happened and state was reset, not left stuck in HANDOVER
temporal --address 127.0.0.1:7233 operator namespace describe mcr-poc
```

If this is rejected with a caller/permission error rather than a syntax error, that's the
finding — it means self-hosted operators need a different, still-undocumented path to trigger
handover, not that the workflow doesn't exist. If `namespace-handover` (v1) is rejected for
version reasons specifically, try `namespace-handover-v2` with the same input before
concluding it's blocked.

**To actually observe a rejected request during the `HANDOVER` window** (not confirmed in
this PoC — the window was too short with zero pre-existing lag): inject artificial
replication backlog before starting handover, or poll sub-second, and attempt a
`workflow start` against `mcr-poc` while `operator namespace describe` shows
`ReplicationConfig.State: Handover`.

## 10. Attempting to force real replication lag (tested — does not work this way)

Tried disabling the connection to build a real backlog before handover, so the `HANDOVER`
window would last long enough to actually catch it with a test request. Confirmed this
doesn't work — recorded here so nobody re-tries the same dead end:

```shell
temporal --address 127.0.0.1:8233 operator cluster upsert \
  --frontend-address "192.168.5.2:7233" --enable-connection=false --enable-replication=false
temporal --address 127.0.0.1:7233 operator cluster upsert \
  --frontend-address "host.docker.internal:8233" --enable-connection=false --enable-replication=false

# Confirm the flags actually registered as false
temporal --address 127.0.0.1:7233 operator cluster list
temporal --address 127.0.0.1:8233 operator cluster list

# Start a workflow anyway — it still replicates within the usual few seconds, even after
# waiting 15s. The already-established stream is not gated by these flags.
temporal --address 127.0.0.1:7233 workflow start \
  --namespace mcr-poc --type SomeWorkflow --task-queue poc-tq \
  --workflow-id mcr-poc-lag-test-1 --execution-timeout 3600
sleep 15
temporal --address 127.0.0.1:8233 workflow show --namespace mcr-poc --workflow-id mcr-poc-lag-test-1

# Restore before moving on
temporal --address 127.0.0.1:8233 operator cluster upsert \
  --frontend-address "192.168.5.2:7233" --enable-connection --enable-replication
temporal --address 127.0.0.1:7233 operator cluster upsert \
  --frontend-address "host.docker.internal:8233" --enable-connection --enable-replication
```

A real test of live rejection during `HANDOVER` would need actual network-level
partitioning between the clusters (e.g. a firewall rule blocking the port), not this flag —
not attempted here.

## 11. Single-cluster with pre-existing history (not a real UCP migration path — see below)

UCP's actual rollout creates both clusters from the start; this is purely "what would happen
if we ever needed to add a cluster later," tested for team reference. Uses a separate
namespace so it doesn't interfere with anything above.

```shell
# Local (non-global) namespace on cluster-a only
temporal --address 127.0.0.1:7233 operator namespace create mcr-poc-migration

for i in 1 2 3 4 5; do
  temporal --address 127.0.0.1:7233 workflow start \
    --namespace mcr-poc-migration --type SomeWorkflow --task-queue poc-tq \
    --workflow-id "migration-pre-$i" --execution-timeout 3600
done
```

## 12. Introduce Cluster B and promote the namespace

The CLI flag is `--promote-global`, not `--promote-namespace` (the source-level field name)
— and it cannot be combined with `--cluster` in the same call; two separate calls are
required:

```shell
temporal --address 127.0.0.1:7233 operator namespace update \
  --namespace mcr-poc-migration --promote-global

temporal --address 127.0.0.1:7233 operator namespace update \
  --namespace mcr-poc-migration --cluster cluster-a --cluster cluster-b

# Confirm one NEW workflow replicates normally post-promotion
temporal --address 127.0.0.1:7233 workflow start \
  --namespace mcr-poc-migration --type SomeWorkflow --task-queue poc-tq \
  --workflow-id migration-post-1 --execution-timeout 3600
temporal --address 127.0.0.1:8233 workflow show --namespace mcr-poc-migration --workflow-id migration-post-1
```

**Do not trust `workflow list`/`workflow show` against cluster-b to check whether the
pre-existing `migration-pre-*` workflows are actually absent.** Both clusters have
`dcRedirectionPolicy: all-apis-forwarding` — a query against cluster-b silently forwards to
cluster-a and gives a false positive. Check cluster-b's own database directly instead:

```shell
docker --context colima-temporal-mcr exec -it cluster-b-postgresql \
  psql -U temporal -d temporal -c \
  "SELECT workflow_id, run_id FROM current_executions WHERE namespace_id = '<namespace-id>';"
```

(Get `<namespace-id>` from `temporal --address 127.0.0.1:7233 operator namespace describe mcr-poc-migration`.)
Should show only `migration-post-1` at this point — none of the `migration-pre-*` rows.

## 13. Backfill with force-replication

```shell
temporal --address 127.0.0.1:7233 workflow start \
  --namespace temporal-system \
  --task-queue default-worker-tq \
  --type force-replication \
  --workflow-id mcr-poc-force-replication-1 \
  --input '{"Namespace":"mcr-poc-migration","Query":"WorkflowId != \"\"","TargetClusterName":"cluster-b"}'

# Watch it complete (~11s for 5 workflows in testing)
temporal --address 127.0.0.1:7233 workflow show \
  --namespace temporal-system --workflow-id mcr-poc-force-replication-1

# Re-run the direct-Postgres check from step 12 — all migration-pre-* rows should now be present
```

`Query` uses the same SQL-like list-filter syntax as `workflow list --query` — confirmed
against that command first, since an empty string does not reliably mean "everything." If v1
has issues, try `force-replication-v2` with the same input before concluding it's blocked.

**If this is ever run against real production data, not just this PoC's 5 test workflows:**
tune `OverallRps`/`ConcurrentActivityCount` in the input down deliberately, and monitor the
live cluster's own latency during the run — it reads from and writes replication tasks
against the same database live traffic is using. Not load-tested at real scale here. See the
research doc's side note on this scenario for the fuller checklist.

## Cleanup

```shell
cd cluster-a && docker compose down -v && cd ..
docker context use colima-temporal-mcr
cd cluster-b && docker compose down -v && cd ..
docker context use default
colima stop --profile temporal-mcr   # or: colima delete --profile temporal-mcr
```
