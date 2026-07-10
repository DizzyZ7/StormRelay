package api

import (
	"net/http"
	"strings"

	"github.com/DizzyZ7/StormRelay/internal/auth"
)

const permissionDeny auth.Permission = "__deny__"

func requiredPermission(r *http.Request) auth.Permission {
	path := r.URL.Path
	method := r.Method
	switch {
	case path == "/api/v1/version":
		return auth.PermissionVersionRead
	case path == "/api/v1/auth/roles":
		return auth.PermissionVersionRead
	case path == "/api/v1/service-accounts" || strings.HasPrefix(path, "/api/v1/service-accounts/") || strings.HasPrefix(path, "/api/v1/service-account-keys/"):
		return auth.PermissionServiceAccounts
	case path == "/api/v1/sources" && method == http.MethodGet:
		return auth.PermissionIntegrationsRead
	case path == "/api/v1/sources" || strings.HasPrefix(path, "/api/v1/sources/"):
		return auth.PermissionIntegrationsWrite
	case path == "/api/v1/incidents" && method == http.MethodGet:
		return auth.PermissionIncidentsRead
	case path == "/api/v1/incidents" && method == http.MethodPost:
		return auth.PermissionIncidentsWrite
	case strings.HasPrefix(path, "/api/v1/incidents/") && method == http.MethodGet:
		return auth.PermissionIncidentsRead
	case strings.HasPrefix(path, "/api/v1/incidents/"):
		return auth.PermissionIncidentsWrite
	case path == "/api/v1/policies" && method == http.MethodGet:
		return auth.PermissionPoliciesRead
	case path == "/api/v1/policies" || path == "/api/v1/policies/validate":
		return auth.PermissionPoliciesWrite
	case path == "/api/v1/notification-channels" && method == http.MethodGet:
		return auth.PermissionIntegrationsRead
	case path == "/api/v1/notification-channels":
		return auth.PermissionIntegrationsWrite
	case path == "/api/v1/audit/export":
		return auth.PermissionAuditRead
	case path == "/api/v1/runbooks" && method == http.MethodGet:
		return auth.PermissionRunbooksRead
	case path == "/api/v1/runbooks" || path == "/api/v1/runbooks/validate":
		return auth.PermissionRunbooksWrite
	case strings.HasPrefix(path, "/api/v1/runbooks/"):
		return auth.PermissionRunbooksRun
	case path == "/api/v1/executions" || (strings.HasPrefix(path, "/api/v1/executions/") && method == http.MethodGet):
		return auth.PermissionExecutionsRead
	case strings.HasPrefix(path, "/api/v1/executions/"):
		return auth.PermissionExecutionsControl
	case path == "/api/v1/approvals":
		return auth.PermissionApprovalsRead
	case strings.HasPrefix(path, "/api/v1/approvals/"):
		return auth.PermissionApprovalsDecide
	case path == "/api/v1/plugins" && method == http.MethodGet:
		return auth.PermissionIntegrationsRead
	case path == "/api/v1/plugins" || strings.HasPrefix(path, "/api/v1/plugins/"):
		return auth.PermissionIntegrationsWrite
	default:
		return permissionDeny
	}
}
