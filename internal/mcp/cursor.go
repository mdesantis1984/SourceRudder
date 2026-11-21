package mcp

import (
	"encoding/base64"
	"errors"
	"regexp"
	"strconv"
	"strings"
)

const (
	cursorVersion = "v1"
	// maxCursorLen bounds what decodeCursor will look at; a real cursor is
	// about 40 characters.
	maxCursorLen = 128
	// maxCursorOffsetDigits keeps the offset well inside int range.
	maxCursorOffsetDigits = 9
)

var resultSetIDShape = regexp.MustCompile(`^rs_[A-Z2-7]{16}$`)

// encodeCursor builds the opaque `nextCursor` value: base64url (no padding)
// of "v1:<result set id>:<offset>". Callers must treat it as opaque.
func encodeCursor(id string, offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(cursorVersion + ":" + id + ":" + strconv.Itoa(offset)))
}

// decodeCursor validates and unpacks a cursor. It never consults the store;
// the error text is the reason that follows "invalid_cursor: ".
func decodeCursor(c string) (string, int, error) {
	if len(c) > maxCursorLen {
		return "", 0, errors.New("too long; pass the nextCursor value of a previous response unchanged")
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(c)
	if err != nil {
		return "", 0, errors.New("not a cursor issued by this server; pass the nextCursor value of a previous response unchanged")
	}
	parts := strings.Split(string(raw), ":")
	if len(parts) != 3 || parts[0] != cursorVersion {
		return "", 0, errors.New("unknown cursor format; pass the nextCursor value of a previous response unchanged")
	}
	if !resultSetIDShape.MatchString(parts[1]) {
		return "", 0, errors.New("cursor does not reference a result set; pass the nextCursor value of a previous response unchanged")
	}
	off := parts[2]
	if off == "" || len(off) > maxCursorOffsetDigits || (len(off) > 1 && off[0] == '0') {
		return "", 0, errors.New("cursor position is out of range; pass the nextCursor value of a previous response unchanged")
	}
	for _, r := range off {
		if r < '0' || r > '9' {
			return "", 0, errors.New("cursor position is not a number; pass the nextCursor value of a previous response unchanged")
		}
	}
	n, err := strconv.Atoi(off)
	if err != nil {
		return "", 0, errors.New("cursor position is out of range; pass the nextCursor value of a previous response unchanged")
	}
	return parts[1], n, nil
}
