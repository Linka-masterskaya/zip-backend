package db

import "strings"

// EscapeLike makes user input match literally inside LIKE/ILIKE patterns:
// without it "%" and "_" act as wildcards and a search for "_" matches
// everything. Use together with ESCAPE E'\\' in SQL.
func EscapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	value = strings.ReplaceAll(value, `_`, `\_`)
	return value
}
