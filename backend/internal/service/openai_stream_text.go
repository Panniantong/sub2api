package service

import "github.com/tidwall/gjson"

// Recognize text independently from reasoning/tools for first-text flushing.
// Terminal events already force a flush, so only incremental/done forms need
// inspecting here. Empty deltas and metadata never release staged output.
func openAIStreamDataHasText(data []byte, eventType string) bool {
	switch eventType {
	case "response.output_text.delta":
		return gjson.GetBytes(data, "delta").String() != ""
	case "response.output_text.done":
		return gjson.GetBytes(data, "text").String() != ""
	case "response.content_part.added", "response.content_part.done":
		part := gjson.GetBytes(data, "part")
		return part.Get("type").String() == "output_text" && part.Get("text").String() != ""
	case "response.output_item.added", "response.output_item.done":
		item := gjson.GetBytes(data, "item")
		if item.Get("type").String() == "message" {
			for _, part := range item.Get("content").Array() {
				if part.Get("type").String() == "output_text" && part.Get("text").String() != "" {
					return true
				}
			}
		}
	}
	return false
}
