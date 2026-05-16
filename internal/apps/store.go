package apps

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultDir is where standalone app definitions live on disk. One YAML
// file per app, basename = app name. Mirrors the layout of domains.d/.
const DefaultDir = "/etc/uwas/apps.d"

// DefaultDataRoot is the parent directory under which each app gets a
// /var/lib/uwas/apps/<name>/ workdir when WorkDir isn't set explicitly.
// Operators can override via Store.DataRoot.
const DefaultDataRoot = "/var/lib/uwas/apps"

// Store is the on-disk persistence layer for apps. All file I/O is
// serialized through the mutex so concurrent Save/Delete don't race a
// load. Reads (Get/List) snapshot under read-lock so the caller can
// release before iterating.
//
// The store deliberately does NOT cache the YAML in memory beyond the
// indexed map of names. Each Get re-reads the file so an out-of-band
// edit (operator hand-tweaking /etc/uwas/apps.d/<name>.yaml) is picked
// up on the next manager poll. The map exists only to enumerate names
// without a directory listing on every call.
type Store struct {
	mu sync.RWMutex

	// Dir is the apps.d directory. Defaults to DefaultDir.
	Dir string

	// DataRoot is the parent under which auto-assigned WorkDir paths
	// live. Defaults to DefaultDataRoot.
	DataRoot string

	// names caches the set of known app names (basenames of *.yaml in
	// Dir). Refreshed on every Load() call.
	names map[string]struct{}
}

// NewStore returns a store rooted at dir. Empty dir falls back to
// DefaultDir. The store does NOT create the directory at construction —
// EnsureDir() (called by Load and Save) handles that lazily so unit
// tests can poke at a Store without touching the filesystem.
func NewStore(dir string) *Store {
	if dir == "" {
		dir = DefaultDir
	}
	return &Store{
		Dir:      dir,
		DataRoot: DefaultDataRoot,
		names:    make(map[string]struct{}),
	}
}

// EnsureDir creates the apps.d directory tree if it doesn't exist. 0755
// because operators may need to ls; the YAML files inside get 0600.
func (s *Store) EnsureDir() error {
	if err := os.MkdirAll(s.Dir, 0755); err != nil {
		return fmt.Errorf("apps: create dir %s: %w", s.Dir, err)
	}
	return nil
}

// pathFor returns the absolute YAML path for an app name. The name
// itself is validated by the caller (Validate) so this is a pure join.
func (s *Store) pathFor(name string) string {
	return filepath.Join(s.Dir, name+".yaml")
}

// Load scans the apps directory and returns every valid App definition
// it finds. Invalid files (bad YAML, schema violation, name mismatch
// against filename) are skipped with a returned error slice so a single
// corrupt file doesn't take down the whole manager — the caller logs
// and continues.
//
// The returned []*App is sorted by name for stable output (matters for
// tests and the dashboard's app list rendering).
func (s *Store) Load() ([]*App, []error, error) {
	if err := s.EnsureDir(); err != nil {
		return nil, nil, err
	}

	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, nil, fmt.Errorf("apps: read dir %s: %w", s.Dir, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Rebuild the name index from scratch.
	s.names = make(map[string]struct{}, len(entries))

	apps := make([]*App, 0, len(entries))
	var skipErrs []error

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		baseName := strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml")

		full := filepath.Join(s.Dir, name)
		app, err := readAppFile(full)
		if err != nil {
			skipErrs = append(skipErrs, fmt.Errorf("apps: %s: %w", name, err))
			continue
		}

		// Filename must agree with the in-file Name field; mismatches
		// are user error and would silently break Get(name) lookups.
		if app.Name == "" {
			app.Name = baseName
		} else if app.Name != baseName {
			skipErrs = append(skipErrs, fmt.Errorf(
				"apps: %s: name %q does not match filename basename %q (rename one to match)",
				name, app.Name, baseName))
			continue
		}

		if err := app.Validate(); err != nil {
			skipErrs = append(skipErrs, fmt.Errorf("apps: %s: %w", name, err))
			continue
		}

		s.applyDefaults(app)
		s.names[app.Name] = struct{}{}
		apps = append(apps, app)
	}

	// Stable name-sort for deterministic output.
	for i := 1; i < len(apps); i++ {
		for j := i; j > 0 && apps[j-1].Name > apps[j].Name; j-- {
			apps[j-1], apps[j] = apps[j], apps[j-1]
		}
	}

	return apps, skipErrs, nil
}

// applyDefaults fills in WorkDir for an app that didn't pin one. Called
// from Load() and Save() so the on-disk representation always carries
// the fully-resolved value once the operator has touched the app via
// the API (which round-trips through Save).
func (s *Store) applyDefaults(a *App) {
	if a.WorkDir == "" {
		a.WorkDir = s.DefaultWorkDir(a.Name)
	}
}

// DefaultWorkDir returns the workdir used for a named app when the
// operator does not provide one explicitly.
func (s *Store) DefaultWorkDir(name string) string {
	return filepath.Join(s.DataRoot, name)
}

// Get reads a single app by name. Returns nil with no error if the
// file does not exist — callers distinguish "missing" from "broken"
// by inspecting the error.
func (s *Store) Get(name string) (*App, error) {
	if !isValidAppName(name) {
		return nil, fmt.Errorf("apps: invalid name %q", name)
	}

	full := s.pathFor(name)
	app, err := readAppFile(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if app.Name == "" {
		app.Name = name
	}
	if app.Name != name {
		return nil, fmt.Errorf("apps: %s.yaml has name %q (mismatch)", name, app.Name)
	}
	if err := app.Validate(); err != nil {
		return nil, err
	}
	s.applyDefaults(app)
	return app, nil
}

// Exists is a cheap test for "is there an app file with this name on
// disk". Does not parse the YAML so a malformed file still reports
// true — callers that need a validated app should use Get.
func (s *Store) Exists(name string) bool {
	if !isValidAppName(name) {
		return false
	}
	_, err := os.Stat(s.pathFor(name))
	return err == nil
}

// Save writes an app to disk atomically (temp+rename) with 0600 perms
// so env vars containing secrets aren't world-readable. Updates the
// CreatedAt/UpdatedAt timestamps in-place on the passed App: brand-new
// records get both stamped to "now", existing records get UpdatedAt
// bumped while CreatedAt is preserved from whatever's already on disk.
//
// Save validates the App before writing so a corrupt schema can't be
// persisted.
func (s *Store) Save(a *App) error {
	if a == nil {
		return fmt.Errorf("apps: cannot save nil app")
	}
	if err := a.Validate(); err != nil {
		return err
	}

	if err := s.EnsureDir(); err != nil {
		return err
	}

	s.applyDefaults(a)

	now := time.Now().UTC()
	if a.CreatedAt.IsZero() {
		// Look on-disk for an existing record — operator may have hand
		// removed CreatedAt from the YAML, in which case we preserve
		// the original timestamp rather than re-stamping to now.
		if existing, err := s.Get(a.Name); err == nil && existing != nil && !existing.CreatedAt.IsZero() {
			a.CreatedAt = existing.CreatedAt
		} else {
			a.CreatedAt = now
		}
	}
	a.UpdatedAt = now

	data, err := yaml.Marshal(a)
	if err != nil {
		return fmt.Errorf("apps: marshal %s: %w", a.Name, err)
	}

	full := s.pathFor(a.Name)
	tmp := full + ".tmp"

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("apps: write tmp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, full); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("apps: rename %s → %s: %w", tmp, full, err)
	}
	s.names[a.Name] = struct{}{}
	return nil
}

// Delete removes an app's YAML file. Idempotent — deleting a missing
// file returns nil so the caller doesn't have to pre-check Exists().
// The caller is responsible for stopping the app's process and tearing
// down its WorkDir if applicable; Delete is concerned only with the
// definition file.
func (s *Store) Delete(name string) error {
	if !isValidAppName(name) {
		return fmt.Errorf("apps: invalid name %q", name)
	}

	full := s.pathFor(name)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.Remove(full); err != nil {
		if os.IsNotExist(err) {
			delete(s.names, name)
			return nil
		}
		return fmt.Errorf("apps: remove %s: %w", full, err)
	}
	delete(s.names, name)
	return nil
}

// Names returns the snapshot of cached app names. Cheap — no I/O. Call
// Load() first to populate the cache.
func (s *Store) Names() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.names))
	for n := range s.names {
		out = append(out, n)
	}
	// Stable sort
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// readAppFile is the single point of YAML unmarshaling. Centralized so
// the same strict-mode decoder settings apply everywhere.
func readAppFile(path string) (*App, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var app App
	if err := yaml.Unmarshal(data, &app); err != nil {
		return nil, fmt.Errorf("yaml: %w", err)
	}
	return &app, nil
}
