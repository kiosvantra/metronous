package archive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kiosvantra/metronous/internal/store"
)

type Stage string

const (
	StageBronze Stage = "bronze"
	StageSilver Stage = "silver"
	StageGold   Stage = "gold"
)

var DefaultSensitivePatterns = []string{
	`(?i)api[_-]?key`,
	`(?i)authorization`,
	`(?i)password`,
	`(?i)secret`,
	`(?i)token`,
}

type Config struct {
	Enabled            bool
	BaseDir            string
	CaptureFullPayload bool
	BlockOnSensitive   bool
	RedactPatterns     []string
	MaxFilesPerStage   map[Stage]int
	MaxBytesPerStage   map[Stage]int64
	MaxAgePerStage     map[Stage]time.Duration
}

func (c Config) DefaultMaxFilesPerStage() int {
	if c.MaxFilesPerStage == nil {
		return 0
	}
	if v, ok := c.MaxFilesPerStage[StageBronze]; ok {
		return v
	}
	return 0
}

type PromoteSanitizer func(map[string]interface{}) map[string]interface{}

type EventArchiver interface {
	CaptureBronze(ctx context.Context, rawArgs map[string]interface{}, event store.Event) (string, error)
}

type Pipeline struct {
	cfg       Config
	sensitive []*regexp.Regexp
	mu        sync.Mutex
}

type archivedEventSummary struct {
	AgentID   string    `json:"agent_id"`
	SessionID string    `json:"session_id"`
	EventType string    `json:"event_type"`
	Model     string    `json:"model"`
	Timestamp time.Time `json:"timestamp"`
}

type archivedRecord struct {
	Stage       Stage                  `json:"stage"`
	CapturedAt  time.Time              `json:"captured_at"`
	Event       archivedEventSummary   `json:"event"`
	FullPayload map[string]interface{} `json:"full_payload,omitempty"`
	Source      string                 `json:"source,omitempty"`
}

type StageUsage struct {
	Files  int    `json:"files"`
	Bytes  int64  `json:"bytes"`
	Oldest string `json:"oldest,omitempty"`
	Newest string `json:"newest,omitempty"`
}

type UsageMetrics struct {
	ByStage map[Stage]StageUsage `json:"by_stage"`
}

func (m UsageMetrics) Stage(stage Stage) StageUsage {
	if m.ByStage == nil {
		return StageUsage{}
	}
	return m.ByStage[stage]
}

func UsageForBaseDir(baseDir string) (UsageMetrics, error) {
	p := &Pipeline{cfg: Config{BaseDir: baseDir}}
	return p.Usage()
}

func NewPipeline(cfg Config) (*Pipeline, error) {
	if cfg.BaseDir == "" {
		return nil, errors.New("archive base dir is required")
	}
	patterns := cfg.RedactPatterns
	if len(patterns) == 0 {
		patterns = append([]string(nil), DefaultSensitivePatterns...)
	}
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		r, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("compile redact pattern %q: %w", p, err)
		}
		compiled = append(compiled, r)
	}
	for _, stage := range []Stage{StageBronze, StageSilver, StageGold} {
		if err := os.MkdirAll(filepath.Join(cfg.BaseDir, string(stage)), 0o700); err != nil {
			return nil, fmt.Errorf("create stage dir %q: %w", stage, err)
		}
	}
	return &Pipeline{cfg: cfg, sensitive: compiled}, nil
}

func (p *Pipeline) CaptureBronze(_ context.Context, rawArgs map[string]interface{}, event store.Event) (string, error) {
	if p == nil || !p.cfg.Enabled {
		return "", nil
	}
	rec := archivedRecord{
		Stage:      StageBronze,
		CapturedAt: time.Now().UTC(),
		Event: archivedEventSummary{
			AgentID:   event.AgentID,
			SessionID: event.SessionID,
			EventType: event.EventType,
			Model:     event.Model,
			Timestamp: event.Timestamp,
		},
	}
	if p.cfg.CaptureFullPayload {
		payload, _ := deepCopyMap(rawArgs)
		rec.FullPayload = p.redactSensitive(payload)
	}
	return p.writeRecord(StageBronze, rec)
}

func (p *Pipeline) Promote(_ context.Context, bronzePath string, toStage Stage, sanitizer PromoteSanitizer) (string, error) {
	if p == nil || !p.cfg.Enabled {
		return "", nil
	}
	if toStage != StageSilver && toStage != StageGold {
		return "", fmt.Errorf("invalid promote stage %q", toStage)
	}
	b, err := os.ReadFile(bronzePath)
	if err != nil {
		return "", fmt.Errorf("read bronze record: %w", err)
	}
	var in archivedRecord
	if err := json.Unmarshal(b, &in); err != nil {
		return "", fmt.Errorf("decode bronze record: %w", err)
	}
	payload, _ := deepCopyMap(in.FullPayload)
	if sanitizer != nil {
		payload = sanitizer(payload)
	}
	if p.cfg.BlockOnSensitive && p.hasSensitive(payload) {
		return "", errors.New("promotion blocked by sensitive content policy")
	}
	payload = p.redactSensitive(payload)

	out := archivedRecord{
		Stage:       toStage,
		CapturedAt:  time.Now().UTC(),
		Event:       in.Event,
		FullPayload: payload,
		Source:      filepath.Base(bronzePath),
	}
	return p.writeRecord(toStage, out)
}

func (p *Pipeline) Usage() (UsageMetrics, error) {
	metrics := UsageMetrics{ByStage: map[Stage]StageUsage{}}
	for _, stage := range []Stage{StageBronze, StageSilver, StageGold} {
		files, err := p.listStageFiles(stage)
		if err != nil {
			return UsageMetrics{}, err
		}
		u := StageUsage{Files: len(files)}
		if len(files) > 0 {
			u.Oldest = files[0].modTime.UTC().Format(time.RFC3339)
			u.Newest = files[len(files)-1].modTime.UTC().Format(time.RFC3339)
		}
		for _, f := range files {
			u.Bytes += f.size
		}
		metrics.ByStage[stage] = u
	}
	return metrics, nil
}

func (p *Pipeline) writeRecord(stage Stage, rec archivedRecord) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	dir := filepath.Join(p.cfg.BaseDir, string(stage))
	filename := fmt.Sprintf("%d-%s-%s.json", time.Now().UTC().UnixNano(), sanitizeFilePart(rec.Event.SessionID), sanitizeFilePart(rec.Event.EventType))
	path := filepath.Join(dir, filename)
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode archive record: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write archive record: %w", err)
	}
	if err := p.pruneStage(stage); err != nil {
		return "", err
	}
	return path, nil
}

type stageFile struct {
	path    string
	name    string
	size    int64
	modTime time.Time
}

func (p *Pipeline) listStageFiles(stage Stage) ([]stageFile, error) {
	dir := filepath.Join(p.cfg.BaseDir, string(stage))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read stage dir %q: %w", dir, err)
	}
	out := make([]stageFile, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, fmt.Errorf("stat stage entry %q: %w", e.Name(), err)
		}
		out = append(out, stageFile{path: filepath.Join(dir, e.Name()), name: e.Name(), size: info.Size(), modTime: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].modTime.Equal(out[j].modTime) {
			return out[i].name < out[j].name
		}
		return out[i].modTime.Before(out[j].modTime)
	})
	return out, nil
}

func (p *Pipeline) pruneStage(stage Stage) error {
	files, err := p.listStageFiles(stage)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}
	now := time.Now().UTC()
	if maxAge := getDurationLimit(p.cfg.MaxAgePerStage, stage); maxAge > 0 {
		for len(files) > 0 && now.Sub(files[0].modTime.UTC()) > maxAge {
			if err := os.Remove(files[0].path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("prune by age: %w", err)
			}
			files = files[1:]
		}
	}

	totalBytes := int64(0)
	for _, f := range files {
		totalBytes += f.size
	}
	maxFiles := getIntLimit(p.cfg.MaxFilesPerStage, stage)
	maxBytes := getInt64Limit(p.cfg.MaxBytesPerStage, stage)

	for len(files) > 0 && ((maxFiles > 0 && len(files) > maxFiles) || (maxBytes > 0 && totalBytes > maxBytes)) {
		head := files[0]
		if err := os.Remove(head.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("prune by count/size: %w", err)
		}
		totalBytes -= head.size
		files = files[1:]
	}
	return nil
}

func getIntLimit(m map[Stage]int, stage Stage) int {
	if m == nil {
		return 0
	}
	return m[stage]
}

func getInt64Limit(m map[Stage]int64, stage Stage) int64 {
	if m == nil {
		return 0
	}
	return m[stage]
}

func getDurationLimit(m map[Stage]time.Duration, stage Stage) time.Duration {
	if m == nil {
		return 0
	}
	return m[stage]
}

func (p *Pipeline) hasSensitive(payload map[string]interface{}) bool {
	if len(payload) == 0 {
		return false
	}
	for k, v := range payload {
		if p.matchSensitiveKey(k) {
			return true
		}
		switch vv := v.(type) {
		case map[string]interface{}:
			if p.hasSensitive(vv) {
				return true
			}
		case []interface{}:
			for _, item := range vv {
				if im, ok := item.(map[string]interface{}); ok && p.hasSensitive(im) {
					return true
				}
			}
		}
	}
	return false
}

func (p *Pipeline) redactSensitive(payload map[string]interface{}) map[string]interface{} {
	if len(payload) == 0 {
		return payload
	}
	out, _ := deepCopyMap(payload)
	for k, v := range out {
		if p.matchSensitiveKey(k) {
			out[k] = "[REDACTED]"
			continue
		}
		switch vv := v.(type) {
		case map[string]interface{}:
			out[k] = p.redactSensitive(vv)
		case []interface{}:
			nv := make([]interface{}, 0, len(vv))
			for _, item := range vv {
				if im, ok := item.(map[string]interface{}); ok {
					nv = append(nv, p.redactSensitive(im))
				} else {
					nv = append(nv, item)
				}
			}
			out[k] = nv
		}
	}
	return out
}

func (p *Pipeline) matchSensitiveKey(key string) bool {
	for _, rx := range p.sensitive {
		if rx.MatchString(key) {
			return true
		}
	}
	return false
}

func deepCopyMap(in map[string]interface{}) (map[string]interface{}, error) {
	if in == nil {
		return nil, nil
	}
	b, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func sanitizeFilePart(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", " ", "_", "..", "_")
	return replacer.Replace(s)
}
