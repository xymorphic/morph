package permissions

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	cli "github.com/urfave/cli/v3"

	morphcli "github.com/wandxy/morph/internal/cli"
	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/permissions"
	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	"github.com/wandxy/morph/internal/rpc/rpcmeta"
	"github.com/wandxy/morph/internal/runtime"
	"github.com/wandxy/morph/pkg/str"
)

var (
	permissionOutput io.Writer = os.Stdout
	newClient                  = func(ctx context.Context, cfg *config.Config) (permissionClient, error) {
		return rpcclient.NewClient(ctx, rpcclient.OptionsWithConfigAuth(rpcclient.Options{
			Address:           cfg.RPC.Address,
			Port:              cfg.RPC.Port,
			PermissionSurface: permissions.SurfaceCLI,
			PermissionPreset:  cfg.Permissions.EffectivePreset(),
			AuthServices:      []string{"/morph.v1.PermissionService"},
		}, cfg))
	}
	getPermissionClient = getClient
	resolveRPC          = runtime.ResolveRPC
)

const defaultPermissionListLimit = 50

type permissionClient interface {
	Close() error
	PermissionAPI() rpcclient.PermissionAPI
}

func SetOutput(writer io.Writer) io.Writer {
	previous := permissionOutput
	if writer == nil {
		permissionOutput = io.Discard
	} else {
		permissionOutput = writer
	}
	return previous
}

func NewListCommand() *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "List approval requests",
		Flags: permissionListFlags(true),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			query, err := getApprovalQuery(cmd, "")
			if err != nil {
				return err
			}
			client, err := getPermissionClient(ctx, cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()
			ctx = permissionContext(ctx)
			requests, err := client.PermissionAPI().ListApprovalRequests(ctx, query)
			if err != nil {
				return err
			}
			return writeRequests(requests)
		}}
}

func NewPendingCommand() *cli.Command {
	return &cli.Command{
		Name:  "pending",
		Usage: "List pending approval requests",
		Flags: permissionListFlags(false),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			query, err := getApprovalQuery(cmd, permissions.ApprovalPending)
			if err != nil {
				return err
			}
			client, err := getPermissionClient(ctx, cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()
			ctx = permissionContext(ctx)
			requests, err := client.PermissionAPI().ListApprovalRequests(ctx, query)
			if err != nil {
				return err
			}
			return writeRequests(requests)
		}}
}

func NewGrantsCommand() *cli.Command {
	return &cli.Command{
		Name:  "grants",
		Usage: "List approval grants",
		Flags: permissionListFlags(true),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			query, err := getGrantQuery(cmd)
			if err != nil {
				return err
			}
			client, err := getPermissionClient(ctx, cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()
			ctx = permissionContext(ctx)
			grants, err := client.PermissionAPI().ListApprovalGrants(ctx, query)
			if err != nil {
				return err
			}
			return writeGrants(grants)
		}}
}

func NewInspectCommand() *cli.Command {
	return &cli.Command{
		Name:      "inspect",
		Usage:     "Inspect an approval grant",
		ArgsUsage: "[approval-or-grant-id]",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			id, err := getRequiredID(cmd)
			if err != nil {
				return err
			}
			client, err := getPermissionClient(ctx, cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			grant, ok, err := client.PermissionAPI().GetApprovalGrant(permissionContext(ctx), id)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("approval grant not found")
			}
			return writeGrant(grant)
		},
	}
}

func NewPruneCommand() *cli.Command {
	return &cli.Command{
		Name: "prune", Usage: "Delete terminal approval history outside its retention window",
		Flags: []cli.Flag{&cli.BoolFlag{Name: "dry-run", Usage: "Report eligible records without deleting them"}},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			client, err := getPermissionClient(ctx, cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()
			result, err := client.PermissionAPI().PruneApprovals(permissionContext(ctx), cmd.Bool("dry-run"))
			if err != nil {
				return err
			}
			mode := "deleted"
			if result.DryRun {
				mode = "eligible"
			}
			_, err = fmt.Fprintf(permissionOutput,
				"%d requests and %d grants %s (request cutoff: %s; grant cutoff: %s)\n",
				result.Requests, result.Grants, mode,
				formatPermissionTime(result.RequestCutoff, "2006-01-02 15:04 MST"),
				formatPermissionTime(result.GrantCutoff, "2006-01-02 15:04 MST"),
			)
			return err
		},
	}
}

func NewPresetCommand() *cli.Command {
	return &cli.Command{
		Name:      "preset",
		Usage:     "Show or set the profile permission preset",
		ArgsUsage: "[ask|approve|full-access|custom]",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "yes", Usage: "Confirm enabling unrestricted full access"},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			cfg, inputs, err := morphcli.LoadConfig(cmd)
			if err != nil {
				return err
			}
			rawPreset := str.String(cmd.Args().First()).Trim()
			if rawPreset == "" {
				preset := cfg.Permissions.EffectivePreset()
				_, err = fmt.Fprintf(permissionOutput, "%s (%s)\n", cfg.Permissions.Label(), preset)
				return err
			}

			preset, err := permissions.ParsePreset(rawPreset)
			if err != nil {
				return err
			}
			if preset == permissions.PresetFullAccess && !cmd.Bool("yes") {
				return errors.New("full access is unsafe; rerun with --yes to confirm")
			}

			updates := []morphcli.ConfigUpdate{{
				Path:  "permissions.preset",
				Value: string(preset),
			}}
			if _, err := morphcli.SetConfigValues(inputs.EnvPath, inputs.ConfigPath, updates); err != nil {
				return err
			}

			policy := cfg.Permissions
			policy.Preset = preset
			_, err = fmt.Fprintf(permissionOutput, "permission preset set to %s\n", policy.Label())
			return err
		},
	}
}

func permissionListFlags(includeStatus bool) []cli.Flag {
	flags := []cli.Flag{
		&cli.IntFlag{Name: "limit", Value: defaultPermissionListLimit, Usage: "Maximum records to return"},
		&cli.IntFlag{Name: "offset", Usage: "Records to skip for pagination"},
	}
	if includeStatus {
		flags = append(flags, &cli.StringFlag{Name: "status", Usage: "Filter by status"})
	}
	return flags
}

func getApprovalQuery(cmd *cli.Command, fixedStatus permissions.ApprovalStatus) (permissions.ApprovalQuery, error) {
	limit, offset := cmd.Int("limit"), cmd.Int("offset")
	if limit <= 0 || limit > 500 {
		return permissions.ApprovalQuery{}, fmt.Errorf("limit must be between 1 and 500")
	}
	if offset < 0 {
		return permissions.ApprovalQuery{}, fmt.Errorf("offset must be greater than or equal to zero")
	}
	status := fixedStatus
	if status == "" {
		status = permissions.ApprovalStatus(str.String(cmd.String("status")).Normalized())
	}
	if status != "" && !isApprovalStatus(status) {
		return permissions.ApprovalQuery{}, fmt.Errorf("invalid approval status")
	}
	return permissions.ApprovalQuery{Status: status, Limit: limit, Offset: offset}, nil
}

func getGrantQuery(cmd *cli.Command) (permissions.GrantQuery, error) {
	limit, offset := cmd.Int("limit"), cmd.Int("offset")
	if limit <= 0 || limit > 500 {
		return permissions.GrantQuery{}, fmt.Errorf("limit must be between 1 and 500")
	}
	if offset < 0 {
		return permissions.GrantQuery{}, fmt.Errorf("offset must be greater than or equal to zero")
	}
	status := permissions.GrantStatus(str.String(cmd.String("status")).Normalized())
	if status != "" && !isGrantStatus(status) {
		return permissions.GrantQuery{}, fmt.Errorf("invalid grant status")
	}
	return permissions.GrantQuery{Status: status, Limit: limit, Offset: offset}, nil
}

func isApprovalStatus(status permissions.ApprovalStatus) bool {
	return status == permissions.ApprovalPending || status == permissions.ApprovalApproved ||
		status == permissions.ApprovalDenied || status == permissions.ApprovalExpired ||
		status == permissions.ApprovalCancelled || status == permissions.ApprovalFailed
}

func isGrantStatus(status permissions.GrantStatus) bool {
	return status == permissions.GrantActive || status == permissions.GrantConsumed ||
		status == permissions.GrantExpired || status == permissions.GrantRevoked
}

func NewApproveCommand() *cli.Command {
	return &cli.Command{
		Name:      "approve",
		Usage:     "Approve a pending request",
		ArgsUsage: "[request-id]",
		Flags: []cli.Flag{&cli.StringFlag{
			Name:  "scope",
			Value: string(permissions.GrantOnce),
			Usage: "once, session, or always",
		}},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			id, err := getRequiredID(cmd)
			if err != nil {
				return err
			}
			scope := permissions.GrantScope(str.String(cmd.String("scope")).Normalized())
			if scope != permissions.GrantOnce && scope != permissions.GrantSession && scope != permissions.GrantAlways {
				return errors.New("approval scope must be one of: once, session, always")
			}
			client, err := getPermissionClient(ctx, cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()
			ctx = permissionContext(ctx)
			request, err := client.PermissionAPI().ResolveApprovalRequest(
				ctx, id, true, scope,
			)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(permissionOutput, "approved %s (%s)\n", request.ID, request.Scope)
			return err
		},
	}
}

func NewDenyCommand() *cli.Command {
	return &cli.Command{
		Name:      "deny",
		Usage:     "Deny a pending request",
		ArgsUsage: "[request-id]",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			id, err := getRequiredID(cmd)
			if err != nil {
				return err
			}
			client, err := getPermissionClient(ctx, cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()
			ctx = permissionContext(ctx)
			request, err := client.PermissionAPI().ResolveApprovalRequest(ctx, id, false, "")
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(permissionOutput, "denied %s\n", request.ID)
			return err
		}}
}

func NewRevokeCommand() *cli.Command {
	return &cli.Command{
		Name:      "revoke",
		Usage:     "Revoke an active grant by approval or grant ID",
		ArgsUsage: "[approval-or-grant-id]", Action: func(ctx context.Context, cmd *cli.Command) error {
			id, err := getRequiredID(cmd)
			if err != nil {
				return err
			}
			client, err := getPermissionClient(ctx, cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()
			ctx = permissionContext(ctx)
			grant, err := client.PermissionAPI().RevokeApprovalGrant(ctx, id)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(permissionOutput, "revoked %s\n", grant.ID)
			return err
		}}
}

func NewDeleteCommand() *cli.Command {
	return &cli.Command{
		Name:      "delete",
		Usage:     "Delete a terminal approval request or grant",
		ArgsUsage: "[approval-or-grant-id]",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			id, err := getRequiredID(cmd)
			if err != nil {
				return err
			}
			client, err := getPermissionClient(ctx, cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()
			result, err := client.PermissionAPI().DeleteApprovalRecord(permissionContext(ctx), id)
			if err != nil {
				return err
			}
			if result.LinkedGrantID != "" {
				_, err = fmt.Fprintf(
					permissionOutput, "deleted %s %s and linked grant %s\n",
					result.Kind, result.ID, result.LinkedGrantID,
				)
				return err
			}
			_, err = fmt.Fprintf(permissionOutput, "deleted %s %s\n", result.Kind, result.ID)
			return err
		},
	}
}

func NewExplainCommand() *cli.Command {
	return &cli.Command{
		Name:      "explain",
		Usage:     "Explain an approval request",
		ArgsUsage: "[request-id]",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			id, err := getRequiredID(cmd)
			if err != nil {
				return err
			}
			client, err := getPermissionClient(ctx, cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()
			ctx = permissionContext(ctx)
			request, ok, err := client.PermissionAPI().GetApprovalRequest(ctx, id)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("approval request not found")
			}
			_, err = fmt.Fprintf(permissionOutput, "%s\nStatus: %s\nEffects: %s\nReason: %s\nExpires: %s\n",
				request.Summary,
				request.Status,
				effectsText(request.Effects),
				request.Reason,
				formatPermissionTime(request.ExpiresAt, "2006-01-02 15:04:05 MST"))
			return err
		}}
}

func getClient(ctx context.Context, cmd *cli.Command) (permissionClient, error) {
	cfg, inputs, err := morphcli.LoadConfig(cmd)
	if err != nil {
		return nil, err
	}
	morphcli.ApplyConfigOverrides(cmd, cfg)
	morphcli.AddStartupFilesystemRoots(cfg, inputs)
	endpoint, err := resolveRPC(ctx, cmd, cfg)
	if err != nil {
		return nil, err
	}
	cfg.RPC = endpoint
	config.Set(cfg)
	return newClient(ctx, cfg)
}

func permissionContext(ctx context.Context) context.Context {
	return rpcmeta.WithOutgoingPermissionSurface(ctx, permissions.SurfaceCLI)
}

func getRequiredID(cmd *cli.Command) (string, error) {
	id := str.String(cmd.Args().First()).Trim()
	if id == "" {
		return "", fmt.Errorf("approval or grant id is required")
	}
	return id, nil
}

func writeRequests(requests []permissions.ApprovalRequest) error {
	if len(requests) == 0 {
		_, err := fmt.Fprintln(permissionOutput, "No approval requests.")
		return err
	}

	w := tabwriter.NewWriter(permissionOutput, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "REQUEST\tSTATUS\tOPERATION\tEFFECTS\tEXPIRES")
	for _, request := range requests {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", request.ID, request.Status, request.Summary,
			effectsText(request.Effects), formatPermissionTime(request.ExpiresAt, "2006-01-02 15:04 MST"))
	}

	return w.Flush()
}

func writeGrants(grants []permissions.ApprovalGrant) error {
	if len(grants) == 0 {
		_, err := fmt.Fprintln(permissionOutput, "No approval grants.")
		return err
	}

	w := tabwriter.NewWriter(permissionOutput, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "GRANT\tSTATUS\tSCOPE\tOPS\tSESSION\tEXPIRES")
	for _, grant := range grants {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\n", grant.ID, grant.Status, grant.Scope,
			len(grant.Operations), grant.SessionID, getGrantExpiryText(grant))
	}

	return w.Flush()
}

func writeGrant(grant permissions.ApprovalGrant) error {
	w := tabwriter.NewWriter(permissionOutput, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "Grant")
	_, _ = fmt.Fprintf(w, "  ID:\t%s\n", grant.ID)
	_, _ = fmt.Fprintf(w, "  Status:\t%s\n", grant.Status)
	_, _ = fmt.Fprintf(w, "  Scope:\t%s\n", grant.Scope)
	_, _ = fmt.Fprintf(w, "  Request:\t%s\n", permissionValue(grant.RequestID))
	_, _ = fmt.Fprintf(w, "  Fingerprint:\t%s\n", permissionValue(grant.Fingerprint))
	_, _ = fmt.Fprintf(w, "  Actor:\t%s\n", getGrantActorText(grant.Actor))
	_, _ = fmt.Fprintf(w, "  Profile:\t%s\n", permissionValue(grant.Profile))
	_, _ = fmt.Fprintf(w, "  Session:\t%s\n", permissionValue(grant.SessionID))
	_, _ = fmt.Fprintf(w, "  Created:\t%s\n", formatGrantTime(grant.CreatedAt))
	_, _ = fmt.Fprintf(w, "  Expires:\t%s\n", getGrantDetailedExpiryText(grant))
	_, _ = fmt.Fprintf(w, "  Consumed:\t%s\n", formatGrantTime(grant.ConsumedAt))
	_, _ = fmt.Fprintf(w, "  Revoked:\t%s\n", formatGrantTime(grant.RevokedAt))
	_, _ = fmt.Fprintf(w, "\nOperations (%d)\n", len(grant.Operations))
	if len(grant.Operations) == 0 {
		_, _ = fmt.Fprintln(w, "  None")
		return w.Flush()
	}
	for index, operation := range grant.Operations {
		_, _ = fmt.Fprintf(w, "  %d. %s\n", index+1, operation)
	}
	return w.Flush()
}

func getGrantActorText(actor permissions.Actor) string {
	switch {
	case actor.Kind == "":
		return permissionValue(actor.ID)
	case actor.ID == "":
		return string(actor.Kind)
	default:
		return string(actor.Kind) + " · " + actor.ID
	}
}

func getGrantExpiryText(grant permissions.ApprovalGrant) string {
	if grant.Scope == permissions.GrantAlways {
		return "never"
	}

	return formatPermissionTime(grant.ExpiresAt, "2006-01-02 15:04 MST")
}

func getGrantDetailedExpiryText(grant permissions.ApprovalGrant) string {
	if grant.Scope == permissions.GrantAlways {
		return "never"
	}
	return formatGrantTime(grant.ExpiresAt)
}

func formatGrantTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return formatPermissionTime(value, "2006-01-02 15:04:05 MST")
}

func permissionValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func formatPermissionTime(value time.Time, layout string) string {
	return value.In(time.Local).Format(layout)
}

func effectsText(effects []permissions.Effect) string {
	values := make([]string, len(effects))
	for index, effect := range effects {
		values[index] = string(effect)
	}

	return strings.Join(values, ",")
}
