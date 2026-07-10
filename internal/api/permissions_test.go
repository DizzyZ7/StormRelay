package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DizzyZ7/StormRelay/internal/auth"
)

func TestRequiredPermission(t *testing.T) {
	tests := []struct {
		method     string
		path       string
		permission auth.Permission
	}{
		{http.MethodGet, "/api/v1/version", auth.PermissionVersionRead},
		{http.MethodGet, "/api/v1/incidents", auth.PermissionIncidentsRead},
		{http.MethodPost, "/api/v1/incidents", auth.PermissionIncidentsWrite},
		{http.MethodGet, "/api/v1/runbooks", auth.PermissionRunbooksRead},
		{http.MethodPost, "/api/v1/runbooks", auth.PermissionRunbooksWrite},
		{http.MethodPost, "/api/v1/runbooks/demo/run", auth.PermissionRunbooksRun},
		{http.MethodGet, "/api/v1/executions/id", auth.PermissionExecutionsRead},
		{http.MethodPost, "/api/v1/executions/id/cancel", auth.PermissionExecutionsControl},
		{http.MethodGet, "/api/v1/approvals", auth.PermissionApprovalsRead},
		{http.MethodPost, "/api/v1/approvals/id/approve", auth.PermissionApprovalsDecide},
		{http.MethodPost, "/api/v1/plugins", auth.PermissionIntegrationsWrite},
		{http.MethodGet, "/api/v1/service-accounts", auth.PermissionServiceAccounts},
		{http.MethodPost, "/api/v1/oidc/providers", auth.PermissionIdentityManage},
		{http.MethodPost, "/api/v1/oidc/providers/id/identities", auth.PermissionIdentityManage},
		{http.MethodPut, "/api/v1/oidc/identities/id", auth.PermissionIdentityManage},
		{http.MethodGet, "/api/v1/unmapped-future-route", permissionDeny},
	}
	for _, test := range tests {
		req := httptest.NewRequest(test.method, test.path, nil)
		if got := requiredPermission(req); got != test.permission {
			t.Fatalf("%s %s: got %s want %s", test.method, test.path, got, test.permission)
		}
	}
}
