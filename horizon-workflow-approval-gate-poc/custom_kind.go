package main

import (
	"fmt"
	"net/http"
	"os"
)

const (
	defaultCustomKindID = "tenant-ucp-admins"
	ucpServiceRNS       = "rns:roc:ucp"
	secretClientSecret  = "client_secret"
	configKeycloakURL   = "keycloak_url"
	defaultKeycloakURL  = "https://qa2-accounts-onecloud.rakuten-it.com/auth/realms/roc/protocol/openid-connect/token"
	defaultCoreDataURL  = "https://qa-horizon-data-api.r-local.net"
	defaultKindRole     = "tenant-admin"
	defaultClientID     = "rns:roc:ucp-ops"
)

// customKindConfig describes a custom HTTP user group kind that reads a
// tenant's members from Core Data with a UCP service client and keeps the
// holders of one UCP role. The client secret and the Keycloak token URL are
// stored as tenant secret/config in the Workflow tenant and only referenced
// by name from the kind.
type customKindConfig struct {
	id           string
	ownerTenant  string
	clientID     string
	clientSecret string
	keycloakURL  string
	coreDataURL  string
	role         string
}

func loadCustomKindConfig(ownerTenant string) customKindConfig {
	return customKindConfig{
		id:           envOr("CUSTOM_KIND_ID", defaultCustomKindID),
		ownerTenant:  ownerTenant,
		clientID:     envOr("CLIENT_ID", defaultClientID),
		clientSecret: os.Getenv("CLIENT_SECRET"),
		keycloakURL:  envOr("KEYCLOAK_URL", defaultKeycloakURL),
		coreDataURL:  envOr("CORE_DATA_BASE_URL", defaultCoreDataURL),
		role:         envOr("CUSTOM_KIND_ROLE", defaultKindRole),
	}
}

// spec builds the kind's http spec. The URL needs roles=true or Core Data
// omits services[].roles; the processor uses a single & for AND, the only
// form the registration endpoint's JSONPath dialect accepts.
func (c customKindConfig) spec() map[string]any {
	return map[string]any{
		"kind": "http",
		"url":  c.coreDataURL + "/v0/tenants/rns:roc:iam::{{ ctx_tenant }}/members?roles=true",
		"auth": map[string]any{
			"auth_type":     "oauth2_client_credentials",
			"client_id":     c.clientID,
			"client_secret": "{{ tenant_secrets." + secretClientSecret + " }}",
			"token_url":     "{{ tenant_config." + configKeycloakURL + " }}",
		},
		"processor": fmt.Sprintf("$.items[?(@.services[?(@.rns == '%s' & @.roles[?(@.name == '%s')])])].email", ucpServiceRNS, c.role),
	}
}

// group is the step user group: the kind id, the tenant that owns the kind,
// and the requesting tenant, which the kind sees as ctx_tenant.
func (c customKindConfig) group() map[string]any {
	return map[string]any{"kind": c.id, "tenant_rns": c.ownerTenant, "tenant": "{{ params.tenant_name }}"}
}

// setup stores the secret and config, then registers the kind.
func (c *workflowClient) setupCustomKind(cfg customKindConfig) error {
	if cfg.clientSecret == "" {
		return fmt.Errorf("CLIENT_SECRET is required for the custom-kind variant")
	}
	if err := c.upsertTenantEntry(cfg.ownerTenant, "secrets", secretClientSecret, cfg.clientSecret); err != nil {
		return fmt.Errorf("store tenant secret: %w", err)
	}
	if err := c.upsertTenantEntry(cfg.ownerTenant, "config", configKeycloakURL, cfg.keycloakURL); err != nil {
		return fmt.Errorf("store tenant config: %w", err)
	}
	if err := c.upsertUserGroupKind(cfg.ownerTenant, cfg.id, cfg.spec()); err != nil {
		return fmt.Errorf("register user group kind: %w", err)
	}
	return nil
}

// teardown removes the kind, the secret and the config; failures are printed
// and do not stop the remaining deletions.
func (c *workflowClient) teardownCustomKind(cfg customKindConfig) {
	base := fmt.Sprintf("/workflow-sapi/2/tenants/%s", cfg.ownerTenant)
	for _, p := range []string{
		base + "/user-group-kinds/" + cfg.id,
		base + "/secrets/" + secretClientSecret,
		base + "/config/" + configKeycloakURL,
	} {
		if _, err := c.do(http.MethodDelete, p, nil, nil); err != nil {
			fmt.Printf("warning: %v\n", err)
		}
	}
}

// upsertTenantEntry creates a tenant-level secret or config entry, replacing
// it if the key already exists (secret values cannot be read back).
// POST /workflow-sapi/2/tenants/{tenant_rns}/{secrets|config}
func (c *workflowClient) upsertTenantEntry(tenantRNS, collection, key, value string) error {
	base := fmt.Sprintf("/workflow-sapi/2/tenants/%s/%s", tenantRNS, collection)
	body := map[string]string{"key": key, "value": value, "description": "PoC MCUCP-304"}
	status, err := c.do(http.MethodPost, base, body, nil)
	if status != http.StatusConflict {
		return err
	}
	if _, err := c.do(http.MethodDelete, base+"/"+key, nil, nil); err != nil {
		return err
	}
	_, err = c.do(http.MethodPost, base, body, nil)
	return err
}

// upsertUserGroupKind registers a custom HTTP user group kind, or replaces
// its spec if the id already exists.
// POST /workflow-sapi/2/tenants/{tenant_rns}/user-group-kinds
func (c *workflowClient) upsertUserGroupKind(tenantRNS, id string, spec map[string]any) error {
	base := fmt.Sprintf("/workflow-sapi/2/tenants/%s/user-group-kinds", tenantRNS)
	status, err := c.do(http.MethodPost, base, map[string]any{"id": id, "spec": spec}, nil)
	if status != http.StatusConflict {
		return err
	}
	_, err = c.do(http.MethodPut, base+"/"+id, map[string]any{"spec": spec}, nil)
	return err
}

// printResolvedApprovers prints, for the workflow's current step, which users
// each trigger resolved to and which triggers the caller may use.
func printResolvedApprovers(wf testWorkflowDetail) {
	fmt.Println("\n--- Resolved approvers on the current step ---")
	if len(wf.Steps) == 0 {
		fmt.Println("no step yet")
		return
	}
	for trigger, groups := range wf.Steps[len(wf.Steps)-1].TriggersUsers {
		for _, g := range groups {
			fmt.Printf("%s: %s -> %v\n", trigger, g.Title, g.Emails)
		}
	}
	for _, t := range wf.UserTriggers {
		fmt.Printf("operator may %s: %v\n", t.Trigger, t.Allowed)
	}
}
