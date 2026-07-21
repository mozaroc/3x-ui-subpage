package theme

import (
	"fmt"
	"html/template"
	"time"
)

// FuncMap returns helper functions available inside theme templates for
// formatting traffic/expiry data.
func FuncMap() template.FuncMap {
	return template.FuncMap{
		"bytesHuman": bytesHuman,
		"dict":       dict,
		"formatDate": func(t *time.Time) string {
			if t == nil {
				return "Never"
			}
			return t.Format("2006-01-02 15:04")
		},
		"percent": func(used, total int64) float64 {
			if total <= 0 {
				return 0
			}
			p := float64(used) / float64(total) * 100
			if p > 100 {
				return 100
			}
			return p
		},
	}
}

// dict builds a map[string]any from alternating key/value arguments, used
// to pass more than one value into a sub-template invoked via {{template}}
// (which otherwise only accepts a single pipeline as its data argument —
// and that action resets "$" to whatever is passed, so the sub-template
// can't reach back to the caller's root data on its own).
func dict(pairs ...any) (map[string]any, error) {
	if len(pairs)%2 != 0 {
		return nil, fmt.Errorf("dict: odd number of arguments")
	}
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict: argument %d must be a string key", i)
		}
		m[key] = pairs[i+1]
	}
	return m, nil
}

// bytesHuman renders a byte count as a human-readable string (e.g. "1.5 GB").
// A negative value (used as the "unlimited remaining" sentinel elsewhere in
// the codebase) renders as "Unlimited".
func bytesHuman(n int64) string {
	if n < 0 {
		return "Unlimited"
	}
	if n == 0 {
		return "0 B"
	}

	const unit = 1024
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}

	f := float64(n)
	i := 0
	for f >= unit && i < len(units)-1 {
		f /= unit
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d %s", n, units[i])
	}
	return fmt.Sprintf("%.2f %s", f, units[i])
}
