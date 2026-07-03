package knowledge

import (
	"context"
	"time"

	"github.com/multica-ai/multica/server/internal/scheduler"
)

// JobNameEmbedBackfill is the audit-row name for the embedding refresh
// job. Stable across releases — do not rename without a migration.
const JobNameEmbedBackfill = "knowledge_embedding_backfill"

// embedBatchesPerTick bounds provider spend per tick: at most
// maxBatches × batchSize nodes are embedded per run; anything left over
// is picked up by the next tick (cadence below).
const (
	embedBatchSize      = 32
	embedBatchesPerTick = 4
)

// EmbedBackfillJob keeps knowledge_node embeddings fresh: any node whose
// embedding is missing, older than the node's last edit, or built with a
// different model gets (re-)embedded. With no provider configured or no
// pgvector table, every tick is an immediate success with zero rows —
// enabling semantic search later requires only setting the env vars and
// restarting.
func EmbedBackfillJob(svc *Service) scheduler.JobSpec {
	return scheduler.JobSpec{
		Name:              JobNameEmbedBackfill,
		Cadence:           5 * time.Minute,
		ScheduleDelay:     time.Minute,
		CatchUpMode:       scheduler.CatchUpLatestOnly,
		CatchUpWindow:     24 * time.Hour,
		RunTimeout:        4 * time.Minute,
		StaleTimeout:      10 * time.Minute,
		HeartbeatInterval: 30 * time.Second,
		AllowStaleReentry: true,
		// The next tick naturally retries whatever a failed run left
		// stale, so in-plan retries add nothing but provider load.
		MaxAttempts: 1,
		Scopes:      scheduler.StaticScopes(scheduler.ScopeGlobal),
		Handler: func(ctx context.Context, in scheduler.HandlerInput) (scheduler.HandlerResult, error) {
			total := 0
			for i := 0; i < embedBatchesPerTick; i++ {
				n, err := svc.EmbedStaleNodes(ctx, embedBatchSize)
				if err != nil {
					// Surface the error with partial progress recorded; the
					// scheduler audit row keeps the failure visible.
					return scheduler.HandlerResult{RowsAffected: int64(total)}, err
				}
				total += n
				if n < embedBatchSize {
					break
				}
			}
			return scheduler.HandlerResult{RowsAffected: int64(total)}, nil
		},
	}
}
