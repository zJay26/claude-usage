package server

import (
	"encoding/json"
	"github.com/zJay26/claude-usage/internal/sources"
	"net/http"
)

func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) {
	if s.LoadConfig == nil {
		writeJSON(w, http.StatusOK, map[string]any{"sources": []sources.Source{}})
		return
	}
	s.pricingMu.Lock()
	defer s.pricingMu.Unlock()
	cfg, err := s.LoadConfig()
	if err != nil {
		writeError(w, err)
		return
	}
	switch r.Method {
	case http.MethodGet:
	case http.MethodPut:
		if s.SaveConfig == nil {
			http.Error(w, "read only", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			ExtraClaudeHomes []string `json:"extra_claude_homes"`
			AutoDiscoverWSL  bool     `json:"auto_discover_wsl"`
			AutoStartWSL     bool     `json:"auto_start_wsl"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
		dec.DisallowUnknownFields()
		if err = dec.Decode(&in); err != nil {
			http.Error(w, "Invalid source settings", http.StatusBadRequest)
			return
		}
		if err = ensureJSONEOF(dec); err != nil {
			http.Error(w, "Invalid source settings", http.StatusBadRequest)
			return
		}
		cfg.ExtraClaudeHomes, cfg.AutoDiscoverWSL, cfg.AutoStartWSL = in.ExtraClaudeHomes, in.AutoDiscoverWSL, in.AutoStartWSL
		if err = s.SaveConfig(cfg); err != nil {
			writeError(w, err)
			return
		}
		sources.Invalidate()
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPut)
		return
	}
	// Source discovery itself runs with bounded subprocesses. A normal request
	// returns the current snapshot immediately, including previously failed roots.
	writeJSON(w, http.StatusOK, map[string]any{"sources": sources.Snapshot(), "extra_claude_homes": cfg.ExtraClaudeHomes, "auto_discover_wsl": cfg.AutoDiscoverWSL, "auto_start_wsl": cfg.AutoStartWSL})
}
