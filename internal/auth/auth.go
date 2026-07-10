package auth

import (
	"context"
	"fmt"
	"sort"
)

type Permission string

const (
	PermissionVersionRead       Permission = "version:read"
	PermissionIncidentsRead     Permission = "incidents:read"
	PermissionIncidentsWrite    Permission = "incidents:write"
	PermissionEventsRead        Permission = "events:read"
	PermissionPoliciesRead      Permission = "policies:read"
	PermissionPoliciesWrite     Permission = "policies:write"
	PermissionRunbooksRead      Permission = "runbooks:read"
	PermissionRunbooksWrite     Permission = "runbooks:write"
	PermissionRunbooksRun       Permission = "runbooks:run"
	PermissionExecutionsRead    Permission = "executions:read"
	PermissionExecutionsControl Permission = "executions:control"
	PermissionApprovalsRead     Permission = "approvals:read"
	PermissionApprovalsDecide   Permission = "approvals:decide"
	PermissionIntegrationsRead  Permission = "integrations:read"
	PermissionIntegrationsWrite Permission = "integrations:write"
	PermissionAuditRead         Permission = "audit:read"
	PermissionServiceAccounts   Permission = "service_accounts:manage"
)

type Role string

const (
	RoleViewer           Role = "viewer"
	RoleOperator         Role = "operator"
	RoleResponder        Role = "responder"
	RoleRunbookEditor    Role = "runbook-editor"
	RoleIntegrationAdmin Role = "integration-admin"
	RoleTenantAdmin      Role = "tenant-admin"
)

var grants = map[Role]map[Permission]struct{}{
	RoleViewer:           permissionSet(PermissionVersionRead, PermissionIncidentsRead, PermissionEventsRead, PermissionPoliciesRead, PermissionRunbooksRead, PermissionExecutionsRead, PermissionAuditRead, PermissionIntegrationsRead),
	RoleOperator:         permissionSet(PermissionVersionRead, PermissionIncidentsRead, PermissionIncidentsWrite, PermissionEventsRead, PermissionPoliciesRead, PermissionRunbooksRead, PermissionExecutionsRead, PermissionAuditRead, PermissionIntegrationsRead),
	RoleResponder:        permissionSet(PermissionVersionRead, PermissionIncidentsRead, PermissionIncidentsWrite, PermissionEventsRead, PermissionPoliciesRead, PermissionRunbooksRead, PermissionRunbooksRun, PermissionExecutionsRead, PermissionExecutionsControl, PermissionApprovalsRead, PermissionApprovalsDecide, PermissionAuditRead, PermissionIntegrationsRead),
	RoleRunbookEditor:    permissionSet(PermissionVersionRead, PermissionIncidentsRead, PermissionEventsRead, PermissionPoliciesRead, PermissionPoliciesWrite, PermissionRunbooksRead, PermissionRunbooksWrite, PermissionRunbooksRun, PermissionExecutionsRead, PermissionAuditRead, PermissionIntegrationsRead),
	RoleIntegrationAdmin: permissionSet(PermissionVersionRead, PermissionIncidentsRead, PermissionEventsRead, PermissionPoliciesRead, PermissionRunbooksRead, PermissionExecutionsRead, PermissionAuditRead, PermissionIntegrationsRead, PermissionIntegrationsWrite),
	RoleTenantAdmin:      permissionSet(PermissionVersionRead, PermissionIncidentsRead, PermissionIncidentsWrite, PermissionEventsRead, PermissionPoliciesRead, PermissionPoliciesWrite, PermissionRunbooksRead, PermissionRunbooksWrite, PermissionRunbooksRun, PermissionExecutionsRead, PermissionExecutionsControl, PermissionApprovalsRead, PermissionApprovalsDecide, PermissionIntegrationsRead, PermissionIntegrationsWrite, PermissionAuditRead, PermissionServiceAccounts),
}

func permissionSet(values ...Permission) map[Permission]struct{} {
	out := make(map[Permission]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func ValidRole(role Role) bool {
	_, ok := grants[role]
	return ok
}

func Roles() []Role {
	out := make([]Role, 0, len(grants))
	for role := range grants {
		out = append(out, role)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func NormalizeRoles(values []Role) ([]Role, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("at least one role is required")
	}
	seen := map[Role]struct{}{}
	out := make([]Role, 0, len(values))
	for _, role := range values {
		if !ValidRole(role) {
			return nil, fmt.Errorf("unknown role %q", role)
		}
		if _, ok := seen[role]; ok {
			continue
		}
		seen[role] = struct{}{}
		out = append(out, role)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

type Principal struct {
	TenantID  string `json:"tenant_id"`
	ActorType string `json:"actor_type"`
	ActorID   string `json:"actor_id"`
	Roles     []Role `json:"roles"`
	KeyID     string `json:"key_id,omitempty"`
}

func (p Principal) Allowed(permission Permission) bool {
	for _, role := range p.Roles {
		if _, ok := grants[role][permission]; ok {
			return true
		}
	}
	return false
}

func (p Principal) Require(permission Permission) error {
	if !p.Allowed(permission) {
		return fmt.Errorf("permission %s is required", permission)
	}
	return nil
}

type contextKey struct{}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, contextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(contextKey{}).(Principal)
	return principal, ok
}
