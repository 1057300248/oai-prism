package main

import (
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/oai-prism/oaiprism/internal/gateway"
)

// Tool-result envelopes contain several unrelated IDs. Only the tool's decoded
// agent_id is a valid wait target; scanning the serialized Item picks its own ID.
func spawnedAgentID(item gateway.Item) (string, error) {
	var payload struct {
		AgentID string `json:"agent_id"`
	}
	texts := []string{item.Output}
	for _, part := range item.Content {
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	for _, text := range texts {
		if json.Unmarshal([]byte(text), &payload) == nil && payload.AgentID != "" {
			if _, err := uuid.Parse(payload.AgentID); err != nil {
				return "", errors.New("invalid spawned agent_id")
			}
			return payload.AgentID, nil
		}
	}
	return "", errors.New("spawn result did not contain a valid agent_id")
}
