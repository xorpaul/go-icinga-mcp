package icinga

import "strings"

// GetIn walks dotted path "a.b.c" through nested map[string]any, returning nil
// if any segment is missing or not a map.
func GetIn(item any, path string) any {
	cur := item
	for _, p := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		v, present := m[p]
		if !present {
			return nil
		}
		cur = v
	}
	return cur
}

func ProjectItem(item any, fields []string) map[string]any {
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		if v := GetIn(item, f); v != nil {
			out[f] = v
		}
	}
	return out
}

func ProjectList(items any, fields []string) []map[string]any {
	list, ok := items.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, it := range list {
		out = append(out, ProjectItem(it, fields))
	}
	return out
}

// ParseFields splits a comma-separated dotted-path list, falling back to the
// caller-supplied default set when the input is empty or absent.
func ParseFields(csv string, def []string) []string {
	if strings.TrimSpace(csv) == "" {
		out := make([]string, len(def))
		copy(out, def)
		return out
	}
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
