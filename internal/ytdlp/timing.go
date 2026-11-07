package ytdlp

import (
	"context"
	"time"
)

// Gosintify does not record per-stage timings; these stand in for the
// profiler helper of the original project.
type noopFinish func(error)

func timingBegin(ctx context.Context, stage, detail string) noopFinish {
	return func(error) {}
}

func timingInterval(ctx context.Context, stage, detail string, start time.Time, err error) {}

// timingEnabled reports whether stage timings are recorded.
func timingEnabled(ctx context.Context) bool { return false }
