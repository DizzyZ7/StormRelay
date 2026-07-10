package runbooks

import "testing"

func TestParseValidRunbook(t *testing.T) {
	doc, err := Parse([]byte(`apiVersion: stormrelay.io/v1
kind: Runbook
metadata:
  id: mitigate-checkout
  version: 1
spec:
  timeout: 15m
  steps:
    - id: request-approval
      type: approval
      with:
        prompt: Restart checkout deployment?
        expiresIn: 30m
    - id: restart
      type: http
      timeout: 10s
      retry:
        maxAttempts: 3
      when:
        field: steps.request-approval.status
        equals: approved
      with:
        method: POST
        url: https://automation.internal/actions/restart
        idempotencyHeader: Idempotency-Key
        body: {"deployment":"checkout"}
`))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Metadata.ID != "mitigate-checkout" || len(doc.Spec.Steps) != 2 {
		t.Fatalf("unexpected document: %#v", doc)
	}
}

func TestRejectsInlineAuthorization(t *testing.T) {
	_, err := Parse([]byte(`apiVersion: stormrelay.io/v1
kind: Runbook
metadata: {id: unsafe, version: 1}
spec:
  steps:
    - id: call
      type: http
      with:
        url: https://example.com
        headers: {Authorization: Bearer secret}
`))
	if err == nil {
		t.Fatal("expected sensitive header rejection")
	}
}

func FuzzRunbookParse(f *testing.F) {
	f.Add([]byte("apiVersion: stormrelay.io/v1\nkind: Runbook"))
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = Parse(data) })
}
