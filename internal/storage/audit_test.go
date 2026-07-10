package storage

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAuditEntryJSONContract(t *testing.T) {
	entry := AuditEntry{
		ID: "audit-1", TenantID: "tenant-1", ActorType: "service-account", ActorID: "actor-1",
		Action: "authorization.denied", ResourceType: "http_route", ResourceID: "/api/v1/incidents",
		Metadata: json.RawMessage(`{"method":"POST"}`), CreatedAt: time.Unix(1, 0).UTC(),
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["action"]; !ok {
		t.Fatalf("missing action field: %s", data)
	}
	if _, ok := decoded["tenant_id"]; !ok {
		t.Fatalf("missing tenant_id field: %s", data)
	}
	if _, ok := decoded["Action"]; ok {
		t.Fatalf("Go field name leaked into JSON: %s", data)
	}
}
