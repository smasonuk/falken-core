package agent

import "testing"

func TestStreamedContentParser_ClassifiesSplitThoughtLine(t *testing.T) {
	var parser streamedContentParser
	eventChan := make(chan any, 10)

	parser.consume("THOU", eventChan)
	parser.consume("GHT: inspect file\nVisible text\n", eventChan)
	visible := parser.finish(eventChan)
	close(eventChan)

	if visible != "Visible text\n" {
		t.Fatalf("expected assistant-visible text only, got %q", visible)
	}

	var thoughts []string
	var assistant []string
	for event := range eventChan {
		switch msg := event.(type) {
		case AgentThoughtMsg:
			thoughts = append(thoughts, msg.Text)
		case AgentTextMsg:
			assistant = append(assistant, msg.Text)
		}
	}

	if len(thoughts) != 1 || thoughts[0] != "inspect file" {
		t.Fatalf("unexpected thought events: %#v", thoughts)
	}
	if len(assistant) != 1 || assistant[0] != "Visible text\n" {
		t.Fatalf("unexpected assistant events: %#v", assistant)
	}
}

func TestStreamedContentParser_DoesNotMisclassifyAssistantTextContainingThoughtMarker(t *testing.T) {
	var parser streamedContentParser
	eventChan := make(chan any, 10)

	parser.consume("Visible ", eventChan)
	parser.consume("THOUGHT: stays assistant text\n", eventChan)
	visible := parser.finish(eventChan)
	close(eventChan)

	if visible != "Visible THOUGHT: stays assistant text\n" {
		t.Fatalf("unexpected visible text: %q", visible)
	}

	var thoughtCount int
	var assistantCount int
	for event := range eventChan {
		switch event.(type) {
		case AgentThoughtMsg:
			thoughtCount++
		case AgentTextMsg:
			assistantCount++
		}
	}

	if thoughtCount != 0 {
		t.Fatalf("expected no thought events, got %d", thoughtCount)
	}
	if assistantCount != 1 {
		t.Fatalf("expected one assistant event, got %d", assistantCount)
	}
}

func TestStreamedContentParser_FlushesFinalThoughtWithoutTrailingNewline(t *testing.T) {
	var parser streamedContentParser
	eventChan := make(chan any, 10)

	parser.consume("THOUGHT: final check", eventChan)
	visible := parser.finish(eventChan)
	close(eventChan)

	if visible != "" {
		t.Fatalf("expected no visible assistant text, got %q", visible)
	}

	var thoughts []string
	for event := range eventChan {
		if msg, ok := event.(AgentThoughtMsg); ok {
			thoughts = append(thoughts, msg.Text)
		}
	}

	if len(thoughts) != 1 || thoughts[0] != "final check" {
		t.Fatalf("unexpected thought events: %#v", thoughts)
	}
}
