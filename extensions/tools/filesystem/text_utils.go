package main

import (
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

var desanitizations = map[string]string{
	"<fnr>":          "<function_results>",
	"<n>":            "<name>",
	"</n>":           "</name>",
	"<o>":            "<output>",
	"</o>":           "</output>",
	"<e>":            "<error>",
	"</e>":           "</error>",
	"<s>":            "<system>",
	"</s>":           "</system>",
	"<r>":            "<result>",
	"</r>":           "</result>",
	"< META_START >": "<META_START>",
	"< META_END >":   "<META_END>",
	"< EOT >":        "<EOT>",
	"< META >":       "<META>",
	"< SOS >":        "<SOS>",
	"\n\nH:":         "\n\nHuman:",
	"\n\nA:":         "\n\nAssistant:",
}

// desanitizeMatchString replaces sanitized tags with their original forms.
// Returns the desanitized string and a map of which replacements were actually applied.
func desanitizeMatchString(input string) (string, map[string]string) {
	result := input
	applied := make(map[string]string)
	for from, to := range desanitizations {
		if strings.Contains(result, from) {
			result = strings.ReplaceAll(result, from, to)
			applied[from] = to
		}
	}
	return result, applied
}

func normalizeQuotes(s string) string {
	s = strings.ReplaceAll(s, "‘", "'")
	s = strings.ReplaceAll(s, "’", "'")
	s = strings.ReplaceAll(s, "“", "\"")
	s = strings.ReplaceAll(s, "”", "\"")
	return s
}

// findActualString tries an exact match, then falls back to matching normalized quotes.
// It uses rune slices to ensure safe indexing across multi-byte characters.
// Returns the exact matching substring from the file, or an empty string if not found.
func findActualString(fileContent, searchString string) string {
	if strings.Contains(fileContent, searchString) {
		return searchString
	}

	runesFile := []rune(fileContent)

	normSearch := normalizeQuotes(searchString)
	normFile := normalizeQuotes(fileContent)
	normRunesFile := []rune(normFile)
	normRunesSearch := []rune(normSearch)

	if len(normRunesSearch) == 0 || len(normRunesSearch) > len(normRunesFile) {
		return ""
	}

	idx := -1
	for i := 0; i <= len(normRunesFile)-len(normRunesSearch); i++ {
		match := true
		for j := range normRunesSearch {
			if normRunesFile[i+j] != normRunesSearch[j] {
				match = false
				break
			}
		}
		if match {
			idx = i
			break
		}
	}

	if idx != -1 {
		return string(runesFile[idx : idx+len(normRunesSearch)])
	}
	return ""
}

func isOpeningContext(runes []rune, index int) bool {
	if index == 0 {
		return true
	}
	prev := runes[index-1]
	return prev == ' ' || prev == '\t' || prev == '\n' || prev == '\r' ||
		prev == '(' || prev == '[' || prev == '{' || prev == '\u2014' || prev == '\u2013'
}

func isLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// preserveQuoteStyle intelligently applies curly quotes to new text if the original text used them.
func preserveQuoteStyle(oldString, actualOldString, newString string) string {
	if oldString == actualOldString {
		return newString // No quote normalization happened
	}

	hasDouble := strings.Contains(actualOldString, "“") || strings.Contains(actualOldString, "”")
	hasSingle := strings.Contains(actualOldString, "‘") || strings.Contains(actualOldString, "’")

	if !hasDouble && !hasSingle {
		return newString
	}

	resultRunes := []rune(newString)
	finalRunes := make([]rune, 0, len(resultRunes))

	for i, r := range resultRunes {
		if r == '"' && hasDouble {
			if isOpeningContext(resultRunes, i) {
				finalRunes = append(finalRunes, '“')
			} else {
				finalRunes = append(finalRunes, '”')
			}
		} else if r == '\'' && hasSingle {
			// Check for apostrophes in contractions (e.g., "don't")
			prevLetter := i > 0 && isLetter(resultRunes[i-1])
			nextLetter := i < len(resultRunes)-1 && isLetter(resultRunes[i+1])

			if prevLetter && nextLetter {
				finalRunes = append(finalRunes, '’') // Apostrophe
			} else if isOpeningContext(resultRunes, i) {
				finalRunes = append(finalRunes, '‘')
			} else {
				finalRunes = append(finalRunes, '’')
			}
		} else {
			finalRunes = append(finalRunes, r)
		}
	}
	return string(finalRunes)
}

// generateDiff creates a standard Unified Diff string (git-style) between two strings.
func generateDiff(original, updated, filename string) string {
	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(original),
		B:        difflib.SplitLines(updated),
		FromFile: "a/" + filename,
		ToFile:   "b/" + filename,
		Context:  3, // Show 3 lines of context around changes
	}
	text, _ := difflib.GetUnifiedDiffString(diff)
	return text
}
