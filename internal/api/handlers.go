package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/chavaliadi/watchdog/internal/service"
)

type Handlers struct {
	svc *service.MonitorService
}

func NewHandlers(svc *service.MonitorService) *Handlers {
	return &Handlers{svc: svc}
}

// CreateMonitor handles POST /monitors
func (h *Handlers) CreateMonitor(w http.ResponseWriter, r *http.Request) {
	var body CreateMonitorJSON
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "MALFORMED_JSON", fmt.Sprintf("malformed JSON body: %v", err), nil)
		return
	}

	req := service.CreateMonitorRequest{
		Name:                body.Name,
		Kind:                body.Kind,
		Target:              body.Target,
		Method:              body.Method,
		ExpectedStatusRange: body.ExpectedStatusRange,
		IntervalMs:          body.IntervalMs,
		TimeoutMs:           body.TimeoutMs,
		Enabled:             body.Enabled,
	}

	created, err := h.svc.CreateMonitor(r.Context(), req)
	if err != nil {
		mapServiceError(w, err)
		return
	}

	w.Header().Set("Location", "/monitors/"+created.ID)
	writeJSON(w, http.StatusCreated, toMonitorResponse(created))
}

// ListMonitors handles GET /monitors
func (h *Handlers) ListMonitors(w http.ResponseWriter, r *http.Request) {
	monitors, err := h.svc.ListMonitors(r.Context())
	if err != nil {
		mapServiceError(w, err)
		return
	}

	resp := make([]MonitorResponse, 0, len(monitors))
	for _, m := range monitors {
		resp = append(resp, toMonitorResponse(m))
	}

	writeJSON(w, http.StatusOK, resp)
}

// GetMonitor handles GET /monitors/{id}
func (h *Handlers) GetMonitor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, err := h.svc.GetMonitor(r.Context(), id)
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toMonitorResponse(m))
}

// PatchMonitor handles PATCH /monitors/{id}
func (h *Handlers) PatchMonitor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var body PatchMonitorJSON
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "MALFORMED_JSON", fmt.Sprintf("malformed JSON body: %v", err), nil)
		return
	}

	req := service.PatchMonitorRequest{
		Name:                body.Name,
		Kind:                body.Kind,
		Target:              body.Target,
		Method:              body.Method,
		ExpectedStatusRange: body.ExpectedStatusRange,
		IntervalMs:          body.IntervalMs,
		TimeoutMs:           body.TimeoutMs,
		Enabled:             body.Enabled,
	}

	updated, err := h.svc.PatchMonitor(r.Context(), id, req)
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toMonitorResponse(updated))
}

// DeleteMonitor handles DELETE /monitors/{id}
func (h *Handlers) DeleteMonitor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.svc.DeleteMonitor(r.Context(), id); err != nil {
		mapServiceError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// GetMonitorStatus handles GET /monitors/{id}/status
func (h *Handlers) GetMonitorStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st, err := h.svc.GetStatus(r.Context(), id)
	if err != nil {
		mapServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toMonitorStatusResponse(st))
}

// GetMonitorChecks handles GET /monitors/{id}/checks
func (h *Handlers) GetMonitorChecks(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	limit := 20
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		parsed, err := strconv.Atoi(limitStr)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "limit query parameter must be a positive integer", nil)
			return
		}
		limit = parsed
	}

	checks, err := h.svc.ListChecks(r.Context(), id, limit)
	if err != nil {
		mapServiceError(w, err)
		return
	}

	resp := make([]CheckResultResponse, 0, len(checks))
	for _, c := range checks {
		resp = append(resp, toCheckResultResponse(c))
	}

	writeJSON(w, http.StatusOK, resp)
}
