package api

import (
	"bytes"
	"encoding/json"
)

// Event is the payload of the "events" topic: a tracked object that Frigate
// has started tracking ("new"), updated ("update") or lost ("end").
type Event struct {
	Type   string      `json:"type"`
	Before EventObject `json:"before"`
	After  EventObject `json:"after"`
}

// EventObject is the state of a tracked object. Only the fields the bridge
// uses are decoded.
type EventObject struct {
	ID            string   `json:"id"`
	Camera        string   `json:"camera"`
	Label         string   `json:"label"`
	Score         float64  `json:"score"`
	TopScore      float64  `json:"top_score"`
	FalsePositive bool     `json:"false_positive"`
	CurrentZones  []string `json:"current_zones"`
}

// ParseEvent decodes an "events" payload. The websocket may carry the JSON
// either as an object or encoded inside a JSON string.
func ParseEvent(payload json.RawMessage) (Event, error) {
	var ev Event
	payload = bytes.TrimSpace(payload)
	if len(payload) > 0 && payload[0] == '"' {
		var inner string
		if err := json.Unmarshal(payload, &inner); err != nil {
			return ev, err
		}
		payload = json.RawMessage(inner)
	}
	err := json.Unmarshal(payload, &ev)
	return ev, err
}
