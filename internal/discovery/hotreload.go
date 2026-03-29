package discovery

import (
	"fmt"
	"path/filepath"

	"go.uber.org/zap"
)

// HotReloader ties a Watcher to a Registry and applies changes as files
// are created, modified, or removed under the watched directory.
type HotReloader struct {
	watcher  *Watcher
	registry *Registry
	logger   *zap.Logger
	done     chan struct{}
}

// NewHotReloader creates a HotReloader. Call Start() to begin watching.
func NewHotReloader(watcher *Watcher, registry *Registry, logger *zap.Logger) *HotReloader {
	return &HotReloader{
		watcher:  watcher,
		registry: registry,
		logger:   logger,
		done:     make(chan struct{}),
	}
}

// Start begins listening for filesystem events and applying registry changes.
// It runs in the background goroutine and returns immediately.
func (h *HotReloader) Start() {
	go func() {
		defer close(h.done)
		for evt := range h.watcher.Events() {
			h.handle(evt)
		}
	}()
}

// Stop waits for the hot-reloader goroutine to exit (after the watcher is closed).
func (h *HotReloader) Stop() {
	<-h.done
}

// handle processes a single WatchEvent.
func (h *HotReloader) handle(evt WatchEvent) {
	switch evt.Type {
	case EventCreate, EventWrite:
		// Try to parse the agent config from the file or its parent directory.
		path := evt.Path
		var cfg *AgentConfig
		var err error

		// If the event is on a JSON config file directly, parse it.
		switch filepath.Base(path) {
		case "opencode.json", "agent.json":
			cfg, err = ParseAgentConfig(path)
		default:
			// Otherwise try the directory.
			cfg, err = ParseAgentDirectory(path)
		}

		if err != nil {
			h.logger.Debug("skip hot-reload: parse error",
				zap.String("path", path),
				zap.Error(err),
			)
			return
		}

		if regErr := h.registry.Register(cfg); regErr != nil {
			h.logger.Error("hot-reload: register agent",
				zap.String("id", cfg.ID),
				zap.Error(regErr),
			)
			return
		}
		h.logger.Info("hot-reload: registered agent",
			zap.String("id", cfg.ID),
			zap.String("model", cfg.Model),
			zap.String("path", cfg.SourcePath),
		)

	case EventRemove:
		h.registry.UnregisterByPath(evt.Path)
		h.logger.Info("hot-reload: unregistered agent(s) at path",
			zap.String("path", evt.Path),
		)
	}
}

// ApplyModelChange updates the model field in an agent config file and
// re-registers the agent in the registry. It returns an error if the config
// cannot be found, parsed, or written.
func ApplyModelChange(registry *Registry, agentID, newModel string, logger *zap.Logger) error {
	cfg, ok := registry.Get(agentID)
	if !ok {
		return fmt.Errorf("agent %q not found in registry", agentID)
	}

	updated, err := rewriteModel(cfg.SourcePath, newModel)
	if err != nil {
		return fmt.Errorf("rewrite config: %w", err)
	}

	if err := registry.Register(updated); err != nil {
		return fmt.Errorf("re-register agent: %w", err)
	}

	if logger != nil {
		logger.Info("apply-model-change: model updated",
			zap.String("agent", agentID),
			zap.String("old_model", cfg.Model),
			zap.String("new_model", newModel),
			zap.String("source", cfg.SourcePath),
		)
	}
	return nil
}
