// horizon-workflow-approval-gate-poc is a spike for MCUCP-304.
//
// It validates whether Horizon Workflow (OneCloud's WaaS platform) can back
// UCP's approval gate for provisioning requests, using a two-layer approval
// chain: first the submitting tenant's admins, then a fixed team
// (clsd-ucp-team, standing in for a real network-team approver group).
//
// Two modes:
//
//   - MODE=test (default): drives a test workflow via the tenant-scoped
//     test-workflows endpoints. Requires no publishing, but never appears in
//     the OneCloud Portal and sends no notifications.
//   - MODE=production: publishes the definition draft and creates a real
//     production workflow, then drives it via the ABAC-scoped /jobs
//     resource — the same resource the OneCloud Portal's Approvals/My
//     Requests tabs are backed by.
//
// Three scenarios (SCENARIO env var), applicable to both modes:
//
//   - happy (default): approve layer 1, then complete layer 2.
//   - reject-layer1: reject at layer 1; layer 2 must never be reached.
//   - cancel-layer1: cancel at layer 1; layer 2 must never be reached.
//
// Optional definition features (VARIANT env var, comma separated):
//
//   - vlan: layer 2 is skipped when the request carries params.vlan (set VLAN).
//   - service-role: layer 1 uses a service_resource_role user group
//     (UCP_SERVICE_RNS, UCP_RESOURCE_RNS, UCP_ROLE) instead of the tenant kind.
//   - user-groups: layer 1 uses one user group per email in APPROVER_EMAILS
//     (5 slots; "none" supplies an empty list), backed up by TEAM_NAME.
//   - email: an email callback node notifies APPROVER_EMAILS before layer 1.
//   - no-successful: the approve path ends in Completed, so no path sets the
//     Successful outcome.
//
// The resume callback (Part B) is deferred — see
// docs/projects/universal-control-plane-ucp/pocs/horizon-workflow-approval-gate/design.md
// in the UCP documentation repository for the full scope and rationale.
//
// Usage:
//
//	KEYCLOAK_ISSUER=https://qa2-accounts-onecloud.rakuten-it.com/auth/realms/roc \
//	KEYCLOAK_CLIENT_ID=rns:roc:portal \
//	WORKFLOW_BASE_URL=https://qa-horizon-workflow-api.r-local.net \
//	TENANT_RNS=rns:roc:iam::<your-tenant> \
//	MODE=production SCENARIO=happy VARIANT=vlan,email \
//	go run .
//
// Optional:
//
//	TOKEN (bearer token; skips the interactive login and the Keycloak settings)
//	KIND_ID (default: ucp_poc_vm_creation_approval)
//	TEAM_NAME (default: clsd-ucp-team)
//	VLAN (value supplied at creation; empty means not supplied)
//	RESOURCE_ID (default: poc-vm-001; appears in the custom email subject)
//	APPROVER_EMAILS (comma separated; default: the operator)
//	UCP_SERVICE_RNS (default: rns:roc:ucp), UCP_ROLE (default: test-admin),
//	UCP_RESOURCE_RNS (default: empty)
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	completeVLAN   = "poc-vlan-100"
	settleAttempts = 15
	settleInterval = time.Second
)

func main() {
	workflowBaseURL := requireEnv("WORKFLOW_BASE_URL")
	tenantRNS := requireEnv("TENANT_RNS")

	kindID := envOr("KIND_ID", "ucp_poc_vm_creation_approval")
	teamName := envOr("TEAM_NAME", "clsd-ucp-team")
	mode := envOr("MODE", "test")
	scenario := envOr("SCENARIO", "happy")
	variants := parseVariants(os.Getenv("VARIANT"))

	layer1Action, ok := map[string]string{
		"happy":         "approve",
		"reject-layer1": "reject",
		"cancel-layer1": "cancel",
	}[scenario]
	if !ok {
		fatalf("unknown SCENARIO %q (want happy, reject-layer1, or cancel-layer1)", scenario)
	}
	if mode != "test" && mode != "production" {
		fatalf("unknown MODE %q (want test or production)", mode)
	}

	fmt.Println("=== Horizon Workflow Approval Gate PoC (MCUCP-304) ===")
	fmt.Printf("tenant_rns=%s kind_id=%s team=%s mode=%s scenario=%s variant=%v\n\n", tenantRNS, kindID, teamName, mode, scenario, keys(variants))

	token := os.Getenv("TOKEN")
	if token == "" {
		fmt.Println("--- Login ---")
		var err error
		token, err = login(requireEnv("KEYCLOAK_ISSUER"), requireEnv("KEYCLOAK_CLIENT_ID"))
		if err != nil {
			fatalf("login failed: %v", err)
		}
	}
	email, err := emailFromToken(token)
	if err != nil {
		fatalf("read email from token: %v", err)
	}
	fmt.Printf("operator: %s\n", email)

	client := newWorkflowClient(workflowBaseURL, token)

	fmt.Println("\n--- Register Workflow kind (idempotent — ok if it already exists) ---")
	if err := client.createWorkflowKind(tenantRNS, kindID, "UCP PoC VM Creation Approval", "PoC for MCUCP-304: two-layer approval gate"); err != nil {
		fatalf("create workflow kind: %v", err)
	}

	layer1Groups := []any{tenantAdminGroup()}
	switch {
	case variants["service-role"]:
		layer1Groups = []any{serviceResourceRoleGroup(
			envOr("UCP_SERVICE_RNS", "rns:roc:ucp"),
			os.Getenv("UCP_RESOURCE_RNS"),
			envOr("UCP_ROLE", "test-admin"),
		)}
	case variants["user-groups"]:
		layer1Groups = emailUserGroups(teamGroup(teamName))
	}

	fmt.Println("\n--- Create definition draft ---")
	definition := buildDefinition(definitionOptions{
		teamName:           teamName,
		layer1Groups:       layer1Groups,
		conditionalNetwork: variants["vlan"],
		emailNotify:        variants["email"],
		noSuccessful:       variants["no-successful"],
	})
	draftNumber, err := client.createDefinitionDraft(tenantRNS, kindID, definition)
	if err != nil {
		fatalf("create definition draft: %v", err)
	}
	fmt.Printf("definition draft number: %d\n", draftNumber)

	params := map[string]any{
		"tenant_rns":      tenantRNS,
		"resource_type":   "vm",
		"resource_id":     envOr("RESOURCE_ID", "poc-vm-001"),
		"approver_emails": approverEmails(envOr("APPROVER_EMAILS", email)),
	}
	if v := os.Getenv("VLAN"); v != "" {
		params["vlan"] = v
	}

	// act and status are mode-specific: test workflows use the
	// tenant-scoped test-workflows endpoints, production workflows use the
	// ABAC-scoped /jobs resource. Everything below this point is written
	// once against these function values rather than duplicated per mode.
	var (
		workflowID string
		act        func(action string, payload map[string]any) error
		status     func() testWorkflowDetail
		final      func()
	)

	if mode == "production" {
		fmt.Println("\n--- Publish definition draft ---")
		version, err := client.publishDefinitionDraft(tenantRNS, kindID, draftNumber)
		if err != nil {
			fatalf("publish definition draft: %v", err)
		}
		fmt.Printf("published version: %d\n", version)

		fmt.Println("\n--- Create production workflow ---")
		workflowID, err = client.createProductionWorkflow(tenantRNS, kindID, version, params)
		if err != nil {
			fatalf("create production workflow: %v", err)
		}
		fmt.Printf("production workflow id: %s\n", workflowID)

		act = func(action string, payload map[string]any) error {
			// A non-nil empty map rather than nil: a nil map here is a
			// typed-nil interface value (non-nil to workflowClient.do's
			// nil check), which marshals to the JSON literal `null` — the
			// API rejects that with WF0001 "Field required".
			if payload == nil {
				payload = map[string]any{}
			}
			return client.triggerJobAction(workflowID, action, payload)
		}
		status = func() testWorkflowDetail {
			wf, err := client.getJob(workflowID)
			if err != nil {
				fatalf("get job: %v", err)
			}
			return wf
		}
		final = func() {
			fmt.Println("\n--- Visibility check: GET /jobs filtered to this operator ---")
			if err := client.listJobs(map[string]string{"approver": email}); err != nil {
				fmt.Printf("warning: could not list jobs by approver: %v\n", err)
			}
		}
	} else {
		fmt.Println("\n--- Create test workflow ---")
		workflowID, err = client.createTestWorkflow(tenantRNS, kindID, draftNumber, params)
		if err != nil {
			fatalf("create test workflow: %v", err)
		}
		fmt.Printf("test workflow id: %s\n", workflowID)

		// process advances a test workflow through automated nodes
		// (callbacks, adapters) up to the next step or terminal node. It is
		// only valid here, right after creation; calling it again while a
		// step is active returns WF0050.
		fmt.Println("\n--- Process test workflow (advance from start) ---")
		if _, err := client.processTestWorkflow(tenantRNS, kindID, workflowID); err != nil {
			fatalf("process test workflow: %v", err)
		}

		act = func(action string, payload map[string]any) error {
			return client.triggerTestWorkflow(tenantRNS, kindID, workflowID, action, payload)
		}
		status = func() testWorkflowDetail {
			wf, err := client.getTestWorkflow(tenantRNS, kindID, workflowID)
			if err != nil {
				fatalf("get test workflow: %v", err)
			}
			return wf
		}
		final = func() {
			if err := client.listTestWorkflowStates(tenantRNS, kindID, workflowID); err != nil {
				fmt.Printf("warning: could not list states: %v\n", err)
			}
		}
	}

	wf := settled(status)
	fmt.Printf("execution_status: %s\n", wf.ExecutionStatus)

	fmt.Printf("\n--- Trigger layer 1: %s ---\n", layer1Action)
	if err := act(layer1Action, nil); err != nil {
		fatalf("trigger layer 1 %s: %v", layer1Action, err)
	}
	wf = settled(status)
	fmt.Printf("execution_status: %s\n", wf.ExecutionStatus)

	if layer1Action != "approve" || wf.ExecutionStatus == "completed" {
		fmt.Println("\n--- Final state ---")
		final()
		fmt.Println("\nWorkflow finished after layer 1 (layer 1 did not approve, or layer 2 was skipped).")
		return
	}

	layer2Action, layer2Payload := "approve", map[string]any(nil)
	if variants["vlan"] {
		layer2Action, layer2Payload = "complete", map[string]any{"vlan": completeVLAN}
	}
	fmt.Printf("\n--- Trigger layer 2: %s ---\n", layer2Action)
	if err := act(layer2Action, layer2Payload); err != nil {
		fatalf("trigger layer 2 %s: %v", layer2Action, err)
	}
	wf = settled(status)
	fmt.Printf("execution_status: %s\n", wf.ExecutionStatus)

	fmt.Println("\n--- Final state ---")
	final()
}

// settled polls status until the workflow leaves "processing": transitions
// are applied asynchronously, so a read right after an action can be stale.
func settled(status func() testWorkflowDetail) testWorkflowDetail {
	wf := status()
	for i := 0; i < settleAttempts && wf.ExecutionStatus == "processing"; i++ {
		time.Sleep(settleInterval)
		wf = status()
	}
	return wf
}

func parseVariants(raw string) map[string]bool {
	out := map[string]bool{}
	for _, v := range strings.Split(raw, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out[v] = true
		}
	}
	return out
}

func keys(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

// approverEmails splits a comma separated list; the literal "none" yields an
// empty list, for exercising the empty-approver case.
func approverEmails(raw string) []any {
	out := []any{}
	if raw == "none" {
		return out
	}
	for _, e := range strings.Split(raw, ",") {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// pause is kept for interactive runs that want a checkpoint.
func pause(message string) {
	fmt.Printf("\n%s\n", message)
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fatalf("missing required environment variable %s", key)
	}
	return v
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
