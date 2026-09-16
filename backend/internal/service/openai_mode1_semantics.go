package service

import "strings"

// This is intentionally a small equivalence map, not a second forwarding
// pipeline. Unknown fields, item types, tools and content remain comparable.
func mode1CanonicalizeCodexRequest(body map[string]any) {
	if model, ok := body["model"].(string); ok {
		body["model"] = strings.TrimSpace(model)
	}
	if reasoning, ok := body["reasoning"].(map[string]any); ok {
		if reasoning["effort"] == "minimal" {
			reasoning["effort"] = "none"
		}
		model, _ := body["model"].(string)
		mode, _ := reasoning["mode"].(string)
		effort, _ := reasoning["effort"].(string)
		if !isOpenAIGPT6AstraModel(model) && strings.EqualFold(strings.TrimSpace(mode), "pro") && strings.TrimSpace(effort) == "" {
			reasoning["effort"] = "max"
			delete(reasoning, "mode")
		}
	}
	if text, ok := body["input"].(string); ok {
		body["input"] = []any{}
		if strings.TrimSpace(text) != "" {
			body["input"] = []any{map[string]any{"type": "message", "role": "user", "content": text}}
		}
	}
	// The proven OAuth path promotes text-only system messages into
	// instructions. Retain them as developer for JSON object requests.
	omitSystem := true
	if text, ok := body["text"].(map[string]any); ok {
		if format, ok := text["format"].(map[string]any); ok && format["type"] == "json_object" {
			omitSystem = false
		}
	}
	extractSystemMessagesFromInput(body, omitSystem)
	if instructions, ok := body["instructions"].(string); body["instructions"] == nil || (ok && strings.TrimSpace(instructions) == "") {
		delete(body, "instructions") // an empty instructions field may receive defaults
	}
	_, hasTools := body["tools"]
	if functions, ok := body["functions"].([]any); ok && !hasTools {
		tools := make([]any, 0, len(functions))
		for _, function := range functions {
			tools = append(tools, map[string]any{"type": "function", "function": function})
		}
		body["tools"] = tools
		delete(body, "functions")
	}
	_, hasChoice := body["tool_choice"]
	if choice, exists := body["function_call"]; exists && !hasChoice {
		switch c := choice.(type) {
		case string:
			body["tool_choice"] = c
			delete(body, "function_call")
		case map[string]any:
			if name, ok := c["name"].(string); ok && name != "" {
				body["tool_choice"] = map[string]any{"type": "function", "name": name}
				delete(body, "function_call")
			}
		}
	}
	if tools, ok := body["tools"].([]any); ok {
		for _, raw := range tools {
			tool, ok := raw.(map[string]any)
			if !ok || tool["type"] != "function" {
				continue
			}
			if function, ok := tool["function"].(map[string]any); ok {
				for key, value := range function {
					if _, exists := tool[key]; !exists {
						tool[key] = value
					}
				}
				delete(tool, "function")
			}
		}
	}
	if choice, ok := body["tool_choice"].(map[string]any); ok && choice["type"] == "function" {
		if function, ok := choice["function"].(map[string]any); ok {
			if _, exists := choice["name"]; !exists {
				choice["name"] = function["name"]
			}
			delete(choice, "function")
		}
	}
	// Aliasing reserved tool names is bijective and applied to declarations,
	// choices and call items together. Errors leave invalid input comparable.
	_, _, _ = aliasOpenAIOAuthReservedToolNames(body)
	input, ok := body["input"].([]any)
	if !ok {
		return
	}
	referenceIDs := codexItemReferenceIDMappings(input, false)
	itemIDs := codexInputItemIDs(input)
	callIDs := codexInputCallIDs(input)
	for i, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if item["role"] == "tool" && strings.TrimSpace(firstNonEmptyString(item["call_id"], item["tool_call_id"], item["id"])) != "" {
			if _, lossless := extractLosslessTextFromContent(item["content"]); lossless {
				if result, changed := normalizeCodexToolRoleMessages([]any{item}); changed {
					item = result[0].(map[string]any)
					input[i] = item
				}
			}
		}
		typ, _ := item["type"].(string)
		if typ == "" && (item["role"] == "user" || item["role"] == "assistant" || item["role"] == "developer") {
			typ = "message"
			item["type"] = typ
		}
		switch typ {
		case "message":
			// Message IDs are store=false replay metadata. Content and role
			// stay protected, including multimodal payloads and their order.
			delete(item, "id")
			if text, ok := item["content"].(string); ok {
				item["content"] = []any{map[string]any{"type": "input_text", "text": text}}
			}
			if parts, ok := item["content"].([]any); ok {
				for _, rawPart := range parts {
					if part, ok := rawPart.(map[string]any); ok && (part["type"] == "text" || part["type"] == "output_text") {
						part["type"] = "input_text"
					}
				}
			}
		case "reasoning":
			delete(item, "id")
			delete(item, "call_id")
			if item["summary"] == nil {
				item["summary"] = []any{}
			}
		case "compaction_summary":
			delete(item, "id")
		case "item_reference":
			id, _ := item["id"].(string)
			id = strings.TrimSpace(id)
			if _, existing := itemIDs[id]; !existing && strings.HasPrefix(id, "call_") {
				if mapped, exists := referenceIDs[id]; exists {
					item["id"] = mapped
				} else if _, sameTurnCall := callIDs[id]; !sameTurnCall {
					item["id"] = normalizeCodexCallID(id)
				}
			}
		}
		if isCodexToolCallItemType(typ) {
			id := firstNonEmptyString(item["call_id"], item["id"])
			if id != "" {
				item["call_id"] = normalizeCodexCallIDForItemType(typ, id)
			}
			delete(item, "id")
		}
	}
}
