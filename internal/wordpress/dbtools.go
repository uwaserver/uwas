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

// OptimizeDatabase cleans up and optimizes the WordPress database. A step that
// fails is named in Output and not counted; when no wp-cli call succeeded at
// all (wp-cli missing, database unreachable, not a WordPress root) the error is
// returned instead of an all-zero "success".
func OptimizeDatabase(webRoot string) (*DBOptimizeResult, error) {
	result := &DBOptimizeResult{}
	var log strings.Builder
	var firstErr error
	succeeded := 0

	// run wraps wpCLI and keeps track of the outcome of every call.
	run := func(args ...string) (string, error) {
		out, err := wpCLI(webRoot, args...)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return out, err
		}
		succeeded++
		return out, nil
	}

	// purge lists ids with listArgs and deletes each one with delArgs.
	// It returns how many were deleted; ok is false when the listing failed.
	purge := func(label string, listArgs []string, delArgs ...string) (deleted int, ok bool) {
		ids, err := run(listArgs...)
		if err != nil {
			log.WriteString(fmt.Sprintf("%s: list failed: %v\n", label, err))
			return 0, false
		}
		all := strings.Fields(ids)
		if len(all) == 0 {
			return 0, true
		}
		for _, id := range all {
			args := append(append([]string{}, delArgs...), id, "--force")
			if _, err := run(args...); err == nil {
				deleted++
			}
		}
		line := fmt.Sprintf("%s: %d", label, deleted)
		if failed := len(all) - deleted; failed > 0 {
			line += fmt.Sprintf(" (%d failed)", failed)
		}
		log.WriteString(line + "\n")
		return deleted, true
	}

	// Delete post revisions (two-step: get IDs, then delete)
	if n, ok := purge("Revisions deleted", []string{"post", "list", "--post_type=revision", "--format=ids"}, "post", "delete"); ok {
		result.RevisionsDeleted = n
	}
	// Delete spam comments
	if n, ok := purge("Spam comments deleted", []string{"comment", "list", "--status=spam", "--format=ids"}, "comment", "delete"); ok {
		result.SpamDeleted = n
	}
	// Delete trashed comments
	purge("Trash comments deleted", []string{"comment", "list", "--status=trash", "--format=ids"}, "comment", "delete")
	// Delete trashed posts
	if n, ok := purge("Trash posts deleted", []string{"post", "list", "--post_status=trash", "--format=ids"}, "post", "delete"); ok {
		result.TrashDeleted = n
	}

	// Clean expired transients
	if out, err := run("transient", "delete", "--expired"); err == nil {
		log.WriteString("Transients: " + strings.TrimSpace(out) + "\n")
		result.TransientsCleaned = 1
	} else {
		log.WriteString(fmt.Sprintf("Transients: failed: %v\n", err))
	}

	// Optimize database tables
	if _, err := run("db", "optimize"); err == nil {
		log.WriteString("Tables optimized\n")
		result.TablesOptimized = 1
	} else {
		log.WriteString(fmt.Sprintf("Tables optimize: failed: %v\n", err))
	}

	result.Output = log.String()
	if succeeded == 0 && firstErr != nil {
		return result, fmt.Errorf("wp-cli could not run: %w", firstErr)
	}
	return result, nil
}
