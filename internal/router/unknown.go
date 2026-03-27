package router

import (
	"bufio"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// UnknownHostEntry tracks a hostname that hit the server but isn't configured.
type UnknownHostEntry struct {
	Host      string    `json:"host"`
	Hits      int64     `json:"hits"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Blocked   bool      `json:"blocked"`
}

// UnknownHostTracker records hostnames that don't match any configured domain.
type UnknownHostTracker struct {
	mu       sync.RWMutex
	hosts    map[string]*UnknownHostEntry
	blocked  map[string]bool // quick lookup for blocked hosts
	filePath string          // path to persist blocked hosts (empty = no persistence)
}

// NewUnknownHostTracker creates a new tracker.
func NewUnknownHostTracker() *UnknownHostTracker {
	return &UnknownHostTracker{
		hosts:   make(map[string]*UnknownHostEntry),
		blocked: make(map[string]bool),
	}
}

// SetPersistPath sets the file path for saving/loading blocked hosts.
// Call this after creation and before any Block calls.
func (t *UnknownHostTracker) SetPersistPath(path string) {
	t.filePath = path
	t.loadBlocked()
}

// Record increments the hit count for an unknown host. Returns true if the host is blocked.
func (t *UnknownHostTracker) Record(host string) bool {
	// Strip port
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	host = strings.ToLower(host)
	if host == "" {
		return false
	}

	now := time.Now()

	t.mu.Lock()
	defer t.mu.Unlock()

	if e, ok := t.hosts[host]; ok {
		e.Hits++
		e.LastSeen = now
		return e.Blocked
	}

	blocked := t.blocked[host]
	t.hosts[host] = &UnknownHostEntry{
		Host:      host,
		Hits:      1,
		FirstSeen: now,
		LastSeen:  now,
		Blocked:   blocked,
	}
	return blocked
}

// IsBlocked returns true if the host is on the block list.
func (t *UnknownHostTracker) IsBlocked(host string) bool {
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	host = strings.ToLower(host)

	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.blocked[host]
}

// Block adds a host to the block list and persists to disk.
func (t *UnknownHostTracker) Block(host string) {
	host = strings.ToLower(host)

	t.mu.Lock()
	t.blocked[host] = true
	if e, ok := t.hosts[host]; ok {
		e.Blocked = true
	}
	t.mu.Unlock()
	t.saveBlocked()
}

// Unblock removes a host from the block list and persists to disk.
func (t *UnknownHostTracker) Unblock(host string) {
	host = strings.ToLower(host)

	t.mu.Lock()
	delete(t.blocked, host)
	if e, ok := t.hosts[host]; ok {
		e.Blocked = false
	}
	t.mu.Unlock()
	t.saveBlocked()
}

// Dismiss removes a host from tracking entirely.
func (t *UnknownHostTracker) Dismiss(host string) {
	host = strings.ToLower(host)

	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.hosts, host)
	delete(t.blocked, host)
}

// List returns all tracked unknown hosts sorted by hit count descending.
func (t *UnknownHostTracker) List() []UnknownHostEntry {
	t.mu.RLock()
	defer t.mu.RUnlock()

	entries := make([]UnknownHostEntry, 0, len(t.hosts))
	for _, e := range t.hosts {
		entries = append(entries, *e)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Hits > entries[j].Hits
	})
	return entries
}

// BlockedHosts returns just the list of blocked hostnames.
func (t *UnknownHostTracker) BlockedHosts() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	hosts := make([]string, 0, len(t.blocked))
	for h := range t.blocked {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	return hosts
}

// saveBlocked writes the blocked hosts list to disk.
func (t *UnknownHostTracker) saveBlocked() {
	if t.filePath == "" {
		return
	}
	t.mu.RLock()
	hosts := make([]string, 0, len(t.blocked))
	for h := range t.blocked {
		hosts = append(hosts, h)
	}
	t.mu.RUnlock()

	sort.Strings(hosts)
	f, err := os.Create(t.filePath)
	if err != nil {
		return
	}
	defer f.Close()
	for _, h := range hosts {
		f.WriteString(h + "\n")
	}
}

// loadBlocked reads the blocked hosts list from disk.
func (t *UnknownHostTracker) loadBlocked() {
	if t.filePath == "" {
		return
	}
	f, err := os.Open(t.filePath)
	if err != nil {
		return
	}
	defer f.Close()

	t.mu.Lock()
	defer t.mu.Unlock()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		host := strings.TrimSpace(scanner.Text())
		if host != "" && !strings.HasPrefix(host, "#") {
			t.blocked[host] = true
		}
	}
}
