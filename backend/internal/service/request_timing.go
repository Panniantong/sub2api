package service

import (
	"context"
	"encoding/json"
)

func timingTerminal(event string) string {
	switch event {
	case "response.completed", "response.done":
		return "completed"
	case "response.failed", "error":
		return "failed"
	case "response.incomplete":
		return "incomplete"
	}
	return ""
}

// Optional repository capability avoids changing billing's existing contract.
func (s *UsageService) RequestTimings(ctx context.Context, id int64) ([]json.RawMessage, error) {
	if _, err := s.GetByID(ctx, id); err != nil {
		return nil, err
	}
	if repo, ok := s.usageRepo.(interface {
		RequestTimings(context.Context, int64) ([]json.RawMessage, error)
	}); ok {
		return repo.RequestTimings(ctx, id)
	}
	return []json.RawMessage{}, nil
}
