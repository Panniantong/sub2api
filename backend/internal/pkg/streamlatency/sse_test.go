package streamlatency

import (
	"reflect"
	"strings"
	"testing"
)

func TestSSEObserverSplitEvents(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		want       []string
	}{
		{"responses", ": heartbeat\r\n\r\ndata: {\"type\":\"response.created\"}\r\n\r\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"\"}\n\ndata: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"think\"}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"你好\"}\n\n", []string{"reasoning", "text"}},
		{"chat", "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"total_tokens\":9}}\n\ndata: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"function\":{\"arguments\":\"{}\"}}]}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\n", []string{"tool", "text"}},
		{"messages", "event: content_block_delta\ndata: {\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n", []string{"text"}},
		{"errors", "data: {\"type\":\"error\",\"error\":{\"message\":\"failed\"}}\n\ndata: [DONE]\n\n", nil},
		{"carriage return", "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\r\r", []string{"text"}},
		{"utf8 bom", "\xef\xbb\xbfdata: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n", []string{"text"}},
		{"messages initial text", "data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"text\",\"text\":\"hello\"}}\n\n", []string{"text"}},
		{"messages empty tool", "data: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"tool_use\",\"name\":\"search\",\"input\":{}}}\n\n", nil},
		{"done arguments", "data: {\"type\":\"response.function_call_arguments.done\",\"arguments\":\"{}\"}\n\n", []string{"tool"}},
		{"done reasoning", "data: {\"type\":\"response.reasoning_summary_text.done\",\"text\":\"thinking\"}\n\n", []string{"reasoning"}},
		{"reasoning item", "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\",\"summary\":[{\"type\":\"summary_text\",\"text\":\"thinking\"}]}}\n\n", []string{"reasoning"}},
		{"completed mixed output", "data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"function_call\",\"arguments\":\"{}\"},{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"answer\"}]}]}}\n\n", []string{"tool", "text"}},
		{"chat multiple choices", "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"thinking\"}},{\"delta\":{\"content\":\"answer\"}}]}\n\n", []string{"reasoning", "text"}},
		{"reasoning then completed mixed output", "data: {\"type\":\"response.reasoning_text.delta\",\"delta\":\"think\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"function_call\",\"arguments\":\"{}\"},{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"answer\"}]}]}}\n\n", []string{"reasoning", "text"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, size := range []int{1, 7, len(tc.data)} {
				var got []string
				s := SSEObserver{OnOutput: func(k string) { got = append(got, k) }}
				for p := []byte(tc.data); len(p) > 0; {
					n := min(size, len(p))
					s.Feed(p[:n])
					p = p[n:]
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("split %d: got %v, want %v", size, got, tc.want)
				}
			}
		})
	}
}

func TestSSEObserverRequiresCompleteEventAndBoundsMemory(t *testing.T) {
	count := 0
	s := SSEObserver{OnOutput: func(string) { count++ }}
	s.Feed([]byte("data: " + strings.Repeat("x", maxEventBytes+100)))
	if len(s.line)+len(s.data) > maxEventBytes || !s.Limited {
		t.Fatal("oversized event retained")
	}
	s.Feed([]byte("\r\n\r\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ready\"}\n"))
	if count != 0 {
		t.Fatal("incomplete event counted")
	}
	s.Feed([]byte("\n"))
	if count != 1 {
		t.Fatalf("observer did not recover after oversized event: %d", count)
	}
}

func TestSSEObserverOversizedEventRecoveryAcrossLineEndings(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n", "\r"} {
		for _, split := range []int{1, 4096, maxEventBytes + 100} {
			count := 0
			s := SSEObserver{OnOutput: func(string) { count++ }}
			data := "data: " + strings.Repeat("x", maxEventBytes+100) + ending + ending +
				"data: {\"type\":\"response.output_text.delta\",\"delta\":\"ready\"}" + ending + ending
			for p := []byte(data); len(p) > 0; {
				n := min(split, len(p))
				s.Feed(p[:n])
				p = p[n:]
			}
			if !s.Limited || count != 1 {
				t.Fatalf("ending=%q split=%d: limited=%v output=%d", ending, split, s.Limited, count)
			}
		}
	}
}

func BenchmarkSSEObserver(b *testing.B) {
	p := []byte("data: {\"type\":\"response.created\"}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s := SSEObserver{}
		s.Feed(p)
	}
}
