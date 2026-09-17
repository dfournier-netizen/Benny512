package web

import (
	"fmt"
	"net/http"
	"time"
)

func (s *Server) handlePatternFade(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FadeMS *int64 `json:"fadeMs"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	if req.FadeMS == nil || *req.FadeMS < 0 || *req.FadeMS > 30000 {
		writeError(w, 422, fmt.Errorf("fadeMs must be an integer between 0 and 30000"))
		return
	}
	st, err := s.RigCheck.SetPatternFade(time.Duration(*req.FadeMS) * time.Millisecond)
	if err != nil {
		writeRigCheckError(w, err)
		return
	}
	writeJSON(w, 200, s.patternStatusJSON(st))
}
