package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/uug-ai/models/pkg/models"
	queue "github.com/uug-ai/queue/pkg/queue"
	"github.com/uug-ai/trace/pkg/opentelemetry"
)

const (
	testTraceID      = "4bf92f3577b34da6a3ce929d0e0e4736"
	testParentSpanID = "00f067aa0ba902b7"
)

// recordSpans installs a global tracer provider with the given root sampler
// (wrapped in ParentBased, like production) that records every ended span.
func recordSpans(t *testing.T, root sdktrace.Sampler) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.ParentBased(root)),
		sdktrace.WithSpanProcessor(recorder),
	)
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(noop.NewTracerProvider())
		_ = provider.Shutdown(context.Background())
	})
	return recorder
}

// dispatchTraced runs the handler on a dispatch carrying the given incoming
// traceparent and returns the result routed back to the workflows queue.
func dispatchTraced(t *testing.T, traceParent string) models.WorkflowRun {
	t.Helper()
	q, err := queue.NewMockQueue()
	if err != nil {
		t.Fatalf("NewMockQueue() error: %v", err)
	}
	tracer, err := opentelemetry.NewTracer("hub-loitering")
	if err != nil {
		t.Fatalf("NewTracer() error: %v", err)
	}

	run := dispatchRun()
	run.TraceId = testTraceID
	run.TraceParent = traceParent
	run.TraceState = "engine=dispatch"

	handleMessage(logrus.New(), tracer, q, "hub-workflows-queue", run)

	sent := q.GetSentMessages()
	if len(sent) != 1 {
		t.Fatalf("expected 1 routed result, got %d", len(sent))
	}
	var result models.WorkflowRun
	if err := json.Unmarshal([]byte(sent[0]), &result); err != nil {
		t.Fatalf("unmarshal routed result: %v", err)
	}
	return result
}

// TestHandleMessageContinuesSampledTraceParent verifies the stage span is a
// child of the engine's dispatch span (roots are never sampled, so the span is
// only recorded because it follows the sampled remote parent) and that the
// routed result names the stage span as the next hop's parent.
func TestHandleMessageContinuesSampledTraceParent(t *testing.T) {
	recorder := recordSpans(t, sdktrace.NeverSample())

	result := dispatchTraced(t, "00-"+testTraceID+"-"+testParentSpanID+"-01")

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want the one stage span", len(spans))
	}
	stage := spans[0]
	if got := stage.Parent().SpanID().String(); got != testParentSpanID || !stage.Parent().IsRemote() {
		t.Errorf("stage span parent = %s (remote=%v), want remote %s", got, stage.Parent().IsRemote(), testParentSpanID)
	}
	if got := stage.SpanContext().TraceID().String(); got != testTraceID {
		t.Errorf("stage span trace ID = %s, want %s", got, testTraceID)
	}

	want := "00-" + testTraceID + "-" + stage.SpanContext().SpanID().String() + "-01"
	if result.TraceParent != want {
		t.Errorf("result.TraceParent = %q, want the stage span %q", result.TraceParent, want)
	}
	if result.TraceState != "engine=dispatch" {
		t.Errorf("result.TraceState = %q, want the propagated tracestate", result.TraceState)
	}
}

// TestHandleMessageKeepsUnsampledDecision verifies an unsampled dispatch stays
// unsampled even though roots are always sampled (so trace-ID-only
// continuation would record a span), and that the routed result carries the
// stage's own unsampled traceparent rather than the engine's.
func TestHandleMessageKeepsUnsampledDecision(t *testing.T) {
	recorder := recordSpans(t, sdktrace.AlwaysSample())

	incoming := "00-" + testTraceID + "-" + testParentSpanID + "-00"
	result := dispatchTraced(t, incoming)

	if spans := recorder.Ended(); len(spans) != 0 {
		t.Errorf("recorded %d spans, want none for an unsampled dispatch", len(spans))
	}
	if len(result.TraceParent) != 55 || result.TraceParent[:36] != "00-"+testTraceID+"-" || result.TraceParent[53:] != "00" {
		t.Errorf("result.TraceParent = %q, want an unsampled traceparent on trace %s", result.TraceParent, testTraceID)
	}
	if result.TraceParent == incoming {
		t.Errorf("result.TraceParent = %q, want the stage span, not the incoming traceparent", result.TraceParent)
	}
}
