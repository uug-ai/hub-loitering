// Package loitering implements the "loitering" workflow stage worker logic: it
// turns a dispatched stage event into the timeline MARKER routed back to the
// workflows engine.
//
// Where a detector re-finds objects the classifier already labelled, this stage
// computes something the pipeline does not: how long a tracked subject lingers
// in view. It reads the classify trajectory already on the run, measures the
// longest dwell, and emits a "marker" block — a named span on the recording's
// timeline — which the engine persists to the markers collection. That makes it
// a deliberately NON-REDUNDANT example of the block contract: it adds a new kind
// of result rather than re-deriving an existing one.
//
// It is a DEMO of the contract, not a production behaviour analyser: it works
// purely from the classify boxes (no media fetch, no model), so it builds and
// runs end-to-end. The queue wiring lives in the command (package main); this
// package holds the stage behaviour so it can be tested in isolation.
package loitering

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/uug-ai/models/pkg/models"
)

// Operation is this stage's id. It binds the queue the worker consumes
// (kcloud-loitering-queue.fifo) and the operation its result is recorded under
// by the workflows engine. It must match the `operation` of the stage registered
// in PIPELINE_STAGE_REGISTRY.
const Operation = "loitering"

// classifyOperation is the key the analysis hand-off files the classifier result
// under in the run's start context (Inputs); it is the source of the
// trajectories this stage measures.
const classifyOperation = "classify"

// loiterClass is the classify label this demo watches. Loitering is a person
// behaviour, so the stage prefers the longest-lingering classified "person" and
// only falls back to another class when the run has no person, so the demo
// always produces a marker.
const loiterClass = "person"

// assumedFPS converts a span of analysed frames into seconds. The stage dispatch
// carries the classify trajectory (frame indices) but not the recording's frame
// rate — that is media metadata, not part of the stage contract — so the demo
// assumes a representative value. A real stage would read the true FPS from the
// media it fetches with the run's Storage credentials.
const assumedFPS = 6.0

// loiterThreshold is the dwell time, in seconds, at or above which a subject is
// flagged as loitering. Below it the marker is still emitted (so the demo always
// shows a result) but tagged as plain presence rather than an alert.
const loiterThreshold int64 = 10

// dwell is one subject's presence span, measured in analysed-frame indices.
type dwell struct {
	id         string
	label      string
	firstFrame int64
	lastFrame  int64
}

// frames is the number of analysed frames the subject was tracked across.
func (d dwell) frames() int64 { return d.lastFrame - d.firstFrame }

// Process runs the stage against a dispatched run and returns the timeline
// marker the worker wraps as a "marker" block in its delegated-ingest Payload.
// The engine routes that block through the shared ingest core into the markers
// collection (keyed by the recording, device and the marker's name + start) and
// mirrors it into the run's Results so a downstream conditional stage can test
// what this stage produced.
//
// This is a DEMO stand-in for real behaviour analysis: it measures the
// longest-lingering classified subject from the run's start context and labels
// that span. When the run carries no usable trajectory it falls back to a single
// synthetic dwell, so the demo always has a marker to show. The worker is
// generic — it processes whatever run it is handed; WHICH runs reach it is the
// registry's job. To restrict the stage to one camera, gate it in
// PIPELINE_STAGE_REGISTRY with a need condition
// {path:"device.deviceKey", op:"eq", value:"<key>"} rather than special-casing a
// device in the worker.
func Process(run *models.WorkflowRun) *models.Marker {
	recStart := recordingStart(run)
	subject, ok := longestDwell(run)
	if !ok {
		// No usable classify trajectory: fall back to one synthetic dwell at the
		// threshold so the demo timeline always has a loitering marker.
		subject = dwell{
			id:         "subject",
			label:      loiterClass,
			firstFrame: 0,
			lastFrame:  int64(float64(loiterThreshold) * assumedFPS),
		}
	}
	return subject.marker(recStart)
}

// longestDwell returns the subject that lingers longest in the run's classify
// trajectory. It prefers a classified "person" (loitering is a person behaviour)
// and otherwise falls back to the longest-tracked subject of any class, so a run
// that classified only vehicles still yields a marker. It reports !ok when the
// run carries no classify or no detail with a usable frame span.
func longestDwell(run *models.WorkflowRun) (dwell, bool) {
	classify, ok := extractClassify(run)
	if !ok {
		return dwell{}, false
	}
	var best, bestPerson dwell
	var found, foundPerson bool
	for i, d := range classify.Details {
		first, last, ok := frameSpan(d)
		if !ok {
			continue
		}
		cand := dwell{
			id:         detailID(d, i),
			label:      strings.TrimSpace(d.Classified),
			firstFrame: first,
			lastFrame:  last,
		}
		if !found || cand.frames() > best.frames() {
			best, found = cand, true
		}
		if strings.EqualFold(cand.label, loiterClass) && (!foundPerson || cand.frames() > bestPerson.frames()) {
			bestPerson, foundPerson = cand, true
		}
	}
	if foundPerson {
		return bestPerson, true
	}
	return best, found
}

// frameSpan returns the first and last analysed-frame index a classify detail
// spans. It prefers the detail's parallel Frames slice, also folds in the frame
// index embedded as the 5th element of each trajectory entry, and finally falls
// back to the detail's single representative Frame. It reports !ok when the
// detail carries no frame information at all.
func frameSpan(d models.ClassifyDetails) (int64, int64, bool) {
	frames := append([]int64(nil), d.Frames...)
	for _, t := range d.Traject {
		if len(t) >= 5 {
			frames = append(frames, int64(t[4]))
		}
	}
	if len(frames) == 0 {
		if d.Frame > 0 {
			f := int64(d.Frame)
			return f, f, true
		}
		return 0, 0, false
	}
	sort.Slice(frames, func(i, j int) bool { return frames[i] < frames[j] })
	return frames[0], frames[len(frames)-1], true
}

// detailID returns a stable id for a classify detail: its own id when present,
// else a positional fallback. It is the suffix of the marker name, so the same
// run always upserts the same marker.
func detailID(d models.ClassifyDetails, i int) string {
	if id := strings.TrimSpace(d.Id); id != "" {
		return id
	}
	return fmt.Sprintf("subject-%d", i)
}

// marker builds the timeline marker for this dwell, anchored at the recording's
// start. StartTimestamp/EndTimestamp are unix seconds; the engine fills the
// organisation and device from the run, so the worker sets only the annotation
// itself. A dwell at or beyond the threshold is tagged as an "alert".
func (d dwell) marker(recStart int64) *models.Marker {
	start := recStart + int64(math.Round(float64(d.firstFrame)/assumedFPS))
	end := recStart + int64(math.Round(float64(d.lastFrame)/assumedFPS))
	if end < start {
		end = start
	}
	duration := end - start

	label := d.label
	if label == "" {
		label = loiterClass
	}

	categories := []models.MarkerCategory{{Name: "object"}}
	verb := "lingered"
	if duration >= loiterThreshold {
		categories = []models.MarkerCategory{{Name: "alert"}, {Name: "object"}}
		verb = "loitered"
	}
	description := fmt.Sprintf("%s %s for %ds in view", label, verb, duration)

	return &models.Marker{
		Name:           fmt.Sprintf("loitering-%s", d.id),
		StartTimestamp: start,
		EndTimestamp:   end,
		Duration:       duration,
		Description:    description,
		Categories:     categories,
		Tags:           []models.MarkerTag{{Name: Operation}, {Name: label}},
		Events: []models.MarkerEvent{{
			StartTimestamp: start,
			EndTimestamp:   end,
			Duration:       duration,
			Name:           "Loitering",
			Description:    description,
		}},
	}
}

// extractClassify pulls the classifier result out of the run's start context.
// The analysis hand-off files it under Inputs["classify"]; a re-entrant run may
// also carry it in Results. It is stored as decoded JSON (map[string]any), so it
// is re-marshalled and decoded into the typed Classify to read the trajectories.
func extractClassify(run *models.WorkflowRun) (models.Classify, bool) {
	raw, ok := run.Inputs[classifyOperation]
	if !ok || raw == nil {
		raw, ok = run.Results[classifyOperation]
	}
	if !ok || raw == nil {
		return models.Classify{}, false
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return models.Classify{}, false
	}
	var classify models.Classify
	if err := json.Unmarshal(encoded, &classify); err != nil {
		return models.Classify{}, false
	}
	return classify, true
}

// recordingStart derives the recording's start time (unix seconds) from the run
// key. A Kerberos media filename encodes its attributes before the extension,
// the first underscore-separated field being the recording start
// ("<start>_<token>_<device>_<width>_<height>_<duration>.mp4"). The dispatch does
// not carry the recording timestamp itself (it is engine-internal), so the
// worker reads it from the key it is handed. A key that does not parse leaves
// only the wall clock — fine for a demo, though it makes the fallback marker
// non-idempotent; a real stage reads the true start from the media metadata.
func recordingStart(run *models.WorkflowRun) int64 {
	name := run.Key
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	pieces := strings.Split(name, ".")
	attrs := name
	if len(pieces) >= 2 {
		attrs = pieces[len(pieces)-2]
	}
	if fields := strings.Split(attrs, "_"); len(fields) > 0 {
		if ts, err := strconv.ParseInt(fields[0], 10, 64); err == nil && ts > 0 {
			return ts
		}
	}
	return time.Now().Unix()
}

// HasCredentials reports whether the dispatch carried any storage credential, so
// the worker can log that it received what it needs to fetch the media without
// leaking the values themselves.
func HasCredentials(storage *models.WorkflowStorage) bool {
	if storage == nil {
		return false
	}
	return storage.Uri != "" || storage.AccessKey != "" || storage.Secret != "" ||
		storage.VaultOverrideUri != "" || storage.VaultOverrideAccessKey != "" ||
		storage.VaultOverrideSecret != "" || storage.VaultOverrideProvider != ""
}
