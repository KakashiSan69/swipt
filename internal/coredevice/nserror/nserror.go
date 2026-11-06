package nserror

import (
	"slices"
	"strconv"
	"strings"
)

type Level struct {
	Domain string
	Code   int64
	Text   string
}

func (l Level) String() string {
	identity := l.Domain + " " + strconv.FormatInt(l.Code, 10)
	if l.Text == "" {
		return identity
	}
	return l.Text + " (" + identity + ")"
}

func Chain(nsError map[string]any) []Level {
	var levels []Level
	for nsError != nil {
		domain, _ := nsError["domain"].(string)
		userInfo, _ := nsError["userInfo"].(map[string]any)
		var details []string
		for _, key := range []string{"NSLocalizedDescription", "NSLocalizedFailureReason", "NSDebugDescription"} {
			if detail, _ := userInfo[key].(string); detail != "" && !slices.Contains(details, detail) {
				details = append(details, detail)
			}
		}
		levels = append(levels, Level{Domain: domain, Code: codeOf(nsError["code"]), Text: strings.Join(details, ": ")})
		nsError, _ = userInfo["NSUnderlyingError"].(map[string]any)
	}
	return levels
}

func Describe(levels []Level) string {
	texts := make([]string, 0, len(levels))
	for _, level := range levels {
		texts = append(texts, level.String())
	}
	return strings.Join(texts, ": ")
}

func codeOf(value any) int64 {
	switch number := value.(type) {
	case int64:
		return number
	case uint64:
		return int64(number)
	}
	return 0
}
