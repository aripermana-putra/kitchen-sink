package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// workflowClient is a thin wrapper around the Horizon Workflow API
// (workflow-sapi/2) used by this PoC. It intentionally does not try to be a
// complete client — only the endpoints exercised by the two-layer approval
// scenario described in
// docs/projects/universal-control-plane-ucp/pocs/horizon-workflow-approval-gate/design.md
// are implemented.
//
// Endpoint paths and request/response body shapes are confirmed against the
// live OpenAPI spec at https://qa-horizon-workflow-api.r-local.net/openapi.json
// (note: test workflow paths use hyphens — "test-workflows" — not
// underscores, despite how the Tutorial/Reference prose renders them).
type workflowClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func newWorkflowClient(baseURL, token string) *workflowClient {
	return &workflowClient{baseURL: baseURL, token: token, http: http.DefaultClient}
}

// do sends a JSON request and decodes a JSON response into out (if non-nil).
// It always prints the request and response for visibility, since this is a
// PoC script meant to be read while it runs, not a black box.
func (c *workflowClient) do(method, path string, body any, out any) (int, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.MarshalIndent(body, "", "  ")
		if err != nil {
			return 0, fmt.Errorf("marshal request body: %w", err)
		}
		fmt.Printf("--> %s %s\n%s\n", method, path, string(b))
		reqBody = bytes.NewReader(b)
	} else {
		fmt.Printf("--> %s %s\n", method, path)
	}

	req, err := http.NewRequest(method, c.baseURL+path, reqBody)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, fmt.Errorf("read response: %w", err)
	}
	fmt.Printf("<-- %d\n%s\n\n", resp.StatusCode, string(respBody))

	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("%s %s returned %d: %s", method, path, resp.StatusCode, string(respBody))
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode response: %w", err)
		}
	}
	return resp.StatusCode, nil
}

// createWorkflowKind registers a new Workflow kind under tenantRNS.
// POST /workflow-sapi/2/tenants/{tenant_rns}/kinds
func (c *workflowClient) createWorkflowKind(tenantRNS, kindID, name, description string) error {
	body := map[string]string{
		"id":          kindID,
		"name":        name,
		"description": description,
	}
	path := fmt.Sprintf("/workflow-sapi/2/tenants/%s/kinds", tenantRNS)
	status, err := c.do(http.MethodPost, path, body, nil)
	if status == http.StatusConflict {
		fmt.Println("kind already exists — continuing (id/name are globally unique, so a prior run likely created it)")
		return nil
	}
	return err
}

// definitionDraftResponse captures the fields needed from a create/update
// definition draft response.
type definitionDraftResponse struct {
	Number int `json:"number"`
}

// createDefinitionDraft creates a new definition draft for kindID.
// POST /workflow-sapi/2/tenants/{tenant_rns}/kinds/{kind}/definitions
func (c *workflowClient) createDefinitionDraft(tenantRNS, kindID string, definition map[string]any) (int, error) {
	path := fmt.Sprintf("/workflow-sapi/2/tenants/%s/kinds/%s/definitions", tenantRNS, kindID)
	var resp definitionDraftResponse
	_, err := c.do(http.MethodPost, path, map[string]any{"definition": definition}, &resp)
	if err != nil {
		return 0, err
	}
	return resp.Number, nil
}

// workflowActionResponse is returned by endpoints that trigger an
// asynchronous action on a workflow (create, process, trigger) —
// "WorkflowActionResponse" in the OpenAPI spec. status here is an
// action-acknowledgement status (e.g. "pending"), not the workflow's
// overall completion state — that's execution_status on
// testWorkflowDetail, only returned by GET.
type workflowActionResponse struct {
	WorkflowID string `json:"workflow_id"`
	Status     string `json:"status"`
}

// testWorkflowDetail captures the fields this PoC reads from GET
// .../test-workflows/{id}. The live response has more fields (steps,
// triggers, user_triggers, parameters, ...) useful for manual inspection —
// those are visible in the raw response that workflowClient.do already
// prints, just not decoded into this struct.
type testWorkflowDetail struct {
	ID              string `json:"id"`
	ExecutionStatus string `json:"execution_status"`
}

// createTestWorkflow creates a test workflow instance against the given
// definition draft number, with the supplied payload parameters.
// POST /workflow-sapi/2/tenants/{tenant_rns}/kinds/{kind}/test-workflows
func (c *workflowClient) createTestWorkflow(tenantRNS, kindID string, draftNumber int, params map[string]any) (string, error) {
	body := map[string]any{
		"definition_draft_number": draftNumber,
		"payload":                 params,
	}
	path := fmt.Sprintf("/workflow-sapi/2/tenants/%s/kinds/%s/test-workflows", tenantRNS, kindID)
	var resp workflowActionResponse
	_, err := c.do(http.MethodPost, path, body, &resp)
	return resp.WorkflowID, err
}

// processTestWorkflow advances a test workflow through automated nodes
// (callbacks, adapters) until it reaches a step node or a terminal node.
// Returns the action-acknowledgement status (e.g. "pending"), not the
// workflow's overall completion state — call getTestWorkflow for that.
// POST /workflow-sapi/2/tenants/{tenant_rns}/kinds/{kind}/test-workflows/{workflow_id}/process
func (c *workflowClient) processTestWorkflow(tenantRNS, kindID, workflowID string) (string, error) {
	path := fmt.Sprintf("/workflow-sapi/2/tenants/%s/kinds/%s/test-workflows/%s/process", tenantRNS, kindID, workflowID)
	var resp workflowActionResponse
	_, err := c.do(http.MethodPost, path, nil, &resp)
	return resp.Status, err
}

// getTestWorkflow reads the current overall state of a test workflow,
// including its execution_status (e.g. "processing", "completed").
// GET /workflow-sapi/2/tenants/{tenant_rns}/kinds/{kind}/test-workflows/{workflow_id}
func (c *workflowClient) getTestWorkflow(tenantRNS, kindID, workflowID string) (testWorkflowDetail, error) {
	path := fmt.Sprintf("/workflow-sapi/2/tenants/%s/kinds/%s/test-workflows/%s", tenantRNS, kindID, workflowID)
	var resp testWorkflowDetail
	_, err := c.do(http.MethodGet, path, nil, &resp)
	return resp, err
}

// triggerTestWorkflow simulates a user action (e.g. "approve", "reject") on
// a test workflow currently sitting at a step node.
// POST /workflow-sapi/2/tenants/{tenant_rns}/kinds/{kind}/test-workflows/{workflow_id}/triggers
//
// Body shape confirmed against the live OpenAPI spec
// (TestWorkflowTriggerPayload): {"trigger": string, "parameters"?: object,
// "as_user"?: UserImpersonationPayload}. This PoC only uses "trigger" since
// the operator acts as themselves for both approval layers (see design doc).
func (c *workflowClient) triggerTestWorkflow(tenantRNS, kindID, workflowID, trigger string) error {
	body := map[string]string{"trigger": trigger}
	path := fmt.Sprintf("/workflow-sapi/2/tenants/%s/kinds/%s/test-workflows/%s/triggers", tenantRNS, kindID, workflowID)
	_, err := c.do(http.MethodPost, path, body, nil)
	return err
}

// listTestWorkflowStates prints the raw state history of a test workflow,
// for manual inspection (which node it's at, which user groups are
// resolved, etc.) since this PoC does not model the full state schema.
// GET /workflow-sapi/2/tenants/{tenant_rns}/kinds/{kind}/test-workflows/{workflow_id}/states
func (c *workflowClient) listTestWorkflowStates(tenantRNS, kindID, workflowID string) error {
	path := fmt.Sprintf("/workflow-sapi/2/tenants/%s/kinds/%s/test-workflows/%s/states", tenantRNS, kindID, workflowID)
	_, err := c.do(http.MethodGet, path, nil, nil)
	return err
}

// publishedVersionResponse captures the fields needed from a publish
// response — version_major is the version number production workflows are
// created against.
type publishedVersionResponse struct {
	VersionMajor *int `json:"version_major"`
}

// publishDefinitionDraft publishes a definition draft, making it usable to
// create production workflows.
// PUT /workflow-sapi/2/tenants/{tenant_rns}/kinds/{kind}/definitions/{number}/publish
func (c *workflowClient) publishDefinitionDraft(tenantRNS, kindID string, number int) (int, error) {
	path := fmt.Sprintf("/workflow-sapi/2/tenants/%s/kinds/%s/definitions/%d/publish", tenantRNS, kindID, number)
	var resp publishedVersionResponse
	_, err := c.do(http.MethodPut, path, nil, &resp)
	if err != nil {
		return 0, err
	}
	if resp.VersionMajor == nil {
		return 0, fmt.Errorf("publish response had no version_major")
	}
	return *resp.VersionMajor, nil
}

// createProductionWorkflow creates a real production workflow instance
// against a published version. Unlike test workflows, production workflows
// process automatically — there is no equivalent of processTestWorkflow to
// call.
// POST /workflow-sapi/2/tenants/{tenant_rns}/kinds/{kind}/versions/{version}/workflows
func (c *workflowClient) createProductionWorkflow(tenantRNS, kindID string, version int, params map[string]any) (string, error) {
	body := map[string]any{"payload": params}
	path := fmt.Sprintf("/workflow-sapi/2/tenants/%s/kinds/%s/versions/%d/workflows", tenantRNS, kindID, version)
	var resp workflowActionResponse
	_, err := c.do(http.MethodPost, path, body, &resp)
	return resp.WorkflowID, err
}

// getJob reads the current overall state of a production workflow via the
// ABAC-scoped /jobs resource — this is the same resource the OneCloud
// Portal's Approvals/My Requests tabs are backed by. Access depends on the
// caller's identity (requester, viewer, or step-user), not on the tenant
// that owns the Kind.
// GET /workflow-sapi/2/jobs/{workflow_id}
func (c *workflowClient) getJob(workflowID string) (testWorkflowDetail, error) {
	path := fmt.Sprintf("/workflow-sapi/2/jobs/%s", workflowID)
	var resp testWorkflowDetail
	_, err := c.do(http.MethodGet, path, nil, &resp)
	return resp, err
}

// listJobs lists workflows visible to the caller via /jobs, optionally
// filtered (e.g. approver=<email> to mirror the Portal's "Approvals" tab,
// created_by=<email> to mirror "My requests"). Prints the raw response for
// inspection.
// GET /workflow-sapi/2/jobs
func (c *workflowClient) listJobs(filters map[string]string) error {
	path := "/workflow-sapi/2/jobs"
	if len(filters) > 0 {
		q := url.Values{}
		for k, v := range filters {
			q.Set(k, v)
		}
		path += "?" + q.Encode()
	}
	_, err := c.do(http.MethodGet, path, nil, nil)
	return err
}

// triggerJobAction performs a user action (approve/reject/cancel) on a
// production workflow via the ABAC-scoped /jobs resource — this is the
// endpoint a UCP-native interface (e.g. a CLI) would call directly, using
// the same bearer token obtained from the shared Keycloak realm, without
// needing to go through the OneCloud Portal.
// POST /workflow-sapi/2/jobs/{workflow_id}/{action}
func (c *workflowClient) triggerJobAction(workflowID, action string, payload map[string]any) error {
	path := fmt.Sprintf("/workflow-sapi/2/jobs/%s/%s", workflowID, action)
	_, err := c.do(http.MethodPost, path, payload, nil)
	return err
}
