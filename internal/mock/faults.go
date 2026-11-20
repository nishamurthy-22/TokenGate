package mock

import (
	"net/http"
	"strconv"
	"sync"

	"github.com/nishamurthy-22/TokenGate/internal/api"
)

// Fail modes for POST /admin/fail.
const (
	modeNone  = ""
	modeError = "error" // fail every request with 503; /healthz also fails
	modeHang  = "hang"  // accept requests but never answer; /healthz still passes
)

type faults struct {
	mu   sync.RWMutex
	slow float64
	mode string
}

func (f *faults) get() (slow float64, mode string) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.slow, f.mode
}

func (f *faults) set(slow float64, mode string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.slow, f.mode = slow, mode
}

type faultState struct {
	SlowFactor float64 `json:"slow_factor"`
	FailMode   string  `json:"fail_mode"`
}

func (s *Server) writeFaultState(w http.ResponseWriter) {
	slow, mode := s.faults.get()
	api.WriteJSON(w, http.StatusOK, faultState{SlowFactor: slow, FailMode: mode})
}

// POST /admin/slow?factor=4 multiplies all delays by factor.
func (s *Server) handleSlow(w http.ResponseWriter, r *http.Request) {
	factor, err := strconv.ParseFloat(r.URL.Query().Get("factor"), 64)
	if err != nil || factor <= 0 {
		api.WriteError(w, http.StatusBadRequest, api.ErrBadRequest, "factor must be a number > 0")
		return
	}
	_, mode := s.faults.get()
	s.faults.set(factor, mode)
	s.writeFaultState(w)
}

// POST /admin/fail?mode=error|hang makes the backend fail.
func (s *Server) handleFail(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")
	if mode != modeError && mode != modeHang {
		api.WriteError(w, http.StatusBadRequest, api.ErrBadRequest, "mode must be error or hang")
		return
	}
	slow, _ := s.faults.get()
	s.faults.set(slow, mode)
	s.writeFaultState(w)
}

// POST /admin/recover returns the backend to normal.
func (s *Server) handleRecover(w http.ResponseWriter, r *http.Request) {
	s.faults.set(1, modeNone)
	s.writeFaultState(w)
}
