package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"

	ingest "github.com/uug-ai/ingest/pkg/ingest"
	"github.com/uug-ai/models/pkg/models"
	queue "github.com/uug-ai/queue/pkg/queue"
	"github.com/uug-ai/trace/pkg/opentelemetry"
)

// TestHandleMessageRoutesMarker verifies that a dispatched loitering stage is
// routed back to the workflows queue as a "loitering" result, that the run
// envelope is preserved, that storage credentials are not echoed back, that the
// typed marker is handed back as a single "marker" block envelope Payload, and
// that the action is terminal (Cancel) since the result was published
// explicitly.
func TestHandleMessageRoutesMarker(t *testing.T) {
	provider := sdktrace.NewTracerProvider()
	originalProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(originalProvider)
		_ = provider.Shutdown(context.Background())
	})

	q, err := queue.NewMockQueue()
	if err != nil {
		t.Fatalf("NewMockQueue() error: %v", err)
	}

	run := &models.WorkflowRun{
		Operation: "loitering",
		RunId:     "run-1",
		Key:       "1700000000_6_camera1_1920_1080_30.mp4",
		TraceId:   "0123456789abcdef0123456789abcdef",
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

	tracer, err := opentelemetry.NewTracer("hub-loitering")
	if err != nil {
		t.Fatalf("NewTracer() error: %v", err)
	}

	carrier := opentelemetry.TraceContextCarrier{TraceParent: "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"}
	action := handleMessage(logrus.New(), tracer, q, "hub-workflows-queue", run, carrier)
	if action != models.PipelineCancel {
		t.Errorf("action = %q, want %q (result routed explicitly)", action, models.PipelineCancel)
	}

	sent := q.GetSentMessages()
	if len(sent) != 1 {
		t.Fatalf("expected 1 routed result, got %d", len(sent))
	}

	var result models.WorkflowRun
	resultCarrier, err := opentelemetry.UnmarshalWithTraceContext([]byte(sent[0]), &result)
	if err != nil {
		t.Fatalf("unmarshal routed result: %v", err)
	}
	continued, err := tracer.ContinueWithTraceContext(context.Background(), result.TraceId, resultCarrier)
	if err != nil {
		t.Fatalf("continue routed trace context: %v", err)
	}
	resultParent := oteltrace.SpanContextFromContext(continued)
	if !resultParent.IsValid() || resultParent.TraceID().String() != result.TraceId || resultParent.SpanID().String() == "0123456789abcdef" {
		t.Fatalf("routed trace context = %v, want loitering child span", resultParent)
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
