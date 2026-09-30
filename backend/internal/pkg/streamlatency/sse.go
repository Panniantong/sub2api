// Package streamlatency measures HTTP streaming latency without changing the wire protocol.
package streamlatency

import (
	"bytes"
	"strings"

	"github.com/tidwall/gjson"
)

// Large image/tool events must never cause unbounded diagnostic buffering.
const maxEventBytes = 256 << 10

// SSEObserver recognizes complete SSE events across arbitrary Write/Read splits.
// It stops inspecting the stream once both usable output and text have appeared.
// Callers serialize access, just as they serialize the underlying stream.
type SSEObserver struct {
	line     []byte
	data     []byte
	event    string
	skip     bool
	size     int
	output   bool
	text     bool
	skipLF   bool
	started  bool
	Limited  bool
	OnOutput func(kind string)
}

func (s *SSEObserver) Feed(p []byte) {
	for len(p) > 0 && !(s.output && s.text) {
		// CR is a line ending on its own; a following LF is part of the same
		// ending even when the two bytes arrive in different reads/writes.
		if s.skipLF {
			s.skipLF = false
			if p[0] == '\n' {
				p = p[1:]
				continue
			}
		}
		i := bytes.IndexAny(p, "\r\n")
		part := p
		if i >= 0 {
			part = p[:i]
		}
		if !s.skip {
			s.size += len(part)
		}
		if s.size > maxEventBytes {
			s.skip = true
			s.Limited = true
			s.data = nil
		}
		// Keep at most one byte while skipping to distinguish a nonempty line.
		if !s.skip {
			s.line = append(s.line, part...)
		} else if len(part) > 0 {
			s.line = []byte{'x'}
		}
		if i < 0 {
			return
		}
		line := s.line
		if !s.started {
			line = bytes.TrimPrefix(line, []byte{0xef, 0xbb, 0xbf})
			s.started = true
		}
		if len(line) == 0 {
			if !s.skip {
				s.dispatch()
			}
			s.data = s.data[:0]
			s.event = ""
			s.size = 0
			s.skip = false
		} else if !s.skip {
			if value, ok := bytes.CutPrefix(line, []byte("data:")); ok {
				value = bytes.TrimPrefix(value, []byte{' '})
				s.data = append(s.data, value...)
				s.data = append(s.data, '\n')
			} else if value, ok := bytes.CutPrefix(line, []byte("event:")); ok {
				s.event = strings.TrimSpace(string(value))
			}
		}
		s.line = s.line[:0]
		s.skipLF = p[i] == '\r'
		p = p[i+1:]
	}
	if s.output && s.text {
		s.line = nil
		s.data = nil
	}
}

func (s *SSEObserver) dispatch() {
	if !gjson.ValidBytes(s.data) {
		return
	}
	root := gjson.ParseBytes(s.data)
	kind := outputKind(root, s.event)
	s.observeKind(kind)
	// A completed response or a multi-choice chunk can carry both a tool or
	// reasoning result and answer text. Do not lose text in the same event.
	if !s.text && kind != "text" && containsAnswerText(root, s.event) {
		s.observeKind("text")
	}
}

func (s *SSEObserver) observeKind(kind string) {
	if kind == "" || (s.output && (kind != "text" || s.text)) {
		return
	}
	s.output = true
	if kind == "text" {
		s.text = true
	}
	if s.OnOutput != nil {
		s.OnOutput(kind)
	}
}

func outputKind(root gjson.Result, event string) string {
	if t := root.Get("type").String(); t != "" {
		event = t
	}
	nonempty := func(path string) bool { v := root.Get(path); return v.Type == gjson.String && v.Str != "" }
	switch event {
	case "response.output_text.delta", "response.refusal.delta", "response.audio_transcript.delta":
		if nonempty("delta") {
			return "text"
		}
	case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
		if nonempty("delta") {
			return "reasoning"
		}
	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		if nonempty("delta") {
			return "tool"
		}
	case "response.image_generation_call.partial_image":
		if nonempty("partial_image_b64") {
			return "image"
		}
	case "response.output_text.done", "response.audio_transcript.done":
		if nonempty("text") {
			return "text"
		}
	case "response.reasoning_text.done", "response.reasoning_summary_text.done":
		if nonempty("text") {
			return "reasoning"
		}
	case "response.function_call_arguments.done":
		if nonempty("arguments") {
			return "tool"
		}
	case "response.custom_tool_call_input.done":
		if nonempty("input") {
			return "tool"
		}
	case "content_block_start":
		switch root.Get("content_block.type").String() {
		case "text":
			if nonempty("content_block.text") {
				return "text"
			}
		case "thinking":
			if nonempty("content_block.thinking") {
				return "reasoning"
			}
		}
	case "content_block_delta":
		switch root.Get("delta.type").String() {
		case "text_delta":
			if nonempty("delta.text") {
				return "text"
			}
		case "thinking_delta":
			if nonempty("delta.thinking") {
				return "reasoning"
			}
		case "input_json_delta":
			if nonempty("delta.partial_json") {
				return "tool"
			}
		}
	case "response.content_part.added", "response.content_part.done":
		if nonempty("part.text") || nonempty("part.refusal") || nonempty("part.transcript") {
			return "text"
		}
	case "response.reasoning_summary_part.added", "response.reasoning_summary_part.done":
		if nonempty("part.text") {
			return "reasoning"
		}
	case "response.output_item.added", "response.output_item.done":
		return itemKind(root.Get("item"))
	case "response.completed", "response.done":
		kind := ""
		root.Get("response.output").ForEach(func(_, v gjson.Result) bool { kind = itemKind(v); return kind == "" })
		return kind
	}
	// Chat Completions: role-only, usage-only and empty delta chunks do not count.
	kind := ""
	root.Get("choices").ForEach(func(_, choice gjson.Result) bool {
		d := choice.Get("delta")
		for _, field := range []string{"content", "refusal", "reasoning_content", "reasoning"} {
			v := d.Get(field)
			if v.Type == gjson.String && v.Str != "" {
				kind = "text"
				if strings.HasPrefix(field, "reasoning") {
					kind = "reasoning"
				}
				return false
			}
		}
		d.Get("tool_calls").ForEach(func(_, tool gjson.Result) bool {
			if tool.Get("function.arguments").String() != "" {
				kind = "tool"
			}
			return kind == ""
		})
		if d.Get("function_call.arguments").String() != "" {
			kind = "tool"
		}
		return kind == ""
	})
	return kind
}

func itemKind(item gjson.Result) string {
	if item.Get("arguments").String() != "" || item.Get("input").String() != "" {
		return "tool"
	}
	kind := ""
	if item.Get("type").String() == "reasoning" {
		item.Get("summary").ForEach(func(_, part gjson.Result) bool {
			if part.Get("text").String() != "" {
				kind = "reasoning"
			}
			return kind == ""
		})
		if kind != "" {
			return kind
		}
	}
	if item.Get("type").String() == "image_generation_call" && item.Get("result").String() != "" {
		return "image"
	}
	item.Get("content").ForEach(func(_, part gjson.Result) bool {
		if part.Get("text").String() != "" || part.Get("refusal").String() != "" || part.Get("transcript").String() != "" {
			kind = "text"
			if item.Get("type").String() == "reasoning" {
				kind = "reasoning"
			}
		}
		return kind == ""
	})
	return kind
}

func containsAnswerText(root gjson.Result, event string) bool {
	if t := root.Get("type").String(); t != "" {
		event = t
	}
	found := false
	if event == "response.completed" || event == "response.done" {
		root.Get("response.output").ForEach(func(_, item gjson.Result) bool {
			found = itemKind(item) == "text"
			return !found
		})
		return found
	}
	root.Get("choices").ForEach(func(_, choice gjson.Result) bool {
		for _, field := range []string{"delta.content", "delta.refusal"} {
			v := choice.Get(field)
			if v.Type == gjson.String && v.Str != "" {
				found = true
				return false
			}
		}
		return true
	})
	return found
}
