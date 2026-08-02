package readiness

import (
	"context"
	"fmt"

	"github.com/xymorphic/morph/internal/profile"
	morphruntime "github.com/xymorphic/morph/internal/runtime"
)

func buildRuntimeGroup(ctx context.Context, active profile.Profile) Group {
	result := morphruntime.Probe(ctx, active)
	if result.Err == nil {
		return Group{
			Name: "daemon",
			Checks: []Check{
				check(
					"runtime",
					StatusPass,
					fmt.Sprintf(
						"profile %q is listening on %s:%d",
						result.Metadata.Profile,
						result.Metadata.RPC.Address,
						result.Metadata.RPC.Port,
					),
				),
			},
		}
	}

	status := StatusWarn
	message := result.Err.Error()
	if result.State == morphruntime.ProbeStateInvalid {
		status = StatusFail
	}

	return Group{
		Name: "daemon",
		Checks: []Check{
			check(
				"runtime",
				status,
				message,
				commandAction("morph daemon", "start the daemon for this profile"),
			),
		},
	}
}
