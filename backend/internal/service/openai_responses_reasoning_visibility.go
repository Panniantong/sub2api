package service

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

// Clone output items before removing summaries so the internal cache still sees
// the original text. Keep item IDs and lifecycle events for reasoning passback.
func withoutReasoningSummaries(output []apicompat.ResponsesOutput) []apicompat.ResponsesOutput {
	if output == nil {
		return nil
	}
	out := append([]apicompat.ResponsesOutput{}, output...)
	for i := range out {
		if out[i].Type == "reasoning" {
			out[i].Summary = nil
		}
	}
	return out
}

func withoutReasoningSummaryEvents(events []apicompat.ResponsesStreamEvent) []apicompat.ResponsesStreamEvent {
	out := make([]apicompat.ResponsesStreamEvent, 0, len(events))
	for _, event := range events {
		if strings.HasPrefix(event.Type, "response.reasoning_summary_") {
			continue
		}
		if event.Item != nil && event.Item.Type == "reasoning" {
			item := *event.Item
			item.Summary = nil
			event.Item = &item
		}
		if event.Response != nil {
			response := *event.Response
			response.Output = withoutReasoningSummaries(response.Output)
			event.Response = &response
		}
		out = append(out, event)
	}
	return out
}
