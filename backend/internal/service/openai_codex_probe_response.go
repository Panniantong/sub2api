package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

// A clean EOF or [DONE] is not evidence of a successful Responses generation.
func validateCodexProbeResponse(body []byte) error {
	completed := false
	inspect := func(data []byte) error {
		if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
			return nil
		}
		var event struct {
			Type     string          `json:"type"`
			Status   string          `json:"status"`
			Error    json.RawMessage `json:"error"`
			Response *struct {
				Status string `json:"status"`
			} `json:"response"`
		}
		if json.Unmarshal(data, &event) != nil {
			return errors.New("invalid probe event")
		}
		if event.Type == "error" || event.Type == "response.failed" || event.Type == "response.incomplete" || event.Status == "failed" || event.Status == "incomplete" || (len(event.Error) > 0 && string(event.Error) != "null") {
			return errors.New("probe response failed")
		}
		if event.Response != nil && (event.Response.Status == "failed" || event.Response.Status == "incomplete") {
			return errors.New("probe response failed")
		}
		if event.Type == "response.completed" {
			if event.Response == nil || event.Response.Status != "completed" {
				return errors.New("invalid probe completion")
			}
			completed = true
		}
		if event.Type == "" && event.Status == "completed" {
			completed = true
		}
		return nil
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		if err := inspect(trimmed); err != nil {
			return err
		}
	} else {
		scanner := bufio.NewScanner(bytes.NewReader(body))
		scanner.Buffer(make([]byte, 4096), 1<<20)
		var data []string
		flush := func() error {
			if len(data) == 0 {
				return nil
			}
			err := inspect([]byte(strings.Join(data, "\n")))
			data = nil
			return err
		}
		for scanner.Scan() {
			line := strings.TrimSuffix(scanner.Text(), "\r")
			if line == "" {
				if err := flush(); err != nil {
					return err
				}
				continue
			}
			if strings.HasPrefix(line, "data:") {
				data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
		}
		if scanner.Err() != nil {
			return errors.New("invalid probe stream")
		}
		// A trailing unterminated event may be truncated, even if its JSON parses.
		if len(data) > 0 {
			return errors.New("unterminated probe event")
		}
	}
	if !completed {
		return errors.New("probe did not complete successfully")
	}
	return nil
}
