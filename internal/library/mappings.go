package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/freakyturtle/jellytrim/internal/jellyfin"
	"github.com/freakyturtle/jellytrim/internal/pathmap"
	"github.com/freakyturtle/jellytrim/internal/store"
)

// MappingCheck is the result of checking one path mapping.
type MappingCheck struct {
	Mapping  store.PathMapping
	Error    string // the mapping itself is invalid, or the folder is missing
	Sampled  int    // files Jellyfin reports under the mapping (a sample)
	Found    int    // of those, how many exist at the mapped path
	Missing  []string
	Writable bool
	HardLink bool
	Problem  string // why JellyTrim cannot replace files here, if it cannot
}

// OK reports whether JellyTrim can see and replace files under the mapping.
func (c MappingCheck) OK() bool {
	return c.Error == "" && c.Sampled > 0 && c.Found == c.Sampled && c.Writable
}

// SuggestMappings proposes a local folder for each library location, trying
// the same path and a few common container layouts.
func SuggestMappings(libs []store.Library, exists func(string) bool) []store.PathMapping {
	var out []store.PathMapping
	seen := map[string]bool{}
	for _, l := range libs {
		for _, loc := range l.Locations {
			if seen[loc] {
				continue
			}
			seen[loc] = true
			local := ""
			base := path.Base(strings.ReplaceAll(loc, `\`, "/"))
			for _, cand := range []string{loc, "/mnt/media/" + base, "/media/" + base, "/data/" + base, "/mnt/" + base, "/data/media/" + base} {
				if strings.HasPrefix(cand, "/") && exists(cand) {
					local = cand
					break
				}
			}
			out = append(out, store.PathMapping{JellyfinPrefix: loc, LocalPrefix: local})
		}
	}
	return out
}

// DirExists reports whether p is a directory. Used for suggestions.
func DirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// CheckMappings checks each mapping against a sample of real Jellyfin paths,
// and tests that JellyTrim can create, hard-link and rename a hidden probe
// file in the mapped folder. The probe file is removed afterwards.
func (s *Service) CheckMappings(ctx context.Context, ms []store.PathMapping) ([]MappingCheck, error) {
	c, err := s.Client(ctx)
	if err != nil {
		return nil, err
	}
	libs, err := s.store.Libraries(ctx)
	if err != nil {
		return nil, err
	}
	var paths []string
	users, _ := s.store.Users(ctx)
	uid := ""
	for _, u := range users {
		if !u.Disabled {
			uid = u.ID
			break
		}
	}
	for _, l := range libs {
		page, err := c.Items(ctx, jellyfin.ItemQuery{UserID: uid, ParentID: l.ID, Limit: 60, Fields: []string{"Path"}, NoImages: true})
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", l.Name, err)
		}
		for _, it := range page.Items {
			if it.Path != "" {
				paths = append(paths, it.Path)
			}
		}
	}
	out := make([]MappingCheck, 0, len(ms))
	for _, m := range ms {
		out = append(out, checkMapping(m, paths))
	}
	return out, nil
}

func checkMapping(m store.PathMapping, jfPaths []string) MappingCheck {
	chk := MappingCheck{Mapping: m}
	mapper, err := pathmap.New([]pathmap.Mapping{{Jellyfin: m.JellyfinPrefix, Local: m.LocalPrefix}})
	if err != nil {
		chk.Error = err.Error()
		return chk
	}
	fi, err := os.Stat(m.LocalPrefix)
	if err != nil || !fi.IsDir() {
		chk.Error = "JellyTrim cannot see a folder at " + m.LocalPrefix + ". Check the volume in your compose file."
		return chk
	}
	for _, p := range jfPaths {
		local, err := mapper.ToLocal(p)
		if err != nil {
			continue
		}
		chk.Sampled++
		if _, err := os.Stat(local); err == nil {
			chk.Found++
		} else if len(chk.Missing) < 3 {
			chk.Missing = append(chk.Missing, local)
		}
	}
	chk.Writable, chk.HardLink, chk.Problem = writeTest(m.LocalPrefix)
	return chk
}

// writeTest creates a hidden file, hard-links it and renames it, the same
// operations a replacement uses, then removes both names.
func writeTest(dir string) (writable, hardlink bool, problem string) {
	f, err := os.CreateTemp(dir, ".jellytrim-write-test-*")
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return false, false, "JellyTrim cannot write to " + dir + ". Run the container as a user that owns your media."
		}
		return false, false, "JellyTrim cannot write to " + dir + ": " + err.Error()
	}
	name := f.Name()
	_ = f.Close()
	defer func() { _ = os.Remove(name) }()
	link := name + ".link"
	hardlink = os.Link(name, link) == nil
	if hardlink {
		defer func() { _ = os.Remove(link) }()
	}
	renamed := filepath.Join(dir, filepath.Base(name)+".renamed")
	if err := os.Rename(name, renamed); err != nil {
		return true, hardlink, "Files in " + dir + " cannot be renamed: " + err.Error()
	}
	_ = os.Remove(renamed)
	if !hardlink {
		problem = "This folder does not support hard links, so backups will be made by renaming instead."
	}
	return true, hardlink, problem
}
