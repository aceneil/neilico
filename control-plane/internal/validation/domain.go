package validation

import (
	"errors"
	"strings"
)

func Domain(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", errors.New("domain is required")
	}
	if strings.ContainsAny(value, "*?_/\\") || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return "", errors.New("domain must be an RFC 1123 hostname without wildcards")
	}
	if len(value) > 253 {
		return "", errors.New("domain must be at most 253 characters")
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 {
			return "", errors.New("each domain label must be between 1 and 63 characters")
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("domain labels must not begin or end with a hyphen")
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return "", errors.New("domain contains an invalid RFC 1123 character")
			}
		}
	}
	return value, nil
}
