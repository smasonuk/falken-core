package pluginsdk

import "regexp"

// SecretPatterns contains regular expressions used to detect sensitive values.
var SecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?m)^-----BEGIN (?:RSA |OPENSSH |[A-Z]+ )?PRIVATE KEY-----`),
	regexp.MustCompile(`(?m)(?:A3T[A-Z0-9]|AKIA|AGPA|AIDA|AROA|AIPA|ANPA|ANVA|ASIA)[A-Z0-9]{16}`), // AWS Keys
	regexp.MustCompile(`(?i)(?:sk_live_|sk_test_)[a-zA-Z0-9]{24,}`),                               // Stripe Keys
}
