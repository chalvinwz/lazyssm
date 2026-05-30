package ui

import (
	"strings"

	"github.com/chalvinwz/lazyssm/internal/inventory"
)

// parseFilter turns a filter-bar string into an inventory.Filter.
//
// Tokens:
//   - "tag:Key=Value"  -> tag constraint
//   - "name:prefix"    -> Name prefix
//   - bare "prefix"     -> Name prefix (last bare token wins)
//
// Example: "tag:Env=prod web-" -> Tags{Env:prod}, NamePrefix "web-".
func parseFilter(s string) inventory.Filter {
	f := inventory.Filter{Tags: map[string]string{}}
	for _, tok := range strings.Fields(s) {
		switch {
		case strings.HasPrefix(tok, "tag:"):
			kv := strings.SplitN(strings.TrimPrefix(tok, "tag:"), "=", 2)
			if len(kv) == 2 && kv[0] != "" {
				f.Tags[kv[0]] = kv[1]
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
	return f
}
