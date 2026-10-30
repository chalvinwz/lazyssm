package ui

import (
	"strings"

	"github.com/chalvinwz/lazyssm/internal/inventory"
)

// parseFilter parses the filter bar. "tag:Key=Value" adds a tag constraint;
// "name:prefix" or a bare word sets the Name prefix, and the last one wins. It
// returns the tokens it ignored (a "tag:" token without a key or "=", or a
// shadowed Name prefix) so the caller can report them.
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
			if f.NamePrefix != "" {
				ignored = append(ignored, f.NamePrefix) // shadowed by this token
			}
			f.NamePrefix = strings.TrimPrefix(tok, "name:")
		default:
			if f.NamePrefix != "" {
				ignored = append(ignored, f.NamePrefix) // shadowed by this token
			}
			f.NamePrefix = tok
		}
	}
	if len(f.Tags) == 0 {
		f.Tags = nil
	}
	return f, ignored
}
