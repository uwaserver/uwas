// Database optimization for WordPress: prune post revisions, spam
// comments, trashed posts, expired transients, then OPTIMIZE TABLE
// across all wp_* tables. Split out of installer.go per refactor.md
// A13.
package wordpress

import (
	"fmt"
	"strings"
)

// --- Database Optimization ---

// DBOptimizeResult holds results of database cleanup.
type DBOptimizeResult struct {
	RevisionsDeleted  int    `json:"revisions_deleted"`
	SpamDeleted       int    `json:"spam_deleted"`
	TrashDeleted      int    `json:"trash_deleted"`
	TransientsCleaned int    `json:"transients_cleaned"`
	TablesOptimized   int    `json:"tables_optimized"`
	Output            string `json:"output"`
}

// OptimizeDatabase cleans up and optimizes the WordPress database.
func OptimizeDatabase(webRoot string) (*DBOptimizeResult, error) {
	result := &DBOptimizeResult{}
	var log strings.Builder

	// Delete post revisions (two-step: get IDs, then delete)
	if ids, err := wpCLI(webRoot, "post", "list", "--post_type=revision", "--format=ids"); err == nil {
		ids = strings.TrimSpace(ids)
		if ids != "" {
			count := len(strings.Fields(ids))
			for _, id := range strings.Fields(ids) {
				wpCLI(webRoot, "post", "delete", id, "--force")
			}
			result.RevisionsDeleted = count
			log.WriteString(fmt.Sprintf("Revisions deleted: %d\n", count))
		}
	}

	// Delete spam comments
	if ids, err := wpCLI(webRoot, "comment", "list", "--status=spam", "--format=ids"); err == nil {
		ids = strings.TrimSpace(ids)
		if ids != "" {
			count := len(strings.Fields(ids))
			for _, id := range strings.Fields(ids) {
				wpCLI(webRoot, "comment", "delete", id, "--force")
			}
			result.SpamDeleted = count
			log.WriteString(fmt.Sprintf("Spam comments deleted: %d\n", count))
		}
	}

	// Delete trashed comments
	if ids, err := wpCLI(webRoot, "comment", "list", "--status=trash", "--format=ids"); err == nil {
		ids = strings.TrimSpace(ids)
		if ids != "" {
			for _, id := range strings.Fields(ids) {
				wpCLI(webRoot, "comment", "delete", id, "--force")
			}
			log.WriteString(fmt.Sprintf("Trash comments deleted: %d\n", len(strings.Fields(ids))))
		}
	}

	// Delete trashed posts
	if ids, err := wpCLI(webRoot, "post", "list", "--post_status=trash", "--format=ids"); err == nil {
		ids = strings.TrimSpace(ids)
		if ids != "" {
			count := len(strings.Fields(ids))
			for _, id := range strings.Fields(ids) {
				wpCLI(webRoot, "post", "delete", id, "--force")
			}
			result.TrashDeleted = count
			log.WriteString(fmt.Sprintf("Trash posts deleted: %d\n", count))
		}
	}

	// Clean expired transients
	if out, err := wpCLI(webRoot, "transient", "delete", "--expired"); err == nil {
		log.WriteString("Transients: " + strings.TrimSpace(out) + "\n")
		result.TransientsCleaned = 1
	}

	// Optimize database tables
	if out, err := wpCLI(webRoot, "db", "optimize"); err == nil {
		log.WriteString("Tables optimized\n")
		_ = out
		result.TablesOptimized = 1
	}

	result.Output = log.String()
	return result, nil
}
