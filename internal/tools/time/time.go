package time

import (
	"context"
	"time"

	"github.com/xymorphic/morph/pkg/logutils"

	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/tools"
	"github.com/xymorphic/morph/internal/tools/common"
)

var (
	log = logutils.Module("tool.time")
	now = time.Now
)

// Definition returns the model-visible tool definition.
func Definition() tools.Definition {
	return tools.Definition{
		Name:          "time",
		Description:   "Returns the current server time in RFC3339 format.",
		InputSchema:   common.ObjectSchema(nil),
		ParallelSafe:  true,
		Groups:        []string{"core"},
		SemanticIndex: tools.SkipSemanticIndex(),
		Permission: permissions.Operation{
			Resource: permissions.ResourceClock,
			Action:   permissions.ActionRead,
			Effects:  []permissions.Effect{permissions.EffectRead},
		},
		Handler: tools.HandlerFunc(func(context.Context, tools.Call) (tools.Result, error) {
			log.Info().
				Str("tool", "time").
				Str("phase", "start").
				Msg("time tool started")

			value := now().UTC().Format(time.RFC3339)

			log.Info().
				Str("tool", "time").
				Str("phase", "complete").
				Msg("time tool completed")

			return tools.Result{Output: value}, nil
		}),
	}
}
