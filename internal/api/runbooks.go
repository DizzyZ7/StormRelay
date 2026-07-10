package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/DizzyZ7/StormRelay/internal/runbooks"
	"github.com/DizzyZ7/StormRelay/internal/storage"
	"github.com/DizzyZ7/StormRelay/internal/telemetry"
)

func (s *Server) validateRunbook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "read_failed", "unable to read runbook", nil)
		return
	}
	doc, err := runbooks.Parse(body)
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_runbook", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": true, "runbook_id": doc.Metadata.ID, "version": doc.Metadata.Version, "steps": len(doc.Spec.Steps)})
}
func (s *Server) applyRunbook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "read_failed", "unable to read runbook", nil)
		return
	}
	out, err := s.store.ApplyRunbook(r.Context(), storage.ApplyRunbookInput{TenantID: tenantID(r), DocumentYAML: string(body), ActorID: actorID(r), RequestID: telemetry.RequestID(r.Context()), TraceID: telemetry.TraceID(r.Context())})
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_runbook", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}
func (s *Server) listRunbooks(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListRunbooks(r.Context(), tenantID(r))
	if err != nil {
		mapStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (s *Server) runbookRoute(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/runbooks/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) == 2 && parts[1] == "run" && r.Method == http.MethodPost {
		var in struct {
			IncidentID string         `json:"incident_id"`
			DryRun     bool           `json:"dry_run"`
			Parameters map[string]any `json:"parameters"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}
		if err := validateParameters(in.Parameters); err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_parameters", err.Error(), nil)
			return
		}
		out, err := s.store.StartExecution(r.Context(), storage.StartExecutionInput{TenantID: tenantID(r), RunbookKey: parts[0], IncidentID: in.IncidentID, RequestedBy: actorID(r), RequestID: telemetry.RequestID(r.Context()), TraceID: telemetry.TraceID(r.Context()), DryRun: in.DryRun, Parameters: in.Parameters})
		if err != nil {
			mapStoreError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, out)
		return
	}
	writeError(w, r, http.StatusNotFound, "not_found", "runbook route not found", nil)
}
func (s *Server) listExecutions(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.store.ListExecutions(r.Context(), tenantID(r), limit)
	if err != nil {
		mapStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (s *Server) executionRoute(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/executions/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) == 1 && r.Method == http.MethodGet {
		out, err := s.store.GetExecution(r.Context(), tenantID(r), parts[0])
		if err != nil {
			mapStoreError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	if len(parts) == 2 && r.Method == http.MethodPost {
		switch parts[1] {
		case "pause", "resume", "cancel":
			out, err := s.store.ChangeExecutionState(r.Context(), tenantID(r), parts[0], parts[1], actorID(r), telemetry.RequestID(r.Context()), telemetry.TraceID(r.Context()))
			if err != nil {
				mapStoreError(w, r, err)
				return
			}
			writeJSON(w, http.StatusOK, out)
			return
		case "rollback":
			out, err := s.store.StartExecutionRollback(r.Context(), tenantID(r), parts[0], actorID(r), telemetry.RequestID(r.Context()), telemetry.TraceID(r.Context()))
			if err != nil {
				mapStoreError(w, r, err)
				return
			}
			writeJSON(w, http.StatusOK, out)
			return
		}
	}
	if len(parts) == 4 && parts[1] == "steps" && parts[3] == "retry" && r.Method == http.MethodPost {
		var body struct {
			Force bool `json:"force"`
		}
		if !decodeJSON(w, r, &body) {
			return
		}
		out, err := s.store.RetryExecutionStep(r.Context(), tenantID(r), parts[0], parts[2], actorID(r), telemetry.RequestID(r.Context()), telemetry.TraceID(r.Context()), body.Force)
		if err != nil {
			mapStoreError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	writeError(w, r, http.StatusNotFound, "not_found", "execution route not found", nil)
}
func (s *Server) listApprovals(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.store.ListApprovals(r.Context(), tenantID(r), r.URL.Query().Get("status"), r.URL.Query().Get("execution_id"), limit)
	if err != nil {
		mapStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (s *Server) approvalRoute(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/approvals/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || r.Method != http.MethodPost || (parts[1] != "approve" && parts[1] != "reject") {
		writeError(w, r, http.StatusNotFound, "not_found", "approval route not found", nil)
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	decision := "approved"
	if parts[1] == "reject" {
		decision = "rejected"
	}
	out, err := s.store.DecideApproval(r.Context(), tenantID(r), parts[0], decision, actorID(r), body.Reason, telemetry.RequestID(r.Context()), telemetry.TraceID(r.Context()))
	if err != nil {
		mapStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func validateParameters(parameters map[string]any) error {
	data, err := json.Marshal(parameters)
	if err != nil {
		return &parameterError{message: "parameters must be JSON-compatible"}
	}
	if len(data) > 64<<10 {
		return &parameterError{message: "parameters exceed 64 KiB"}
	}
	return inspectParameterValue("parameters", parameters, 0)
}
func inspectParameterValue(path string, value any, depth int) error {
	if depth > 10 {
		return &parameterError{message: "parameters nesting exceeds 10 levels"}
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			lower := strings.ToLower(key)
			for _, secret := range []string{"secret", "token", "password", "authorization", "cookie", "credential", "private_key"} {
				if strings.Contains(lower, secret) {
					return &parameterError{message: path + "." + key + " contains a prohibited sensitive key"}
				}
			}
			if err := inspectParameterValue(path+"."+key, child, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for index, child := range typed {
			if err := inspectParameterValue(path+"["+strconv.Itoa(index)+"]", child, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

type parameterError struct{ message string }

func (e *parameterError) Error() string { return e.message }
