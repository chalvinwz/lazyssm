package ui

import (
	"strings"

	"github.com/chalvinwz/lazyssm/internal/inventory"
)

// parseFilter turns a filter-bar string into an inventory.Filter, returning any
// tokens it could not interpret so the caller can surface them rather than
// silently dropping the constraint.
//
// Tokens:
//   - "tag:Key=Value"  -> tag constraint
//   - "name:prefix"    -> Name prefix
//   - bare "prefix"     -> Name prefix (last bare token wins)
//
// A "tag:" token without "=Value" (or with an empty key) is reported as ignored.
//
// Example: "tag:Env=prod web-" -> Tags{Env:prod}, NamePrefix "web-".
func parseFilter(s string) (inventory.Filter, []string) {
	f := inventory.Filter{Tags: map[string]string{}}
	var ignored []string
	for _, tok := range strings.Fields(s) {
		switch {
		case strings.HasPrefix(tok, "tag:"):
			kv := strings.SplitN(strings.TrimPrefix(tok, "tag:"), "=", 2)
			if len(kv) == 2 && kv[0] != "" {
				f.Tags[kv[0]] = kv[1]
			} else {
				ignored = append(ignored, tok)
			}
		case strings.HasPrefix(tok, "name:"):
			f.NamePrefix = strings.TrimPrefix(tok, "name:")
		default:
			f.NamePrefix = tok
		}
	}
	if len(f.Tags) == 0 {
		f.Tags = nil
	}
	return f, ignored
}
