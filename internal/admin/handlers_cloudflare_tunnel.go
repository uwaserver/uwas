// Cloudflare tunnel handler wrappers are now in handlers_cloudflare.go.
// This file retains the tunnelView type for backward compat with tests.
package admin

import "time"

type tunnelView struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Hostname    string    `json:"hostname"`
	LocalTarget string    `json:"local_target"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	Running     bool      `json:"running"`
	PID         int       `json:"pid,omitempty"`
	Uptime      string    `json:"uptime,omitempty"`
}
