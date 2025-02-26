package handlers

import (
	"encoding/json"
	"net/http"
	"sync/atomic"

	"timesheet-filler/internal/models"
)

type HealthHandler struct {
	ready *int32
}

func NewHealthHandler() *HealthHandler {
	var ready int32 = 1
	return &HealthHandler{
		ready: &ready,
	}
}

func (h *HealthHandler) SetNotReady() {
	atomic.StoreInt32(h.ready, 0)
}

func (h *HealthHandler) SetReady() {
	atomic.StoreInt32(h.ready, 1)
}

func (h *HealthHandler) LivenessHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	response := models.HealthCheckResponse{
		Status: "ok",
	}
	json.NewEncoder(w).Encode(response)
}

func (h *HealthHandler) ReadinessHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if atomic.LoadInt32(h.ready) == 1 {
		w.WriteHeader(http.StatusOK)
		response := models.HealthCheckResponse{
			Status: "ready",
		}
		json.NewEncoder(w).Encode(response)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		response := models.HealthCheckResponse{
			Status: "not ready",
		}
		json.NewEncoder(w).Encode(response)
	}
}
