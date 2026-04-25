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

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
