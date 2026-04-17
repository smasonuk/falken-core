package agent

import "strings"

type streamedContentParser struct {
	pending string
	visible strings.Builder
}

func (p *streamedContentParser) consume(delta string, eventChan chan<- any) {
	if delta == "" {
		return
	}

	p.pending += delta
	for {
		idx := strings.IndexByte(p.pending, '\n')
		if idx == -1 {
			return
		}

		line := p.pending[:idx+1]
		p.pending = p.pending[idx+1:]
		p.emit(line, eventChan)
	}
}

func (p *streamedContentParser) finish(eventChan chan<- any) string {
	if p.pending != "" {
		p.emit(p.pending, eventChan)
		p.pending = ""
	}
	return p.visible.String()
}

func (p *streamedContentParser) emit(segment string, eventChan chan<- any) {
	trimmed := strings.TrimRight(segment, "\r\n")
	if strings.HasPrefix(trimmed, "THOUGHT:") {
		thought := strings.TrimSpace(strings.TrimPrefix(trimmed, "THOUGHT:"))
		if eventChan != nil {
			eventChan <- AgentThoughtMsg{Text: thought}
		}
		return
	}

	p.visible.WriteString(segment)
	if eventChan != nil {
		eventChan <- AgentTextMsg{Text: segment}
	}
}
