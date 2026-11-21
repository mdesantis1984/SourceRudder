package mcp

import (
	"context"
	"encoding/base64"
	"errors"
	"regexp"
	"strconv"
	"strings"
)

const (
	cursorVersion = "v2"
	// maxCursorLen bounds what decodeCursor will look at; a real cursor is
	// about 40 characters.
	maxCursorLen = 128
	// maxCursorOffsetDigits keeps the offset well inside int range.
	maxCursorOffsetDigits = 9
)

var (
	resultSetIDShape = regexp.MustCompile(`^rs_[A-Z2-7]{16}$`)
	cursorToolShape  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
)

// retiredArgsWarning is added to a search response whose call still carries
// the offset or resultSetId arguments that cursor paging replaced.
const retiredArgsWarning = "offset and resultSetId are no longer supported; page with cursor=<nextCursor>"

type toolNameKey struct{}

// withToolName records the tool being called so a cursor can be bound to it.
func withToolName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, toolNameKey{}, name)
}

func toolNameFrom(ctx context.Context) string {
	name, _ := ctx.Value(toolNameKey{}).(string)
	return name
}

// encodeCursor builds the opaque `nextCursor` value: base64url (no padding)
// of "v2:<tool>:<result set id>:<offset>". The cursor only works with the
// tool that issued it. Callers must treat it as opaque.
func encodeCursor(tool, id string, offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(cursorVersion + ":" + tool + ":" + id + ":" + strconv.Itoa(offset)))
}

// decodeCursor validates and unpacks a cursor. It never consults the store;
// the error text is the reason that follows "invalid_cursor: ".
func decodeCursor(c string) (tool, id string, offset int, err error) {
	if len(c) > maxCursorLen {
		return "", "", 0, errors.New("too long; pass the nextCursor value of a previous response unchanged")
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(c)
	if err != nil {
		return "", "", 0, errors.New("not a cursor issued by this server; pass the nextCursor value of a previous response unchanged")
	}
	parts := strings.Split(string(raw), ":")
	if len(parts) != 4 || parts[0] != cursorVersion {
		return "", "", 0, errors.New("unknown cursor format; pass the nextCursor value of a previous response unchanged")
	}
	if !cursorToolShape.MatchString(parts[1]) {
		return "", "", 0, errors.New("cursor does not name a tool; pass the nextCursor value of a previous response unchanged")
	}
	if !resultSetIDShape.MatchString(parts[2]) {
		return "", "", 0, errors.New("cursor does not reference a result set; pass the nextCursor value of a previous response unchanged")
	}
	off := parts[3]
	if off == "" || len(off) > maxCursorOffsetDigits || (len(off) > 1 && off[0] == '0') {
		return "", "", 0, errors.New("cursor position is out of range; pass the nextCursor value of a previous response unchanged")
	}
	for _, r := range off {
		if r < '0' || r > '9' {
			return "", "", 0, errors.New("cursor position is not a number; pass the nextCursor value of a previous response unchanged")
		}
	}
	n, err := strconv.Atoi(off)
	if err != nil {
		return "", "", 0, errors.New("cursor position is out of range; pass the nextCursor value of a previous response unchanged")
	}
	return parts[1], parts[2], n, nil
}
