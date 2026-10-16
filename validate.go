package main

import (
	"net/url"
	"regexp"
	"strings"
)

var aliasPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{3,20}$`)

// schemePattern matches a leading scheme like "https:" or "mailto:".
// hostPortPattern matches input like "localhost:8080/x", which has no scheme.
var (
	schemePattern   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
	hostPortPattern = regexp.MustCompile(`^[^/?#:]+:[0-9]{1,5}([/?#]|$)`)
)

func normalizeURL(raw string) string {
	raw = strings.TrimSpace(raw)

	if raw == "" {
		return raw
	}

	scheme := schemePattern.FindString(raw)
	if scheme == "" || hostPortPattern.MatchString(raw) {
		return "https://" + raw
	}

	// Keep the scheme the user typed (validURL rejects anything but
	// http and https), lowercased so HTTPS://go.dev becomes https://go.dev.
	return strings.ToLower(scheme) + raw[len(scheme):]
}

func validURL(raw string) bool {
	if raw == "" || len(raw) > 2048 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != ""
}

var reservedAliases = map[string]struct{}{
	"api":     {},
	"static":  {},
	"health":  {},
	"robots":  {},
	"favicon": {},
}

func isReservedAlias(alias string) bool {
	_, ok := reservedAliases[strings.ToLower(alias)]
	return ok
}

func validAlias(alias string) bool {
	return aliasPattern.MatchString(alias) && !isReservedAlias(alias)
}

