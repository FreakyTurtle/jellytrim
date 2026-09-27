package jellyfintest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// FixtureDir returns internal/jellyfin/testdata, which holds JSON captured
// from a Jellyfin 12.1 dev server. It is found from this source file so it
// works from any package's tests.
func FixtureDir() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("cannot locate the jellyfintest source directory")
	}
	return filepath.Join(filepath.Dir(file), "..", "testdata"), nil
}

func readFixture(name string, v any) error {
	dir, err := FixtureDir()
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("opening the fixture directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	b, err := root.ReadFile(name)
	if err != nil {
		return fmt.Errorf("reading %s: %w", name, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("decoding %s: %w", name, err)
	}
	return nil
}

// fixtureUser is the shape of one entry in users.json.
type fixtureUser struct {
	ID               string     `json:"Id"`
	Name             string     `json:"Name"`
	LastActivityDate *time.Time `json:"LastActivityDate"`
	Policy           struct {
		IsAdministrator bool `json:"IsAdministrator"`
		IsDisabled      bool `json:"IsDisabled"`
		IsHidden        bool `json:"IsHidden"`
	} `json:"Policy"`
}

// fixtureItem adds the user data captured with each item.
type fixtureItem struct {
	Item
	UserData *struct {
		Played                bool       `json:"Played"`
		PlayCount             int        `json:"PlayCount"`
		IsFavorite            bool       `json:"IsFavorite"`
		LastPlayedDate        *time.Time `json:"LastPlayedDate"`
		PlaybackPositionTicks int64      `json:"PlaybackPositionTicks"`
	} `json:"UserData"`
}

// loadFixtures loads libraries, users and items (with the capturing user's
// watch state) from the captured JSON.
func (s *Server) loadFixtures() error {
	var libs []Library
	if err := readFixture("virtual_folders.json", &libs); err != nil {
		return err
	}
	var users []fixtureUser
	if err := readFixture("users.json", &users); err != nil {
		return err
	}
	var page struct {
		Items []fixtureItem `json:"Items"`
	}
	if err := readFixture("items.json", &page); err != nil {
		return err
	}
	if len(users) == 0 {
		return errors.New("users.json has no users")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.libraries = append(s.libraries, libs...)
	for _, u := range users {
		s.users = append(s.users, User{
			ID: u.ID, Name: u.Name, LastActivityDate: u.LastActivityDate,
			IsAdministrator: u.Policy.IsAdministrator, IsDisabled: u.Policy.IsDisabled, IsHidden: u.Policy.IsHidden,
		})
	}
	// items.json was captured with the first user's ID.
	owner := users[0].ID
	for _, fi := range page.Items {
		s.addItemLocked(fi.Item)
		if ud := fi.UserData; ud != nil {
			s.setUserDataLocked(owner, fi.ID, UserData{
				Played: ud.Played, PlayCount: ud.PlayCount, IsFavorite: ud.IsFavorite,
				LastPlayedDate: ud.LastPlayedDate, PlaybackPositionTicks: ud.PlaybackPositionTicks,
			})
		}
	}
	return nil
}

// FixtureUserID is the ID of the user in the captured fixtures.
const FixtureUserID = "26e2f832364644b5b401d1d53f240436"

// Fixture library IDs from the captured fixtures.
const (
	FixtureMoviesLibraryID = "f137a2dd21bbc1b99aa5c0f6bf02a805"
	FixtureTVLibraryID     = "4514ec850e5ad0c47b58444e17b6346c"
)
