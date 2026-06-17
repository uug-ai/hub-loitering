package main

import (
	"encoding/json"

	"github.com/sirupsen/logrus"

	ingest "github.com/uug-ai/models/pkg/ingest"
	"github.com/uug-ai/models/pkg/models"
	queue "github.com/uug-ai/queue/pkg/queue"

	"github.com/uug-ai/hub-loitering/internal/loitering"
)

// handleMessage processes a single dispatched "loitering" stage and routes its
// result back to the workflows engine.
//
// The inbound run is the engine's stage dispatch: Operation "loitering" with the
// storage credentials the worker would use to fetch the media in its Storage and
// the run's accumulated upstream outputs in Results/Inputs. The behaviour itself
// lives in the internal/loitering package (a demo that measures the longest
// dwell in the run's classify trajectory); this handler is the transport glue —
// it logs the dispatch and routes the run back onto the workflows queue with the
// typed marker wrapped in a block envelope as the Payload, so the engine
// persists it and any conditional stage that needs "loitering" can fire. Which
// runs reach this worker is the registry's routing (a stage need), not the
// worker's concern.
//
// It returns PipelineCancel: the result has already been routed onward via an
// explicit publish, so there is nothing for the queue library to forward.
func handleMessage(logger *logrus.Logger, q queue.QueueInterface, workflowsQueue string, run *models.WorkflowRun) models.PipelineAction {
	logger.WithFields(logrus.Fields{
		"operation":  run.Operation,
		"traceId":    run.TraceId,
		"mediaKey":   run.Key,
		"runId":      run.RunId,
		"deviceKey":  run.Device.DeviceKey,
		"hasStorage": loitering.HasCredentials(run.Storage),
	}).Info("loitering stage received dispatch")

	// Build the result routed back to the engine. Preserve the run envelope (its
	// identity and accumulated upstream context) but drop the storage credentials
	// — they are never echoed back — and hand the typed marker back as a
	// single-block envelope in Payload. The engine routes each block through the
	// shared ingest core (this one marker block into the markers collection) and
	// mirrors the blocks grouped by type into Results, so any conditional stage
	// that needs "loitering" still fires.
	result := *run
	result.Operation = loitering.Operation
	result.Storage = nil

	marker := loitering.Process(run)
	logger.WithFields(logrus.Fields{
		"runId":     run.RunId,
		"deviceKey": run.Device.DeviceKey,
		"marker":    marker.Name,
		"duration":  marker.Duration,
	}).Debug("loitering stage produced marker result")

	blockData, err := json.Marshal(marker)
	if err != nil {
		logger.Errorf("loitering: failed to marshal marker block for media=%s: %v", run.Key, err)
		return models.PipelineCancel
	}
	// Wrap the marker in a self-describing block envelope. A demo stage emits a
	// single marker block; a richer stage could append a detection, a media
	// patch, etc., and the engine would ingest each block in order.
	envelope, err := json.Marshal(ingest.BlockEnvelope{
		Blocks: []ingest.Block{{Type: ingest.KindMarker, Data: blockData}},
	})
	if err != nil {
		logger.Errorf("loitering: failed to marshal block envelope for media=%s: %v", run.Key, err)
		return models.PipelineCancel
	}
	result.Payload = envelope

	payload, err := json.Marshal(&result)
	if err != nil {
		logger.Errorf("loitering: failed to marshal result for media=%s: %v", run.Key, err)
		return models.PipelineCancel
	}
	if err := q.Publish(workflowsQueue, payload); err != nil {
		logger.Errorf("loitering: failed to route result to %q for media=%s: %v", workflowsQueue, run.Key, err)
		return models.PipelineCancel
	}

	logger.Debugf("loitering: routed result for media=%s back to %q", run.Key, workflowsQueue)
	return models.PipelineCancel
}
