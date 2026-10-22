package model

import "strings"

// NormalizeImageRef expands a Docker image reference to its fully qualified form,
// e.g. "order-service:local" -> "docker.io/library/order-service:local".
func NormalizeImageRef(ref string) string {
	if ref == "" {
		return ""
	}
	name, suffix := ref, ""
	if i := strings.Index(name, "@"); i >= 0 {
		name, suffix = name[:i], name[i:]
	}
	lastSlash := strings.LastIndex(name, "/")
	if i := strings.LastIndex(name, ":"); i > lastSlash {
		suffix = name[i:] + suffix
		name = name[:i]
	} else if suffix == "" {
		suffix = ":latest"
	}
	first, rest, hasSlash := strings.Cut(name, "/")
	switch {
	case !hasSlash:
		name = "docker.io/library/" + name
	case !strings.ContainsAny(first, ".:") && first != "localhost":
		name = "docker.io/" + first + "/" + rest
	}
	return name + suffix
}
