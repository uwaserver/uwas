package migrate

import (
	"strings"

	"github.com/uwaserver/uwas/internal/database"
)

func validMigrateDBIdentifier(s string) bool {
	return database.ValidDBIdentifier(s)
}

func sqlIdent(s string) string {
	return database.BacktickID(s)
}

func sqlString(s string) string {
	return database.EscapeSQL(s)
}

// safeMigratePrefix strips any non-alphanumeric characters from s so it is
// safe to embed in an os.CreateTemp prefix (which interprets directory separators
// in the prefix as subdirectory components in the temp tree).
func safeMigratePrefix(s string) string {
	var b strings.Builder
	for _, c := range s {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			b.WriteRune(c)
		}
	}
	if b.Len() == 0 {
		return "unnamed"
	}
	return b.String()
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
