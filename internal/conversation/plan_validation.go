package conversation

import (
	"fmt"
	"strings"
)

// planSection describes a required section in an implementation plan.
type planSection struct {
	label    string
	headings []string
}

// requiredPlanSections lists the concerns every plan must address.
var requiredPlanSections = []planSection{
	{label: "Goal", headings: []string{"Goal"}},
	{label: "Files", headings: []string{"Files"}},
	{label: "Changes", headings: []string{"Changes", "Implementation Steps"}},
	{label: "Verification", headings: []string{"Verification", "Validation"}},
}

// minPlanBytes is the minimum byte length accepted as a meaningful plan.
const minPlanBytes = 100

type markdownHeading struct {
	Text      string
	LineIndex int
}

// ValidateImplementationPlan checks that an implementation plan has the
// required Markdown headings and meaningful content under each required section.
func ValidateImplementationPlan(plan string) error {
	trimmed := strings.TrimSpace(plan)
	if trimmed == "" || trimmed == strings.TrimSpace(DefaultPlanStarterText) {
		return ErrInvalidPlan
	}
	if len([]byte(trimmed)) < minPlanBytes {
		return fmt.Errorf("%w: plan is too short to be useful; expand it to cover Goal, Files, Changes, and Verification", ErrInvalidPlan)
	}

	lines := strings.Split(trimmed, "\n")
	headings := detectMarkdownHeadings(lines)
	detected := make([]string, 0, len(headings))
	detectedSet := make(map[string]markdownHeading, len(headings))
	for _, heading := range headings {
		detected = append(detected, heading.Text)
		detectedSet[strings.ToLower(heading.Text)] = heading
	}

	for _, section := range requiredPlanSections {
		heading, ok := findRequiredHeading(detectedSet, section.headings)
		if !ok {
			return fmt.Errorf("%w: %s", ErrInvalidPlan, missingHeadingMessage(section, detected))
		}
		body := sectionBody(lines, headings, heading.LineIndex)
		if err := validatePlanSectionBody(section.label, body); err != nil {
			return err
		}
	}
	return nil
}

func detectMarkdownHeadings(lines []string) []markdownHeading {
	var headings []markdownHeading
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") {
			continue
		}
		hashes := 0
		for hashes < len(trimmed) && trimmed[hashes] == '#' {
			hashes++
		}
		if hashes == len(trimmed) || hashes > 6 || trimmed[hashes] != ' ' {
			continue
		}
		heading := strings.TrimSpace(trimmed[hashes:])
		heading = strings.Trim(heading, "# \t")
		if heading != "" {
			headings = append(headings, markdownHeading{Text: heading, LineIndex: i})
		}
	}
	return headings
}

func findRequiredHeading(detected map[string]markdownHeading, expected []string) (markdownHeading, bool) {
	for _, heading := range expected {
		if found, ok := detected[strings.ToLower(heading)]; ok {
			return found, true
		}
	}
	return markdownHeading{}, false
}

func sectionBody(lines []string, headings []markdownHeading, startLine int) string {
	endLine := len(lines)
	for _, heading := range headings {
		if heading.LineIndex > startLine {
			endLine = heading.LineIndex
			break
		}
	}
	if startLine+1 >= endLine {
		return ""
	}
	return strings.TrimSpace(strings.Join(lines[startLine+1:endLine], "\n"))
}

func validatePlanSectionBody(label, body string) error {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return fmt.Errorf("%w: plan section %q has no meaningful content", ErrInvalidPlan, label)
	}
	normalized := normalizePlaceholder(trimmed)
	switch normalized {
	case "tbd", "todo", "na", "n/a", "none":
		return fmt.Errorf("%w: plan section %q is placeholder-only", ErrInvalidPlan, label)
	}
	return nil
}

func normalizePlaceholder(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "- ")
	value = strings.TrimPrefix(value, "* ")
	value = strings.Trim(value, ". \t\r\n")
	return value
}

func missingHeadingMessage(section planSection, detected []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Plan is missing required heading: %s\n\n", section.label)
	b.WriteString("Detected headings:")
	if len(detected) == 0 {
		b.WriteString("\n- (none)")
	} else {
		for _, heading := range detected {
			fmt.Fprintf(&b, "\n- %s", heading)
		}
	}
	b.WriteString("\n\nExpected one of:")
	for _, heading := range section.headings {
		fmt.Fprintf(&b, "\n# %s", heading)
	}
	return b.String()
}
