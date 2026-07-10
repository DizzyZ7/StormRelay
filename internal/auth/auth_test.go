package auth

import "testing"

func TestRolePermissions(t *testing.T) {
	tests := []struct {
		role       Role
		permission Permission
		allowed    bool
	}{
		{RoleViewer, PermissionIncidentsRead, true},
		{RoleViewer, PermissionIncidentsWrite, false},
		{RoleViewer, PermissionIdentityManage, false},
		{RoleResponder, PermissionApprovalsDecide, true},
		{RoleResponder, PermissionIdentityManage, false},
		{RoleRunbookEditor, PermissionRunbooksWrite, true},
		{RoleRunbookEditor, PermissionIntegrationsWrite, false},
		{RoleIntegrationAdmin, PermissionIntegrationsWrite, true},
		{RoleIntegrationAdmin, PermissionIdentityManage, false},
		{RoleTenantAdmin, PermissionServiceAccounts, true},
		{RoleTenantAdmin, PermissionIdentityManage, true},
	}
	for _, tt := range tests {
		principal := Principal{Roles: []Role{tt.role}}
		if got := principal.Allowed(tt.permission); got != tt.allowed {
			t.Fatalf("%s %s: got %v", tt.role, tt.permission, got)
		}
	}
}

func TestNormalizeRolesRejectsUnknownAndDeduplicates(t *testing.T) {
	roles, err := NormalizeRoles([]Role{RoleResponder, RoleViewer, RoleResponder})
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) != 2 || roles[0] != RoleResponder || roles[1] != RoleViewer {
		t.Fatalf("unexpected roles: %#v", roles)
	}
	if _, err := NormalizeRoles([]Role{"root"}); err == nil {
		t.Fatal("expected unknown role rejection")
	}
}
