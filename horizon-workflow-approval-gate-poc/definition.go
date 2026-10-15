package main

import "fmt"

const (
	outcomeSuccessful = "Successful"
	outcomeCompleted  = "Completed"

	// skipTrigger is fired automatically when the network step's skip_if holds;
	// it carries no payload, unlike the completing trigger.
	skipTrigger = "skip"
)

// definitionOptions selects which optional pieces of the state machine are
// built. The base definition is:
//
//	start -> tenant_admin_approval (step) -> network_team_approval (step) -> end
type definitionOptions struct {
	// teamName is the fixed Team that approves the network_team_approval step.
	teamName string
	// layer1Groups are the user groups that may act on tenant_admin_approval.
	layer1Groups []any
	// conditionalNetwork skips network_team_approval when params.vlan is set,
	// and makes its approving trigger "complete" with a required vlan payload.
	conditionalNetwork bool
	// emailNotify adds an email callback node before the first approval step.
	emailNotify bool
	// noSuccessful ends the approve path with "Completed" instead of
	// "Successful", so that no path sets the Successful outcome.
	noSuccessful bool
}

// tenantAdminGroup resolves to the admins of the tenant passed as the
// workflow's own tenant_rns parameter.
func tenantAdminGroup() map[string]any {
	return map[string]any{
		"kind":       "tenant",
		"tenant_rns": "{{ params.tenant_rns }}",
		"role":       "admins",
	}
}

// serviceResourceRoleGroup resolves to the holders of role on resourceRNS in
// the Core Data service serviceRNS.
func serviceResourceRoleGroup(serviceRNS, resourceRNS, role string) map[string]any {
	return map[string]any{
		"kind":         "service_resource_role",
		"service_rns":  serviceRNS,
		"resource_rns": resourceRNS,
		"role":         role,
	}
}

// approverSlots is the fixed number of user groups built from
// params.approver_emails; slots beyond the list length evaluate to an empty
// value, which the platform treats as a null group and removes.
const approverSlots = 5

// emailUserGroups builds one "user" group per approver slot. The first slot
// carries backup, used when no email resolves.
func emailUserGroups(backup map[string]any) []any {
	groups := make([]any, 0, approverSlots)
	for i := 0; i < approverSlots; i++ {
		g := map[string]any{
			"kind": "user",
			"user": fmt.Sprintf("{{ params.approver_emails[%d] if params.approver_emails | length > %d }}", i, i),
		}
		if i == 0 && backup != nil {
			g["backup_spec"] = backup
		}
		groups = append(groups, g)
	}
	return groups
}

func teamGroup(team string) map[string]any {
	return map[string]any{"kind": "team", "team": team, "role": "admins"}
}

// stepTriggers builds the three triggers of an approval step: the approving
// trigger (named approveTrigger), reject, and a requester-only cancel.
func stepTriggers(approveTrigger string, approvePayload map[string]any, groups []any) map[string]any {
	reasonPayload := func(title string) map[string]any {
		return map[string]any{
			"title": title,
			"type":  "object",
			"properties": map[string]any{
				"reason": map[string]any{"type": "string"},
			},
		}
	}
	return map[string]any{
		approveTrigger: map[string]any{"payload": approvePayload, "users": groups},
		"reject":       map[string]any{"payload": reasonPayload("Reject"), "users": groups},
		"cancel": map[string]any{
			"payload": reasonPayload("Cancel"),
			"users":   []any{map[string]any{"kind": "user", "user": "{{ created_by }}"}},
		},
	}
}

func transition(source, target, condition, result string) map[string]any {
	t := map[string]any{"source": source, "target": target}
	if condition != "" {
		t["condition"] = condition
	}
	if result != "" {
		t["result"] = result
	}
	return t
}

// buildDefinition returns the approval state machine described in
// docs/projects/universal-control-plane-ucp/pocs/horizon-workflow-approval-gate/design.md
// (Part A — the resume callback is deferred to Part B).
//
// A reject/cancel at either step routes straight to "end" without ever
// reaching the other step.
func buildDefinition(opts definitionOptions) map[string]any {
	success := outcomeSuccessful
	if opts.noSuccessful {
		success = outcomeCompleted
	}

	networkApprove := "approve"
	networkApprovePayload := map[string]any{"title": "Approve"}
	networkStep := map[string]any{
		"kind":      "step",
		"step_kind": "network_team_approval",
		"name":      "Network team approval",
	}
	if opts.conditionalNetwork {
		networkApprove = "complete"
		networkApprovePayload = map[string]any{
			"title": "Complete",
			"type":  "object",
			"properties": map[string]any{
				"vlan": map[string]any{"type": "string"},
			},
			"required": []any{"vlan"},
		}
		networkStep["skip_if"] = "{{ params.vlan is defined and params.vlan | length > 0 }}"
		networkStep["skip_with"] = skipTrigger
		networkStep["skipped_label"] = "Network team approval (skipped - VLAN provided)"
	}
	networkTriggers := stepTriggers(networkApprove, networkApprovePayload, []any{teamGroup(opts.teamName)})
	if opts.conditionalNetwork {
		networkTriggers[skipTrigger] = map[string]any{
			"payload": map[string]any{"title": "Skip"},
			"users":   []any{teamGroup(opts.teamName)},
		}
	}
	networkStep["triggers"] = networkTriggers

	nodes := map[string]any{
		"start": map[string]any{"kind": "start", "name": "Started"},
		"tenant_admin_approval": map[string]any{
			"kind":      "step",
			"step_kind": "tenant_admin_approval",
			"name":      "Submitting tenant admin approval",
			"triggers":  stepTriggers("approve", map[string]any{"title": "Approve"}, opts.layer1Groups),
		},
		"network_team_approval": networkStep,
		"end":                   map[string]any{"kind": "end", "name": "Completed"},
	}

	transitions := []any{
		transition("tenant_admin_approval", "network_team_approval", "approve", ""),
		transition("tenant_admin_approval", "end", "reject", "Rejected"),
		transition("tenant_admin_approval", "end", "cancel", "Canceled"),
		transition("network_team_approval", "end", networkApprove, success),
		transition("network_team_approval", "end", "reject", "Rejected"),
		transition("network_team_approval", "end", "cancel", "Canceled"),
	}

	if opts.conditionalNetwork {
		transitions = append(transitions, transition("network_team_approval", "end", skipTrigger, success))
	}

	if opts.emailNotify {
		nodes["notify_approvers"] = map[string]any{
			"kind": "callback",
			"name": "Notify approvers",
			"callback": map[string]any{
				"kind":      "email",
				"recipient": "{{ params.approver_emails[0] }}",
				"subject":   "Approval needed: {{ params.resource_type }} {{ params.resource_id }}",
				"body": "<html><body><p>A provision request for a {{ params.resource_type }} " +
					"({{ params.resource_id }}) from tenant {{ params.tenant_rns }} needs your approval.</p></body></html>",
			},
		}
		transitions = append(transitions,
			transition("start", "notify_approvers", "", ""),
			transition("notify_approvers", "tenant_admin_approval", "success", ""),
			transition("notify_approvers", "end", "failed", "Failed"),
		)
	} else {
		transitions = append(transitions, transition("start", "tenant_admin_approval", "", ""))
	}

	return map[string]any{
		"nodes":       nodes,
		"transitions": transitions,
		"payload_schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tenant_rns":      map[string]any{"type": "string"},
				"resource_type":   map[string]any{"type": "string"},
				"resource_id":     map[string]any{"type": "string"},
				"vlan":            map[string]any{"type": "string"},
				"approver_emails": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
			"required": []any{"tenant_rns"},
		},
	}
}
