package main

import (
	"strings"
	"unicode/utf8"

	"cloud.google.com/go/spanner/admin/database/apiv1/databasepb"
)

// Prefix and line-comment scanning follows go-sql-spanner's dialect rules.
// Those helpers are unexported, so this file only recognizes whitespace,
// comments, and literals. It does not parse SQL grammar.
//
// GoogleSQL: '#' line comments, no nested block comments, backtick quotes,
// triple quotes, and backslash escapes.
// PostgreSQL: '--' line comments, nested block comments, and dollar quotes.
// A '/*@' comment is a PostgreSQL statement hint and is not skipped, matching
// the driver's hint handling.

func stripExplainPrefix(sql string, dialect databasepb.DatabaseDialect) (stmtDisplayKind, string, bool) {
	// Do not skip PostgreSQL /*@ hints. They belong to the statement, and the
	// driver does not treat them as ordinary comments.
	pos := skipSpaceAndComments(sql, 0, dialect, false)
	keyword, after := readKeyword(sql, pos)
	if !keywordIs(keyword, "EXPLAIN") {
		return 0, "", false
	}
	peek := skipSpaceAndComments(sql, after, dialect, false)
	next, afterNext := readKeyword(sql, peek)
	if keywordIs(next, "ANALYZE") {
		return stmtDisplayPlanOnlyProfile, sql[afterNext:], true
	}
	return stmtDisplayPlanOnlyPlan, sql[after:], true
}

func statementEndsInOpenLineComment(sql string, dialect databasepb.DatabaseDialect) bool {
	pos := 0
	for pos < len(sql) {
		if isLineCommentStart(sql, pos, dialect) {
			next, closed := skipLineComment(sql, pos)
			if !closed {
				return true
			}
			pos = next
			continue
		}
		if sql[pos] == '/' && pos+1 < len(sql) && sql[pos+1] == '*' {
			pos = skipBlockComment(sql, pos, dialect)
			continue
		}
		if next, ok := skipLiteral(sql, pos, dialect); ok {
			if next == pos {
				return false
			}
			pos = next
			continue
		}
		pos = skipRune(sql, pos)
	}
	return false
}

func keywordIs(got, want string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := 0; i < len(got); i++ {
		c := got[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		if c != want[i] {
			return false
		}
	}
	return true
}

func readKeyword(sql string, pos int) (string, int) {
	start := pos
	for pos < len(sql) {
		c := sql[pos]
		if c > 0x7F || !isKeywordByte(c) {
			break
		}
		pos++
	}
	return sql[start:pos], pos
}

func isKeywordByte(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_'
}

func skipSpaceAndComments(sql string, pos int, dialect databasepb.DatabaseDialect, skipPGHints bool) int {
	for pos < len(sql) {
		c := sql[pos]
		if c > 0x7F {
			break
		}
		if c == '-' && pos+1 < len(sql) && sql[pos+1] == '-' {
			next, _ := skipLineComment(sql, pos)
			pos = next
			continue
		}
		if c == '#' && !postgres(dialect) {
			next, _ := skipLineComment(sql, pos)
			pos = next
			continue
		}
		if c == '/' && pos+1 < len(sql) && sql[pos+1] == '*' {
			if !skipPGHints && postgres(dialect) && pos+2 < len(sql) && sql[pos+2] == '@' {
				break
			}
			pos = skipBlockComment(sql, pos, dialect)
			continue
		}
		if !isSQLSpace(c) {
			break
		}
		pos++
	}
	return pos
}

func isLineCommentStart(sql string, pos int, dialect databasepb.DatabaseDialect) bool {
	if pos >= len(sql) || sql[pos] > 0x7F {
		return false
	}
	if sql[pos] == '-' && pos+1 < len(sql) && sql[pos+1] == '-' {
		return true
	}
	return sql[pos] == '#' && !postgres(dialect)
}

// skipLineComment consumes a line comment that starts at pos.
// closed is false when the comment runs to EOF without a newline.
func skipLineComment(sql string, pos int) (int, bool) {
	if pos < len(sql) && sql[pos] == '#' {
		pos++
	} else if pos+1 < len(sql) && sql[pos] == '-' && sql[pos+1] == '-' {
		pos += 2
	}
	for pos < len(sql) {
		if sql[pos] > 0x7F {
			pos = skipRune(sql, pos)
			continue
		}
		if sql[pos] == '\n' {
			return pos + 1, true
		}
		pos++
	}
	return pos, false
}

func skipBlockComment(sql string, pos int, dialect databasepb.DatabaseDialect) int {
	if pos+1 >= len(sql) || sql[pos] != '/' || sql[pos+1] != '*' {
		return pos
	}
	pos += 2
	level := 1
	nested := postgres(dialect)
	for pos < len(sql) {
		if sql[pos] > 0x7F {
			pos = skipRune(sql, pos)
			continue
		}
		if sql[pos] == '*' && pos+1 < len(sql) && sql[pos+1] == '/' {
			level--
			pos += 2
			if level == 0 || !nested {
				return pos
			}
			continue
		}
		if nested && sql[pos] == '/' && pos+1 < len(sql) && sql[pos+1] == '*' {
			level++
			pos += 2
			continue
		}
		pos++
	}
	return pos
}

// skipLiteral reports whether sql[pos] opens a literal or quoted identifier
// and returns the position after it. Unclosed literals return ok with the
// same position so the caller stops instead of treating the rest as code.
func skipLiteral(sql string, pos int, dialect databasepb.DatabaseDialect) (int, bool) {
	if pos >= len(sql) {
		return pos, false
	}
	c := sql[pos]
	if c > 0x7F {
		return pos, false
	}
	if c == '$' && postgres(dialect) {
		next, started := skipDollarQuote(sql, pos)
		if !started {
			return pos, false
		}
		if next < 0 {
			// Unclosed dollar quote. Stop so a later '--' is not treated as code.
			return pos, true
		}
		return next, true
	}
	if c != '\'' && c != '"' && !(c == '`' && !postgres(dialect)) {
		return pos, false
	}
	quoteLen := 1
	if !postgres(dialect) && pos+2 < len(sql) && sql[pos+1] == c && sql[pos+2] == c {
		quoteLen = 3
	}
	end, ok := skipQuoted(sql, pos+quoteLen, c, quoteLen, dialect)
	if !ok {
		return pos, true
	}
	return end, true
}

func skipQuoted(sql string, pos int, quote byte, quoteLen int, dialect databasepb.DatabaseDialect) (int, bool) {
	pg := postgres(dialect)
	for pos < len(sql) {
		c := sql[pos]
		if c > 0x7F {
			pos = skipRune(sql, pos)
			continue
		}
		if !pg && c == '\\' && pos+1 < len(sql) {
			pos += 2
			continue
		}
		if c == quote {
			if quoteLen == 3 {
				if pos+2 < len(sql) && sql[pos+1] == quote && sql[pos+2] == quote {
					return pos + 3, true
				}
				pos++
				continue
			}
			if pg && pos+1 < len(sql) && sql[pos+1] == quote {
				pos += 2
				continue
			}
			return pos + 1, true
		}
		if !pg && c == '\n' && quoteLen == 1 {
			return pos, false
		}
		pos++
	}
	return pos, false
}

// skipDollarQuote returns the position after a dollar-quoted string.
// started is false when pos is not a dollar tag. A negative position means
// the tag started but the closing tag is missing.
func skipDollarQuote(sql string, pos int) (int, bool) {
	if pos >= len(sql) || sql[pos] != '$' {
		return pos, false
	}
	tagEnd := pos + 1
	if tagEnd < len(sql) && isIdentStart(sql[tagEnd]) {
		tagEnd++
		for tagEnd < len(sql) && isIdentContinue(sql[tagEnd]) {
			tagEnd++
		}
	}
	if tagEnd >= len(sql) || sql[tagEnd] != '$' {
		return pos, false
	}
	tag := sql[pos : tagEnd+1]
	rest := sql[tagEnd+1:]
	idx := strings.Index(rest, tag)
	if idx < 0 {
		return -1, true
	}
	return tagEnd + 1 + idx + len(tag), true
}

func isIdentStart(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_'
}

func isIdentContinue(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

func isSQLSpace(c byte) bool {
	switch c {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x85, 0xA0:
		return true
	default:
		return false
	}
}

func skipRune(sql string, pos int) int {
	if pos >= len(sql) {
		return pos
	}
	if sql[pos] <= 0x7F {
		return pos + 1
	}
	_, size := utf8.DecodeRuneInString(sql[pos:])
	if size < 1 {
		return pos + 1
	}
	return pos + size
}

func postgres(dialect databasepb.DatabaseDialect) bool {
	return effectiveStatementDialect(dialect) == databasepb.DatabaseDialect_POSTGRESQL
}
