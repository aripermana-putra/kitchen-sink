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
//     test-workflows endpoints. Requires no publishing and no real
//     tenant-admin/team-admin role propagation, but never appears in the
//     OneCloud Portal.
//   - MODE=production: publishes the definition draft and creates a real
//     production workflow, then drives it via the ABAC-scoped /jobs
//     resource — the same resource the OneCloud Portal's Approvals/My
//     Requests tabs are backed by, and the one a UCP-native interface
//     (e.g. a CLI) would call directly instead of requiring a Portal login.
//
// Three scenarios (SCENARIO env var), applicable to both modes:
//
//   - happy (default): approve layer 1, then approve layer 2.
//   - reject-layer1: reject at layer 1; layer 2 must never be reached.
//   - cancel-layer1: cancel at layer 1; layer 2 must never be reached.
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
//	MODE=production \
//	SCENARIO=happy \
//	go run .
//
// Optional:
//
//	KIND_ID (default: ucp_poc_vm_creation_approval)
//	TEAM_NAME (default: clsd-ucp-team)
package main

import (
	"bufio"
	"fmt"
	"os"
)

func main() {
	issuer := requireEnv("KEYCLOAK_ISSUER")
	clientID := requireEnv("KEYCLOAK_CLIENT_ID")
	workflowBaseURL := requireEnv("WORKFLOW_BASE_URL")
	tenantRNS := requireEnv("TENANT_RNS")

	kindID := envOr("KIND_ID", "ucp_poc_vm_creation_approval")
	teamName := envOr("TEAM_NAME", "clsd-ucp-team")
	mode := envOr("MODE", "test")
	scenario := envOr("SCENARIO", "happy")

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
	fmt.Printf("tenant_rns=%s kind_id=%s team=%s mode=%s scenario=%s\n\n", tenantRNS, kindID, teamName, mode, scenario)

	fmt.Println("--- Login ---")
	token, err := login(issuer, clientID)
	if err != nil {
		fatalf("login failed: %v", err)
	}
	email, err := emailFromToken(token)
	if err != nil {
		fatalf("read email from token: %v", err)
	}
	fmt.Printf("login ok (%s)\n", email)

	client := newWorkflowClient(workflowBaseURL, token)

	fmt.Println("\n--- Register Workflow kind (idempotent — ok if it already exists) ---")
	if err := client.createWorkflowKind(tenantRNS, kindID, "UCP PoC VM Creation Approval", "PoC for MCUCP-304: two-layer approval gate"); err != nil {
		fatalf("create workflow kind: %v", err)
	}

	fmt.Println("\n--- Create definition draft ---")
	definition := buildDefinition(teamName)
	draftNumber, err := client.createDefinitionDraft(tenantRNS, kindID, definition)
	if err != nil {
		fatalf("create definition draft: %v", err)
	}
	fmt.Printf("definition draft number: %d\n", draftNumber)

	params := map[string]any{
		"tenant_rns":    tenantRNS,
		"resource_type": "vm",
		"resource_id":   "poc-vm-001",
	}

	// act and status are mode-specific: test workflows use the
	// tenant-scoped test-workflows endpoints, production workflows use the
	// ABAC-scoped /jobs resource. Everything below this point is written
	// once against these two function values rather than duplicated per
	// mode.
	var (
		workflowID string
		act        func(action string) error
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
		fmt.Println("Production workflows process automatically — no equivalent of `process` for test workflows is needed.")

		act = func(action string) error {
			// map[string]any{} rather than nil: a nil map here is a
			// typed-nil interface value (non-nil to workflowClient.do's
			// nil check), which marshals to the JSON literal `null` — the
			// API rejects that with WF0001 "Field required", since it
			// wants an object body, even an empty one.
			return client.triggerJobAction(workflowID, action, map[string]any{})
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
			fmt.Println("(mirrors what a UCP-native interface would call instead of the OneCloud Portal)")
			if err := client.listJobs(map[string]string{"created_by": email}); err != nil {
				fmt.Printf("warning: could not list jobs by created_by: %v\n", err)
			}
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

		// process only advances a workflow through automated nodes
		// (callback, adapter) up to the next step or terminal node. Our
		// definition has no automated nodes at all — every hop after
		// "start" requires a human trigger — so process is only
		// meaningful once, right here, to move off "start". Calling it
		// again after a trigger returns WF0050 ("Workflow has an active
		// step") because there's nothing automated left to do; the
		// trigger itself already performed the transition.
		fmt.Println("\n--- Process test workflow (advance from start) ---")
		actionStatus, err := client.processTestWorkflow(tenantRNS, kindID, workflowID)
		if err != nil {
			fatalf("process test workflow: %v", err)
		}
		fmt.Printf("action status: %s (acknowledges the process request; fetching overall state next)\n", actionStatus)

		act = func(action string) error {
			return client.triggerTestWorkflow(tenantRNS, kindID, workflowID, action)
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

	wf := status()
	fmt.Printf("execution_status: %s\n", wf.ExecutionStatus)

	pause(fmt.Sprintf("About to trigger layer 1: %s. Press Enter to continue.", layer1Action))

	fmt.Printf("\n--- Trigger layer 1: %s ---\n", layer1Action)
	if err := act(layer1Action); err != nil {
		fatalf("trigger layer 1 %s: %v", layer1Action, err)
	}
	wf = status()
	fmt.Printf("execution_status: %s\n", wf.ExecutionStatus)

	if layer1Action != "approve" {
		fmt.Println("\n--- Final state ---")
		final()
		fmt.Printf("\nExpected result: execution_status completed, workflow at 'end' with an outcome matching layer 1's action (%s) — layer 2 (%s) must never have been exposed an action.\n", layer1Action, teamName)
		return
	}

	pause(fmt.Sprintf("Layer 1 approved. The workflow should now be sitting at network_team_approval. Press Enter to trigger layer 2 (%s) approval.", teamName))

	fmt.Println("\n--- Trigger layer 2: approve ---")
	if err := act("approve"); err != nil {
		fatalf("trigger layer 2 approve: %v", err)
	}
	wf = status()
	fmt.Printf("execution_status: %s\n", wf.ExecutionStatus)

	fmt.Println("\n--- Final state ---")
	final()
	fmt.Println("\nExpected result: execution_status completed, workflow at 'end' with outcome Successful.")
}

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
