// 03-chi-standard: chi with manual handlers — no code generation.
//
// Key things to observe:
// - chi has NO native global error handler — handlers must write responses directly
//   OR you build a custom middleware to intercept errors (extra boilerplate)
// - This is the standard chi pattern: each handler is responsible for its own errors
// - Compare: echo's HTTPErrorHandler is one function; chi requires a shared helper
//   that every handler must remember to call — discipline-dependent, not enforced
// - Request binding: manual json.NewDecoder — no c.Bind() equivalent
package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/kitchen-sink/shared"
)

type ErrorResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId,omitempty"`
}

// writeError is the chi workaround — a helper every handler must call.
// In echo, this logic lives ONCE in HTTPErrorHandler.
// In chi standard mode, it's a shared helper that handlers must remember to use.
// If a handler forgets and writes its own response, errors become inconsistent.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	reqID := chimiddleware.GetReqID(r.Context())
	w.Header().Set("Content-Type", "application/json")

	var de *shared.DomainError
	if errors.As(err, &de) {
		if de.Cause != nil {
			slog.Error("domain error", "code", de.Code, "cause", de.Cause, "request_id", reqID)
		}
		w.WriteHeader(de.Status)
		json.NewEncoder(w).Encode(ErrorResponse{Code: de.Code, Message: de.Message, RequestID: reqID})
		return
	}

	slog.Error("unhandled error", "error", err, "request_id", reqID)
	w.WriteHeader(500)
	json.NewEncoder(w).Encode(ErrorResponse{Code: "INTERNAL_ERROR", Message: "an internal error occurred", RequestID: reqID})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func main() {
	store := shared.NewStore()

	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)

	r.Post("/compute", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name     string `json:"name"`
			TenantID string `json:"tenantId"`
			Provider string `json:"provider"`
			Size     string `json:"size"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, r, &shared.DomainError{Code: "INVALID_REQUEST", Message: "invalid request body", Status: 400})
			return
		}
		if req.Name == "" {
			writeError(w, r, &shared.DomainError{Code: "INVALID_REQUEST", Message: "name is required", Status: 400})
			return
		}
		if req.TenantID == "" {
			writeError(w, r, &shared.DomainError{Code: "INVALID_REQUEST", Message: "tenantId is required", Status: 400})
			return
		}
		if req.Provider != "gcp" && req.Provider != "aws" {
			writeError(w, r, &shared.DomainError{Code: "INVALID_REQUEST", Message: "provider must be gcp or aws", Status: 400})
			return
		}
		if req.Size == "" {
			req.Size = "medium"
		}
		inst, err := store.Create(req.Name, req.TenantID, req.Provider, req.Size)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{
			"workflowId": inst.WorkflowID,
			"status":     "provisioning",
			"message":    "compute instance provisioning started",
		})
	})

	r.Get("/compute", func(w http.ResponseWriter, r *http.Request) {
		tenantID := r.URL.Query().Get("tenantId")
		if tenantID == "" {
			writeError(w, r, &shared.DomainError{Code: "INVALID_REQUEST", Message: "tenantId query param is required", Status: 400})
			return
		}
		instances, err := store.List(tenantID, 20)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"items": instances, "total": len(instances)})
	})

	r.Get("/compute/{name}", func(w http.ResponseWriter, r *http.Request) {
		inst, err := store.Get(chi.URLParam(r, "name"))
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, inst)
	})

	r.Delete("/compute/{name}", func(w http.ResponseWriter, r *http.Request) {
		if err := store.Delete(chi.URLParam(r, "name")); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	slog.Info("03-chi-standard listening", "port", 9003)
	http.ListenAndServe(":9003", r)
}
