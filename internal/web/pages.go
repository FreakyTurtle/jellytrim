package web

import (
	"errors"
	"net/http"

	"github.com/freakyturtle/jellytrim/internal/store"
	"github.com/freakyturtle/jellytrim/internal/units"
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
		QueueCount:   s.queueCount(r),
		Sync:         s.syncState(r),
		AssetVersion: assetVersion(),
	}, nil
}

// syncState describes the last or current sync for the top bar.
func (s *Server) syncState(r *http.Request) views.SyncStatus {
	if s.Library != nil {
		if st := s.Library.Status(); st.Running {
			return views.SyncStatus{State: "running", Text: "Syncing"}
		} else if st.LastError != "" {
			return views.SyncStatus{State: "error", Text: "Last sync failed"}
		}
	}
	last, err := s.Store.LastSync(r.Context())
	switch {
	case errors.Is(err, store.ErrNotFound):
		return views.SyncStatus{State: "never", Text: "Not synced yet"}
	case err != nil:
		return views.SyncStatus{State: "error", Text: "Sync status unknown"}
	case last.Status == "failed":
		return views.SyncStatus{State: "error", Text: "Last sync failed"}
	case last.FinishedAt != nil:
		return views.SyncStatus{State: "ok", Text: "Synced " + units.Ago(*last.FinishedAt, s.Now())}
	}
	return views.SyncStatus{State: "running", Text: "Syncing"}
}

// queueCount is the number of waiting and running jobs, for the nav badge.
func (s *Server) queueCount(r *http.Request) int {
	n, err := s.Store.ActiveJobCount(r.Context())
	if err != nil {
		return 0
	}
	return n
}

func (s *Server) styleguide(w http.ResponseWriter, r *http.Request) {
	sh, err := s.shell(r, "Style guide", "")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, views.Styleguide(sh))
}
