package pipeline

import "context"

// timing is a no-op stand-in for the profiler helper of the original project.
// Gosintify does not record per-stage timings.
type noopFinish func(error)

func begin(ctx context.Context, stage, detail string) noopFinish {
	return func(error) {}
}

// measure starts a stage and returns its finish function.
func measure(ctx context.Context, stage, detail string) noopFinish {
	return begin(ctx, stage, detail)
}
