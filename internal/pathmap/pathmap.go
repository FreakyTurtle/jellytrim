// Package pathmap translates file paths between what Jellyfin sees and what
// JellyTrim sees. The two usually differ when they run in separate
// containers: /media/movies in Jellyfin may be /mnt/media/movies here.
//
// Matching is by whole path segments and the longest prefix wins. Paths with
// ".." segments are rejected rather than cleaned, because a Jellyfin path
// containing them is either malformed or hostile.
package pathmap

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
)

// Mapping pairs a Jellyfin path prefix with the local path prefix that holds
// the same files.
type Mapping struct {
	Jellyfin string
	Local    string
}

// Errors returned by New and the translation functions.
var (
	ErrInvalid  = errors.New("invalid path mapping")
	ErrUnmapped = errors.New("no path mapping covers this path")
	ErrUnsafe   = errors.New("path contains '..' or is not absolute")
)

type rule struct {
	jf      string // normalised with forward slashes, no trailing slash
	local   string // cleaned, absolute
	windows bool   // Jellyfin runs on Windows: backslashes and case-insensitive
}

// Mapper translates paths using a fixed set of mappings.
type Mapper struct {
	byJF    []rule // longest Jellyfin prefix first
	byLocal []rule // longest local prefix first
}

// New validates the mappings and builds a Mapper.
func New(ms []Mapping) (*Mapper, error) {
	m := &Mapper{}
	seen := map[string]bool{}
	for i, mp := range ms {
		r, err := newRule(mp)
		if err != nil {
			return nil, fmt.Errorf("%w: mapping %d: %v", ErrInvalid, i+1, err)
		}
		key := r.jf
		if r.windows {
			key = strings.ToLower(key)
		}
		if seen[key] {
			return nil, fmt.Errorf("%w: Jellyfin path %q is mapped twice", ErrInvalid, mp.Jellyfin)
		}
		seen[key] = true
		m.byJF = append(m.byJF, r)
	}
	m.byLocal = append([]rule(nil), m.byJF...)
	sort.SliceStable(m.byJF, func(i, j int) bool { return len(m.byJF[i].jf) > len(m.byJF[j].jf) })
	sort.SliceStable(m.byLocal, func(i, j int) bool { return len(m.byLocal[i].local) > len(m.byLocal[j].local) })
	return m, nil
}

func newRule(mp Mapping) (rule, error) {
	jf := strings.TrimSpace(mp.Jellyfin)
	local := strings.TrimSpace(mp.Local)
	if jf == "" || local == "" {
		return rule{}, errors.New("both paths are required")
	}
	win := isWindows(jf)
	njf := normalise(jf)
	if !safe(njf) {
		return rule{}, fmt.Errorf("the Jellyfin path %q: %w", jf, ErrUnsafe)
	}
	if !path.IsAbs(local) || !safe(local) {
		return rule{}, fmt.Errorf("local path %q: %w", local, ErrUnsafe)
	}
	return rule{jf: strings.TrimSuffix(njf, "/"), local: path.Clean(local), windows: win}, nil
}

// isWindows reports whether a Jellyfin path is a Windows path (C:\ or \\server).
func isWindows(p string) bool {
	if strings.HasPrefix(p, `\\`) {
		return true
	}
	return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

// normalise converts a Jellyfin path to forward slashes. It does not clean
// the path, so ".." segments survive to be rejected by safe.
func normalise(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	if len(p) >= 2 && p[1] == ':' {
		p = "/" + p // "C:/Media" becomes "/C:/Media" so it is absolute
	}
	for strings.Contains(p, "//") && !strings.HasPrefix(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	return p
}

// safe reports whether p is absolute and has no "." or ".." segments.
func safe(p string) bool {
	if !strings.HasPrefix(p, "/") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." || seg == "." {
			return false
		}
	}
	return true
}

// hasPrefix reports whether p equals prefix or starts with prefix + "/".
func hasPrefix(p, prefix string, fold bool) bool {
	if fold {
		p, prefix = strings.ToLower(p), strings.ToLower(prefix)
	}
	if prefix == "/" {
		return strings.HasPrefix(p, "/")
	}
	return p == prefix || strings.HasPrefix(p, prefix+"/")
}

// ToLocal maps a path reported by Jellyfin to the path JellyTrim can open.
func (m *Mapper) ToLocal(jellyfinPath string) (string, error) {
	np := normalise(jellyfinPath)
	if !safe(np) {
		return "", fmt.Errorf("%q: %w", jellyfinPath, ErrUnsafe)
	}
	for _, r := range m.byJF {
		if hasPrefix(np, r.jf, r.windows) {
			rest := strings.TrimPrefix(np[len(r.jf):], "/")
			if rest == "" {
				return r.local, nil
			}
			return r.local + "/" + rest, nil
		}
	}
	return "", fmt.Errorf("%q: %w", jellyfinPath, ErrUnmapped)
}

// ToJellyfin maps a local path back to the path Jellyfin knows, for rescans.
func (m *Mapper) ToJellyfin(localPath string) (string, error) {
	if !safe(localPath) {
		return "", fmt.Errorf("%q: %w", localPath, ErrUnsafe)
	}
	for _, r := range m.byLocal {
		if !hasPrefix(localPath, r.local, false) {
			continue
		}
		rest := strings.TrimPrefix(localPath[len(r.local):], "/")
		out := r.jf
		if rest != "" {
			out = strings.TrimSuffix(out, "/") + "/" + rest
		}
		if r.windows {
			// Undo normalise: "/C:/Media" back to "C:\Media", "//server" to "\\server".
			if !strings.HasPrefix(out, "//") {
				out = strings.TrimPrefix(out, "/")
			}
			out = strings.ReplaceAll(out, "/", `\`)
		}
		return out, nil
	}
	return "", fmt.Errorf("%q: %w", localPath, ErrUnmapped)
}

// Roots returns the local prefixes. JellyTrim only touches files inside them.
func (m *Mapper) Roots() []string {
	out := make([]string, 0, len(m.byLocal))
	for _, r := range m.byLocal {
		out = append(out, r.local)
	}
	sort.Strings(out)
	return out
}

// Within reports whether p is inside one of roots. p should already have had
// symlinks resolved by the caller.
func Within(roots []string, p string) bool {
	if !safe(p) {
		return false
	}
	for _, root := range roots {
		if hasPrefix(p, root, false) {
			return true
		}
	}
	return false
}
