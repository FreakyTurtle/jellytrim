package web

import (
	"net/http"

	"github.com/freakyturtle/jellytrim/internal/web/views"
)

// shell builds the layout data shared by every page.
func (s *Server) shell(r *http.Request, title, active string) (views.Shell, error) {
	st, err := s.Store.Settings(r.Context())
	if err != nil {
		return views.Shell{}, err
	}
	return views.Shell{
		Title:        title,
		Active:       active,
		DryRun:       st.DryRun,
		Sync:         views.SyncStatus{State: "never", Text: "Not synced yet"},
		AssetVersion: assetVersion(),
	}, nil
}

func (s *Server) placeholder(w http.ResponseWriter, r *http.Request, title, active, description string) {
	sh, err := s.shell(r, title, active)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, views.PlaceholderPage(sh, title, description))
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	s.placeholder(w, r, "Dashboard", "dashboard", "Storage used, what JellyTrim could save, and what it is doing.")
}

func (s *Server) library(w http.ResponseWriter, r *http.Request) {
	s.placeholder(w, r, "Library", "library", "Your Jellyfin media and what JellyTrim plans for each item.")
}

func (s *Server) policies(w http.ResponseWriter, r *http.Request) {
	s.placeholder(w, r, "Policies", "policies", "Rules that describe what to optimise, and when.")
}

func (s *Server) queue(w http.ResponseWriter, r *http.Request) {
	s.placeholder(w, r, "Queue", "queue", "Files being optimised now and waiting their turn.")
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	s.placeholder(w, r, "History", "history", "Everything JellyTrim has done, skipped or failed to do.")
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	s.placeholder(w, r, "Settings", "settings", "Jellyfin connection, paths, hardware and safety settings.")
}

func (s *Server) styleguide(w http.ResponseWriter, r *http.Request) {
	sh, err := s.shell(r, "Style guide", "")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, views.Styleguide(sh))
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	s.placeholder(w, r, "Set up JellyTrim", "", "The setup wizard arrives in milestone M2.")
}
