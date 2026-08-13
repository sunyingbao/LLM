package legacy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	protoevent "eino-cli/deepagent/cloud/protocol/event"
	"eino-cli/deepagent/cloud/protocol/timeline"

	"eino-cli/backend/session/runs"
)

const interruptedReason = "legacy_runtime_not_resumable"

type Destination interface {
	ImportLegacyThread(ctx context.Context, thread Thread) (created bool, err error)
}

type Thread struct {
	SourceSessionID string
	Fingerprint     string
	Title           string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Interrupted     bool
	Events          []timeline.Event
}

type Report struct {
	Imported int
	Skipped  int
	Failed   int
	Sources  int
	Failures []Failure
}

type Failure struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type Importer struct {
	SourceRoot   string
	ManifestPath string
	Destination  Destination
}

func (importer *Importer) Import(ctx context.Context) (report Report, err error) {
	if importer == nil || importer.Destination == nil {
		return report, fmt.Errorf("legacy import destination is required")
	}
	manifest, err := LoadManifest(importer.ManifestPath)
	if err != nil {
		return report, err
	}
	sessionsRoot := filepath.Join(importer.SourceRoot, "sessions")
	entries, err := os.ReadDir(sessionsRoot)
	if os.IsNotExist(err) {
		return report, nil
	}
	if err != nil {
		return report, fmt.Errorf("read legacy sessions: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		report.Sources++
		thread, failures, scanErr := scanSession(filepath.Join(sessionsRoot, entry.Name()), entry.Name())
		report.Failures = append(report.Failures, failures...)
		report.Failed += len(failures)
		if scanErr != nil {
			report.Failed++
			report.Failures = append(report.Failures, Failure{Path: filepath.Join("sessions", entry.Name()), Reason: scanErr.Error()})
			continue
		}
		if manifest.Contains(thread.SourceSessionID, thread.Fingerprint) {
			report.Skipped++
			continue
		}
		created, importErr := importer.Destination.ImportLegacyThread(ctx, thread)
		if importErr != nil {
			report.Failed++
			report.Failures = append(report.Failures, Failure{Path: filepath.Join("sessions", entry.Name()), Reason: importErr.Error()})
			continue
		}
		if created {
			report.Imported++
		} else {
			report.Skipped++
		}
		manifest.Record(thread.SourceSessionID, thread.Fingerprint)
	}
	if err = manifest.Save(importer.ManifestPath); err != nil {
		return report, err
	}
	return report, nil
}

func scanSession(sessionDir, sessionID string) (thread Thread, failures []Failure, err error) {
	runsDir := filepath.Join(sessionDir, "runs")
	entries, err := os.ReadDir(runsDir)
	if os.IsNotExist(err) {
		return Thread{SourceSessionID: sessionID, Fingerprint: emptyFingerprint(sessionID)}, nil, nil
	}
	if err != nil {
		return thread, nil, err
	}
	type sourceRun struct {
		record  runs.Record
		path    string
		payload []byte
	}
	valid := make([]sourceRun, 0, len(entries))
	hash := sha256.New()
	_, _ = hash.Write([]byte(sessionID))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(runsDir, entry.Name())
		payload, readErr := os.ReadFile(path)
		if readErr != nil {
			failures = append(failures, Failure{Path: path, Reason: "read failed"})
			continue
		}
		var record runs.Record
		if decodeErr := json.Unmarshal(payload, &record); decodeErr != nil || strings.TrimSpace(record.ID) == "" {
			failures = append(failures, Failure{Path: path, Reason: "invalid run record"})
			continue
		}
		_, _ = hash.Write([]byte(entry.Name()))
		_, _ = hash.Write(payload)
		valid = append(valid, sourceRun{record: record, path: path, payload: payload})
	}
	sort.Slice(valid, func(i, j int) bool { return valid[i].record.CreatedAt.Before(valid[j].record.CreatedAt) })
	thread = Thread{SourceSessionID: sessionID, Fingerprint: hex.EncodeToString(hash.Sum(nil)), Title: "Imported SGADK session " + sessionID}
	for _, item := range valid {
		record := item.record
		if thread.CreatedAt.IsZero() || record.CreatedAt.Before(thread.CreatedAt) {
			thread.CreatedAt = record.CreatedAt
		}
		if record.UpdatedAt.After(thread.UpdatedAt) {
			thread.UpdatedAt = record.UpdatedAt
		}
		thread.Events = append(thread.Events, eventsFromRun(sessionID, record)...)
		if record.Status == "pending" || record.Status == "running" {
			thread.Interrupted = true
		}
	}
	return thread, failures, nil
}

func eventsFromRun(sessionID string, record runs.Record) (events []timeline.Event) {
	turnID := record.ID
	sequence := 0
	appendEvent := func(eventType protoevent.EventType, payload any, at time.Time) {
		sequence++
		raw, _ := json.Marshal(payload)
		events = append(events, timeline.Event{EventID: fmt.Sprintf("legacy:%s:%s:%03d", sessionID, turnID, sequence), EventType: eventType.String(), TurnID: turnID, CreatedAtMs: at.UnixMilli(), Payload: raw})
	}
	appendEvent(protoevent.EventTypeTurnStarted, struct{}{}, record.CreatedAt)
	if strings.TrimSpace(record.Output) != "" {
		appendEvent(protoevent.EventTypeAssistantMessage, protoevent.MessageEventPayload{Parts: []protoevent.MessagePart{{Type: protoevent.MessagePartTypeText, Text: record.Output}}}, record.UpdatedAt)
	}
	switch record.Status {
	case "success":
		appendEvent(protoevent.EventTypeTurnFinished, struct{}{}, record.UpdatedAt)
	case "pending", "running":
		appendEvent(protoevent.EventTypeTurnInterrupted, map[string]string{"reason": interruptedReason}, record.UpdatedAt)
	default:
		message := strings.TrimSpace(record.Error)
		if message == "" {
			message = "legacy run failed"
		}
		appendEvent(protoevent.EventTypeError, protoevent.ErrorEventPayload{Message: message}, record.UpdatedAt)
	}
	return events
}

func emptyFingerprint(sessionID string) (fingerprint string) {
	sum := sha256.Sum256([]byte(sessionID))
	return hex.EncodeToString(sum[:])
}
