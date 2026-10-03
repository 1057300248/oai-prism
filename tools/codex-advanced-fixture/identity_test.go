package main

import (
	"github.com/oai-prism/oaiprism/internal/gateway"
	"testing"
)

func TestSpawnIdentityUsesPayloadNotEnvelope(t *testing.T) {
	const agent = "01a10215-aa49-7491-8d18-40686c18e9a6"
	item := gateway.Item{Type: "function_call_output", ID: "fco_01a10215-aa4c-7ab3-9960-385caa535cf8", CallID: "call_ac93c147-da02-430e-b0e3-3ab2e02cfc7a", Output: `{"agent_id":"` + agent + `","nickname":"fixture"}`}
	id, err := spawnedAgentID(item)
	if err != nil || id != agent {
		t.Fatalf("wrong identity: %q %v", id, err)
	}
	item.Output = ""
	if _, err = spawnedAgentID(item); err == nil {
		t.Fatal("envelope ID accepted without tool payload")
	}
	item.Content = []gateway.Content{{Type: "input_text", Text: `{"agent_id":"` + agent + `"}`}}
	if id, err = spawnedAgentID(item); err != nil || id != agent {
		t.Fatal(id, err)
	}
}
