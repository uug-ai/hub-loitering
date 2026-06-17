package loitering

import (
	"strings"
	"testing"

	"github.com/uug-ai/models/pkg/models"
)

// personRun is a dispatched run whose classify start context tracks one person
// across 180 analysed frames (30s at the assumed 6 FPS), keyed to a media file
// whose name encodes a known recording start.
func personRun() *models.WorkflowRun {
	return &models.WorkflowRun{
		RunId: "run-1",
		Key:   "1700000000_6_camera1_1920_1080_30.mp4",
		Inputs: map[string]interface{}{
			"classify": map[string]any{
				"properties": []any{"person"},
				"details": []any{
					map[string]any{
						"id":         "p-1",
						"classified": "person",
						"frames":     []any{float64(0), float64(90), float64(180)},
					},
				},
			},
		},
	}
}

// TestProcessMeasuresDwellAsMarker proves the stage turns the classify
// trajectory into a marker whose span is the subject's dwell time, anchored at
// the recording start parsed from the media key, and flagged as an alert once it
// crosses the loitering threshold.
func TestProcessMeasuresDwellAsMarker(t *testing.T) {
	m := Process(personRun())

	if m.Name != "loitering-p-1" {
		t.Errorf("Name = %q, want \"loitering-p-1\" (the classify detail id)", m.Name)
	}
	if m.StartTimestamp != 1700000000 {
		t.Errorf("StartTimestamp = %d, want 1700000000 (recording start from the key)", m.StartTimestamp)
	}
	if m.Duration != 30 {
		t.Errorf("Duration = %d, want 30s (180 frames at %.0f fps)", m.Duration, assumedFPS)
	}
	if m.EndTimestamp != m.StartTimestamp+m.Duration {
		t.Errorf("EndTimestamp = %d, want StartTimestamp+Duration (%d)", m.EndTimestamp, m.StartTimestamp+m.Duration)
	}
	if !hasCategory(m, "alert") {
		t.Errorf("categories = %+v, want an \"alert\" (30s is over the %ds threshold)", m.Categories, loiterThreshold)
	}
	if len(m.Events) != 1 || m.Events[0].Name != "Loitering" {
		t.Fatalf("events = %+v, want one \"Loitering\" event", m.Events)
	}
	if m.Events[0].Duration != m.Duration {
		t.Errorf("event duration = %d, want the marker duration %d", m.Events[0].Duration, m.Duration)
	}
}

// TestProcessPrefersThePerson proves a run that tracks both a vehicle and a
// person measures the person, even when the vehicle lingers longer — loitering
// is a person behaviour.
func TestProcessPrefersThePerson(t *testing.T) {
	run := &models.WorkflowRun{
		RunId: "run-2",
		Key:   "1700000000_6_camera1_1920_1080_30.mp4",
		Inputs: map[string]interface{}{
			"classify": map[string]any{
				"details": []any{
					map[string]any{
						"id":         "car-1",
						"classified": "car",
						"frames":     []any{float64(0), float64(600)}, // lingers longer
					},
					map[string]any{
						"id":         "p-1",
						"classified": "person",
						"frames":     []any{float64(0), float64(120)},
					},
				},
			},
		},
	}

	m := Process(run)
	if m.Name != "loitering-p-1" {
		t.Errorf("Name = %q, want the person \"loitering-p-1\" even though the car lingers longer", m.Name)
	}
}

// TestProcessFallsBackWithoutClassify proves the demo always emits a valid,
// idempotent-shaped marker even when the run carries no classify context.
func TestProcessFallsBackWithoutClassify(t *testing.T) {
	m := Process(&models.WorkflowRun{RunId: "run-3", Key: "1700000000_6_camera1_1920_1080_30.mp4"})

	if m == nil {
		t.Fatal("Process returned nil, want a fallback marker")
	}
	if m.Name == "" || m.StartTimestamp <= 0 {
		t.Errorf("fallback marker = %+v, want a named, positively-timestamped span", m)
	}
	if m.Duration <= 0 {
		t.Errorf("fallback duration = %d, want a positive synthetic dwell", m.Duration)
	}
}

// TestHasCredentials covers the credential-presence probe used for logging.
func TestHasCredentials(t *testing.T) {
	if HasCredentials(nil) {
		t.Error("HasCredentials(nil) = true, want false")
	}
	if HasCredentials(&models.WorkflowStorage{}) {
		t.Error("HasCredentials(empty) = true, want false")
	}
	if !HasCredentials(&models.WorkflowStorage{Uri: "s3://bucket"}) {
		t.Error("HasCredentials with a uri = false, want true")
	}
	if !HasCredentials(&models.WorkflowStorage{VaultOverrideProvider: "minio"}) {
		t.Error("HasCredentials with a vault override = false, want true")
	}
}

// hasCategory reports whether the marker carries a category with the given name.
func hasCategory(m *models.Marker, name string) bool {
	for _, c := range m.Categories {
		if strings.EqualFold(c.Name, name) {
			return true
		}
	}
	return false
}
