package maps

import "fmt"

func ToSlice(in map[string]string) []string {
	var out = make([]string, 0, len(in))
	for k, v := range in {
		out = append(out, fmt.Sprintf("%s: %s", k, v))
	}
	return out
}
