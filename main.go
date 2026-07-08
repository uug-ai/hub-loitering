// Command hub-loitering is a standalone, queue-driven workflow stage worker.
//
// It is a clone-and-build example of a custom pipeline stage: the hub-workflows
// engine dispatches a "loitering" stage to this worker's FIFO queue
// (kcloud-loitering-queue.fifo) whenever the stage registry routes a run to it —
// typically once an upstream "classify" stage has labelled a person. The worker
// reacts to the dispatch and routes its own result back to the workflows engine,
// which records the resolution and may fan out further conditional stages that
// depend on "loitering".
//
// The routing (which runs reach this stage) lives entirely in the registry as
// the stage's `needs` and `condition`; this worker only performs the stage's
// side effect. That side effect — measuring how long a tracked subject lingers
// and emitting a timeline marker — is a DELIBERATELY NON-REDUNDANT example: it
// adds a new kind of result (a "marker" block) rather than re-deriving what the
// classifier already produced. The behaviour lives in internal/loitering; this
// command is just the queue wiring.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"

	"github.com/uug-ai/models/pkg/models"
	queue "github.com/uug-ai/queue/pkg/queue"
	"github.com/uug-ai/trace/pkg/opentelemetry"

	"github.com/uug-ai/hub-loitering/internal/loitering"
)

var (
	serviceName     = "hub-loitering"
	deadLetterQueue = "dead-letter-queue"
)

// newLogger builds the JSON logger every hub service uses, honouring LOG_LEVEL.
func newLogger() *logrus.Logger {
	logger := logrus.New()
	logger.SetFormatter(&logrus.JSONFormatter{})
	logger.SetOutput(os.Stdout)

	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "trace":
		logger.SetLevel(logrus.TraceLevel)
	case "debug":
		logger.SetLevel(logrus.DebugLevel)
	case "warn":
		logger.SetLevel(logrus.WarnLevel)
	case "error":
		logger.SetLevel(logrus.ErrorLevel)
	default:
		logger.SetLevel(logrus.InfoLevel)
	}
	return logger
}

// envOr returns the environment variable value or a fallback when unset/blank.
func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func main() {
	logger := newLogger()

	// The queue this worker consumes dispatched stages from. The workflows
	// engine publishes to "kcloud-<operation>-queue.fifo", so the default
	// matches the engine's queueNameFor("loitering"). Override with
	// LOITERING_QUEUE if the dispatch naming convention changes.
	stageQueue := envOr("LOITERING_QUEUE", "kcloud-"+loitering.Operation+"-queue.fifo")

	// The workflows engine queue this worker routes its result back to, so the
	// run records the "loitering" resolution and can dispatch any conditional
	// stage that needs it. Defaults to the engine's consumer queue.
	workflowsQueue := envOr("WORKFLOWS_QUEUE", "hub-workflows-queue")

	// Start OpenTelemetry tracing so this stage's work joins the distributed trace
	// the workflows engine propagates on each run (via run.TraceId). Tracing is
	// best-effort: with no OTEL_EXPORTER_OTLP_ENDPOINT the tracer stays in no-op
	// mode (trace v1.2.0) rather than failing the worker.
	tracer, err := opentelemetry.NewTracer(serviceName)
	if err != nil {
		logger.Errorf("failed to create tracer: %v", err)
	} else if err := tracer.Connect(); err != nil {
		logger.Errorf("failed to connect tracer: %v", err)
	} else {
		defer func() { _ = tracer.Shutdown(context.Background()) }()
	}

	// Prometheus metrics, exposed on :8080/metrics like the other workers.
	metricName := strings.NewReplacer("-", "_", ".", "_").Replace(stageQueue)
	processed := prometheus.NewCounter(prometheus.CounterOpts{
		Name: metricName + "_processed_total",
		Help: "Total number of loitering stage messages processed.",
	})
	prometheus.MustRegister(processed)
	http.Handle("/metrics", promhttp.Handler())
	go func() {
		if err := http.ListenAndServe(":8080", nil); err != nil {
			logger.Errorf("metrics server stopped: %v", err)
		}
	}()

	// Connect to RabbitMQ. This worker only consumes and dead-letters; it never
	// forwards down a stage list, so RouterQueue/AnalysisQueue are left unset.
	// The deadletter queue catches unparseable messages.
	options := queue.NewRabbitOptions().
		SetHost(os.Getenv("RABBITMQ_HOST")).
		SetExchange(os.Getenv("RABBITMQ_EXCHANGE")).
		SetUsername(os.Getenv("RABBITMQ_USERNAME")).
		SetPassword(os.Getenv("RABBITMQ_PASSWORD")).
		SetWorkflowsStageQueue(stageQueue).
		SetDeadletterQueue(deadLetterQueue).
		Build()

	q, err := queue.New(options)
	if err != nil {
		logger.Fatalf("failed to create queue: %v", err)
	}
	if err := q.Client.Connect(); err != nil {
		logger.Fatalf("failed to connect to queue: %v", err)
	}

	logger.Infof("%s started: consuming %q, routing results to %q", serviceName, stageQueue, workflowsQueue)

	prometheusHandler := func(models.PipelineMetrics) { processed.Inc() }

	// The workflow subsystem exchanges models.WorkflowRun, not the
	// models.PipelineEvent the shared queue library decodes by default, so we
	// consume raw bytes and decode the run ourselves. A body that is not a
	// WorkflowRun is dead-lettered (PipelineError); otherwise the handler routes
	// the result back to the engine itself and we ack it (PipelineCancel).
	rawHandler := func(payload []byte, args ...any) (models.PipelineAction, []byte, int) {
		var run models.WorkflowRun
		if err := json.Unmarshal(payload, &run); err != nil {
			logger.Errorf("loitering: failed to unmarshal WorkflowRun, dead-lettering: %v", err)
			return models.PipelineError, payload, 0
		}
		return handleMessage(logger, tracer, q.Client, workflowsQueue, &run), payload, 0
	}

	// ReadRawMessages is a method on the concrete RabbitMQ client (it is not part
	// of QueueInterface), so type-assert the client to reach it.
	rmq, ok := q.Client.(*queue.RabbitMQ)
	if !ok {
		logger.Fatalf("loitering worker requires a *queue.RabbitMQ client, got %T", q.Client)
	}

	for {
		if err := rmq.ReadRawMessages(rawHandler, prometheusHandler); err != nil {
			logger.Errorf("failed to read messages: %v", err)
		}
		if err := rmq.Reconnect(); err != nil {
			logger.Errorf("failed to reconnect: %v", err)
			time.Sleep(5 * time.Second)
		}
	}
}
