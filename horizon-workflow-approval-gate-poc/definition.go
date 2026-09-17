package main

// buildDefinition returns the two-layer approval state machine described in
// docs/projects/universal-control-plane-ucp/pocs/horizon-workflow-approval-gate/design.md
// (Part A — the callback node is deferred to Part B, blocked on finding a
// reachable receiver; see the design doc's Risks section):
//
//	start -> tenant_admin_approval (step) -> network_team_approval (step) -> end
//
// Layer 1 ("tenant_admin_approval") resolves its approver group dynamically
// from the workflow's own tenant_rns parameter. Layer 2
// ("network_team_approval") resolves against the fixed clsd-ucp-team Team,
// standing in for a real network-team approver group, regardless of which
// tenant submitted the request. A reject/cancel at either step routes
// straight to "end" without ever reaching the other step.
func buildDefinition(teamName string) map[string]any {
	rejectPayload := map[string]any{
		"title": "Reject",
		"type":  "object",
		"properties": map[string]any{
			"reason": map[string]any{"type": "string"},
		},
	}
	cancelPayload := rejectPayload

	return map[string]any{
		"nodes": map[string]any{
			"start": map[string]any{
				"kind": "start",
				"name": "Started",
			},
			"tenant_admin_approval": map[string]any{
				"kind":      "step",
				"step_kind": "tenant_admin_approval",
				"name":      "Submitting tenant admin approval",
				"triggers": map[string]any{
					"approve": map[string]any{
						"payload": map[string]any{"title": "Approve"},
						"users": []any{
							map[string]any{
								"kind":       "tenant",
								"tenant_rns": "{{ params.tenant_rns }}",
								"role":       "admins",
							},
						},
					},
					"reject": map[string]any{
						"payload": rejectPayload,
						"users": []any{
							map[string]any{
								"kind":       "tenant",
								"tenant_rns": "{{ params.tenant_rns }}",
								"role":       "admins",
							},
						},
					},
					"cancel": map[string]any{
						"payload": cancelPayload,
						"users": []any{
							map[string]any{"kind": "user", "user": "{{ created_by }}"},
						},
					},
				},
			},
			"network_team_approval": map[string]any{
				"kind":      "step",
				"step_kind": "network_team_approval",
				"name":      "Network team approval",
				"triggers": map[string]any{
					"approve": map[string]any{
						"payload": map[string]any{"title": "Approve"},
						"users": []any{
							map[string]any{
								"kind": "team",
								"team": teamName,
								"role": "admins",
							},
						},
					},
					"reject": map[string]any{
						"payload": rejectPayload,
						"users": []any{
							map[string]any{
								"kind": "team",
								"team": teamName,
								"role": "admins",
							},
						},
					},
					"cancel": map[string]any{
						"payload": cancelPayload,
						"users": []any{
							map[string]any{"kind": "user", "user": "{{ created_by }}"},
						},
					},
				},
			},
			"end": map[string]any{
				"kind": "end",
				"name": "Completed",
			},
		},
		"transitions": []any{
			map[string]any{"source": "start", "target": "tenant_admin_approval"},
			map[string]any{"source": "tenant_admin_approval", "target": "network_team_approval", "condition": "approve"},
			map[string]any{"source": "tenant_admin_approval", "target": "end", "condition": "reject", "result": "Rejected"},
			map[string]any{"source": "tenant_admin_approval", "target": "end", "condition": "cancel", "result": "Canceled"},
			map[string]any{"source": "network_team_approval", "target": "end", "condition": "approve", "result": "Successful"},
			map[string]any{"source": "network_team_approval", "target": "end", "condition": "reject", "result": "Rejected"},
			map[string]any{"source": "network_team_approval", "target": "end", "condition": "cancel", "result": "Canceled"},
		},
		"payload_schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tenant_rns":    map[string]any{"type": "string"},
				"resource_type": map[string]any{"type": "string"},
				"resource_id":   map[string]any{"type": "string"},
			},
			"required": []any{"tenant_rns"},
		},
	}
}
