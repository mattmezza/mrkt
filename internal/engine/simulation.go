package engine

import (
	"context"
	"github.com/mattmezza/mrkt/internal/manifest"
	"time"
)

func (e *Engine) doSimulation(ctx context.Context, op Operation) (any, error) {
	var in struct {
		Manifest  *manifest.Manifest `json:"manifest"`
		Sequence  string             `json:"sequence"`
		Variables map[string]any     `json:"variables"`
		Start     string             `json:"start"`
	}
	if err := decode(op.Input, &in); err != nil {
		return nil, err
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if in.Start != "" {
		var err error
		start, err = time.Parse(time.RFC3339, in.Start)
		if err != nil {
			return nil, bad("invalid_start", "RFC3339 timestamp required")
		}
	}
	var m manifest.Manifest
	if in.Manifest != nil {
		m = *in.Manifest
	} else {
		var err error
		m, _, err = e.activeManifest(ctx, op.Project)
		if err != nil {
			return nil, err
		}
	}
	trace, err := manifest.Simulate(m, in.Sequence, in.Variables, start)
	if err != nil {
		return nil, bad("simulation_failed", err.Error())
	}
	return map[string]any{"external_sends": false, "trace": trace, "start": start, "manifest_digest": manifest.Digest(m)}, nil
}
