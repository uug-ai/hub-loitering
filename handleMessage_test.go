package main

import (
	"encoding/json"
	"testing"

	"github.com/sirupsen/logrus"

	ingest "github.com/uug-ai/models/pkg/ingest"
	"github.com/uug-ai/models/pkg/models"
	queue "github.com/uug-ai/queue/pkg/queue"
)

// TestHandleMessageRoutesMarker verifies that a dispatched loitering stage is
// routed back to the workflows queue as a "loitering" result, that the run
// envelope is preserved, that storage credentials are not echoed back, that the
// typed marker is handed back as a single "marker" block envelope Payload, and
// that the action is terminal (Cancel) since the result was published
// explicitly.
func TestHandleMessageRoutesMarker(t *testing.T) {
	q, err := queue.NewMockQueue()
	if err != nil {
		t.Fatalf("NewMockQueue() error: %v", err)
	}

	run := &models.WorkflowRun{
		Operation: "loitering",
		RunId:     "run-1",
		Key:       "1700000000_6_camera1_1920_1080_30.mp4",
		TraceId:   "trace-123",
		Storage: &models.WorkflowStorage{
			Uri:       "s3://bucket",
			AccessKey: "AKIA",
			Secret:    "secret",
		},
		Inputs: map[string]interface{}{
			"classify": map[string]any{
				"properties": []any{"person"},
				"details": []any{
					map[string]any{
						"id":         "p-1",
						"classified": "person",
						"frames":     []any{float64(0), float64(180)},
					},
				},
			},
		},
	}

	action := handleMessage(logrus.New(), q, "hub-workflows-queue", run)
	if action != models.PipelineCancel {
		t.Errorf("action = %q, want %q (result routed explicitly)", action, models.PipelineCancel)
	}

	sent := q.GetSentMessages()
	if len(sent) != 1 {
		t.Fatalf("expected 1 routed result, got %d", len(sent))
	}

	var result models.WorkflowRun
	if err := json.Unmarshal([]byte(sent[0]), &result); err != nil {
		t.Fatalf("unmarshal routed result: %v", err)
	}
	if result.Operation != "loitering" {
		t.Errorf("result.Operation = %q, want \"loitering\"", result.Operation)
	}
	if result.Key != run.Key {
		t.Errorf("result.Key = %q, want the run media key preserved", result.Key)
	}
	if result.Storage != nil {
		t.Errorf("result.Storage = %+v, want nil (credentials not echoed back)", result.Storage)
	}
	if len(result.Payload) == 0 {
		t.Fatal("result.Payload is empty, want the block envelope")
	}

	var env ingest.BlockEnvelope
	if err := json.Unmarshal(result.Payload, &env); err != nil {
		t.Fatalf("unmarshal result.Payload envelope: %v", err)
	}
	if len(env.Blocks) != 1 || env.Blocks[0].Type != ingest.KindMarker {
		t.Fatalf("want one marker block, got %+v", env.Blocks)
	}

	var marker models.Marker
	if err := json.Unmarshal(env.Blocks[0].Data, &marker); err != nil {
		t.Fatalf("unmarshal marker block: %v", err)
	}
	if marker.Name == "" || marker.StartTimestamp <= 0 {
		t.Errorf("marker = %+v, want a named, timestamped annotation", marker)
	}
	if marker.Duration != 30 {
		t.Errorf("marker.Duration = %d, want 30s for the 180-frame person dwell", marker.Duration)
	}
}
