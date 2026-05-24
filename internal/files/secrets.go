package files

import "strings"

func scanSecretContent(content string) string {
	switch {
	case strings.Contains(content, "-----BEGIN PRIVATE KEY-----"):
		return "content contains private key material"
	case strings.Contains(content, "-----BEGIN RSA PRIVATE KEY-----"):
		return "content contains private key material"
	case strings.Contains(content, "-----BEGIN EC PRIVATE KEY-----"):
		return "content contains private key material"
	case strings.Contains(content, "-----BEGIN OPENSSH PRIVATE KEY-----"):
		return "content contains private key material"
	case containsAPIKeyLikeSecret(content):
		return "content contains API-key-like secret material"
	default:
		return ""
	}
}

func containsAPIKeyLikeSecret(content string) bool {
	fields := strings.FieldsFunc(content, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ' ' || r == '\t' || r == '"' || r == '\''
	})
	for _, field := range fields {
		normalized := strings.Trim(field, ",;")
		switch {
		case strings.HasPrefix(normalized, "sk-") && len(normalized) >= 24:
			return true
		case strings.HasPrefix(normalized, "ghp_") && len(normalized) >= 24:
			return true
		}
	}
	return false
}
