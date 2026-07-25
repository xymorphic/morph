package authcmd

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	cli "github.com/urfave/cli/v3"

	morphauth "github.com/wandxy/morph/internal/auth"
	morphcli "github.com/wandxy/morph/internal/cli"
	"github.com/wandxy/morph/internal/config"
	"github.com/wandxy/morph/internal/credential"
	"github.com/wandxy/morph/internal/datadir"
	"github.com/wandxy/morph/internal/permissions"
	"github.com/wandxy/morph/internal/profile"
	rpcclient "github.com/wandxy/morph/internal/rpc/client"
	morphpb "github.com/wandxy/morph/internal/rpc/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type morphAuthClient interface {
	Close() error
	AuthAPI() rpcclient.AuthAPI
}

var newMorphAuthClient = func(
	ctx context.Context,
	cfg *config.Config,
	methods []string,
) (morphAuthClient, error) {
	return rpcclient.NewClient(ctx, rpcclient.OptionsWithConfigAuth(rpcclient.Options{
		Address: cfg.RPC.Address, Port: cfg.RPC.Port,
		PermissionSurface: permissions.SurfaceCLI,
		PermissionPreset:  cfg.Permissions.EffectivePreset(),
		AuthMethods:       append([]string(nil), methods...),
	}, cfg))
}

func newIdentityCommand() *cli.Command {
	return &cli.Command{
		Name: "identity", Usage: "Manage the Morph profile identity",
		Commands: []*cli.Command{
			{
				Name: "init", Usage: "Initialize the profile identity",
				Flags: []cli.Flag{morphcli.ProfileFlag()},
				Action: func(_ context.Context, cmd *cli.Command) error {
					identity, err := getEffectiveIdentity(cmd, true)
					if err != nil {
						return err
					}
					return writeIdentityResult(cmd, identity)
				},
			},
			{
				Name: "show", Usage: "Show the safe profile identity",
				Flags: []cli.Flag{morphcli.ProfileFlag()},
				Action: func(_ context.Context, cmd *cli.Command) error {
					identity, err := getEffectiveIdentity(cmd, false)
					if err != nil {
						return err
					}
					return writeSafeJSONOrText(cmd, map[string]any{
						"identity_id": identity.ID,
						"generation":  identity.Generation,
					}, "identity: %s\ngeneration: %d\n", identity.ID, identity.Generation)
				},
			},
			{
				Name: "rotate", Usage: "Rotate the profile identity and revoke prior authorization",
				Flags:  []cli.Flag{morphcli.ProfileFlag()},
				Action: rotateIdentity,
			},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
	}
}

func rotateIdentity(ctx context.Context, cmd *cli.Command) error {
	cfg, err := loadAuthConfig(cmd)
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Auth.Key) != "" {
		return errors.New("configured Morph identity keys must be rotated at their source")
	}
	store, err := getAuthStore(cmd)
	if err != nil {
		return err
	}
	databasePath := datadir.AuthDBPath()
	databaseExists := true
	if _, statErr := os.Stat(databasePath); statErr != nil {
		if !os.IsNotExist(statErr) {
			return fmt.Errorf("inspect RPC auth database: %w", statErr)
		}
		databaseExists = false
	}
	currentRecord, next, err := loadOrPrepareIdentityRotation(store)
	if err != nil {
		return err
	}
	if databaseExists {
		activePending, checkErr := checkPendingIdentityActive(ctx, cfg, next)
		if checkErr != nil {
			return checkErr
		}
		if activePending {
			if err := store.ActivateIdentityRotation(next.ID); err != nil {
				return err
			}
			return writeIdentityResult(cmd, next)
		}
		err := withMorphAuthAPI(ctx, cmd, []string{
			morphpb.AuthService_RotateIdentity_FullMethodName,
		}, func(api rpcclient.AuthAPI) error {
			_, rotateErr := api.RotateIdentity(
				ctx, currentRecord.IdentityID, next.ID, next.PublicKey, next.Generation,
			)
			return rotateErr
		})
		if err != nil {
			if shouldAbortIdentityRotation(err) {
				_ = store.AbortIdentityRotation(next.ID)
			}
			return err
		}
	}
	if err := store.ActivateIdentityRotation(next.ID); err != nil {
		return err
	}

	return writeIdentityResult(cmd, next)
}

func loadOrPrepareIdentityRotation(
	store *credential.FileStore,
) (credential.MorphAuthRecord, morphauth.Identity, error) {
	record, found, err := store.LoadMorphAuth()
	if err != nil {
		return credential.MorphAuthRecord{}, morphauth.Identity{}, err
	}
	if found && record.Pending != nil {
		identity, err := morphauth.ParseIdentity(
			[]byte(record.Pending.PrivateKey),
			record.Pending.Generation,
		)
		if err != nil || identity.ID != record.Pending.IdentityID {
			return credential.MorphAuthRecord{}, morphauth.Identity{},
				errors.New("pending Morph identity rotation is invalid")
		}
		return record, identity, nil
	}
	return store.PrepareIdentityRotation()
}

func checkPendingIdentityActive(
	ctx context.Context,
	cfg *config.Config,
	identity morphauth.Identity,
) (bool, error) {
	pendingConfig := *cfg
	privateKey, err := morphauth.MarshalIdentity(identity)
	if err != nil {
		return false, err
	}
	pendingConfig.Auth.Key = string(privateKey)
	pendingConfig.Auth.Generation = identity.Generation
	client, err := newMorphAuthClient(ctx, &pendingConfig, []string{
		morphpb.AuthService_IdentityStatus_FullMethodName,
	})
	if err != nil {
		return false, err
	}
	defer func() { _ = client.Close() }()
	result, err := client.AuthAPI().IdentityStatus(ctx)
	if err != nil {
		switch status.Code(err) {
		case codes.Unauthenticated, codes.PermissionDenied, codes.NotFound, codes.Unimplemented:
			return false, nil
		default:
			return false, err
		}
	}
	return result.GetIdentityId() == identity.ID &&
		result.GetGeneration() == identity.Generation &&
		result.GetStatus() == morphauth.StatusActive, nil
}

func shouldAbortIdentityRotation(err error) bool {
	switch status.Code(err) {
	case codes.InvalidArgument, codes.PermissionDenied, codes.FailedPrecondition,
		codes.Unauthenticated, codes.NotFound, codes.Unimplemented:
		return true
	default:
		return false
	}
}

func newSessionCommand() *cli.Command {
	return &cli.Command{
		Name: "session", Usage: "Inspect and revoke RPC auth sessions",
		Commands: []*cli.Command{
			{
				Name: "list", Usage: "List RPC auth sessions",
				Flags: []cli.Flag{morphcli.ProfileFlag()},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return withMorphAuthAPI(ctx, cmd, []string{
						morphpb.AuthService_ListSessions_FullMethodName,
					}, func(api rpcclient.AuthAPI) error {
						sessions, err := api.ListSessions(ctx)
						if err != nil {
							return err
						}
						if cmd.Bool("json") {
							return writeJSONValue(sessions)
						}
						writer := tabwriter.NewWriter(authOutput, 0, 4, 2, ' ', 0)
						_, _ = fmt.Fprintln(writer, "ID\tIDENTITY\tSTATUS\tEXPIRES")
						for _, session := range sessions {
							_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n",
								session.GetId(), session.GetIdentityId(), session.GetStatus(),
								formatProtoTime(session.GetAbsoluteExpiresAt()))
						}
						return writer.Flush()
					})
				},
			},
			{
				Name: "revoke", Usage: "Revoke an RPC auth session", ArgsUsage: "<session-id>",
				Flags: []cli.Flag{morphcli.ProfileFlag(), &cli.StringFlag{Name: "reason"}},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					id := strings.TrimSpace(cmd.Args().First())
					if id == "" {
						return errors.New("auth session ID is required")
					}
					return withMorphAuthAPI(ctx, cmd, []string{
						morphpb.AuthService_RevokeSession_FullMethodName,
					}, func(api rpcclient.AuthAPI) error {
						session, err := api.RevokeSession(ctx, id, cmd.String("reason"))
						if err != nil {
							return err
						}
						return writeSafeJSONOrText(cmd, session, "revoked auth session %s\n", session.GetId())
					})
				},
			},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
	}
}

func newTokenCommand() *cli.Command {
	return &cli.Command{
		Name: "token", Usage: "Inspect and revoke RPC access tokens",
		Commands: []*cli.Command{
			{
				Name: "generate", Usage: "Generate a signed RPC access token",
				Flags: []cli.Flag{
					morphcli.ProfileFlag(),
					&cli.DurationFlag{Name: "ttl", Value: 5 * time.Minute},
					&cli.StringFlag{Name: "owner"},
					&cli.StringFlag{Name: "user"},
					&cli.StringFlag{Name: "session"},
					&cli.StringFlag{Name: "source", Value: "cli"},
					&cli.StringSliceFlag{Name: "role", Value: []string{morphauth.RoleOwner}},
					&cli.StringSliceFlag{Name: "service"},
					&cli.StringSliceFlag{Name: "method"},
					&cli.UintFlag{Name: "authorization-revision", Value: 1},
					&cli.StringFlag{Name: "output", Aliases: []string{"o"}},
				},
				Action: generateToken,
			},
			{
				Name: "list", Usage: "List safe RPC token metadata",
				Flags: []cli.Flag{morphcli.ProfileFlag()},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return withMorphAuthAPI(ctx, cmd, []string{
						morphpb.AuthService_ListTokens_FullMethodName,
					}, func(api rpcclient.AuthAPI) error {
						tokens, err := api.ListTokens(ctx)
						if err != nil {
							return err
						}
						if cmd.Bool("json") {
							return writeJSONValue(tokens)
						}
						writer := tabwriter.NewWriter(authOutput, 0, 4, 2, ' ', 0)
						_, _ = fmt.Fprintln(writer, "ID\tSESSION\tSTATUS\tEXPIRES")
						for _, token := range tokens {
							_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n",
								token.GetId(), token.GetSessionId(), token.GetStatus(),
								formatProtoTime(token.GetExpiresAt()))
						}
						return writer.Flush()
					})
				},
			},
			{
				Name: "revoke", Usage: "Revoke an RPC access token", ArgsUsage: "<token-id>",
				Flags: []cli.Flag{morphcli.ProfileFlag(), &cli.StringFlag{Name: "reason"}},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					id := strings.TrimSpace(cmd.Args().First())
					if id == "" {
						return errors.New("auth token ID is required")
					}
					return withMorphAuthAPI(ctx, cmd, []string{
						morphpb.AuthService_RevokeToken_FullMethodName,
					}, func(api rpcclient.AuthAPI) error {
						token, err := api.RevokeToken(ctx, id, cmd.String("reason"))
						if err != nil {
							return err
						}
						return writeSafeJSONOrText(cmd, token, "revoked auth token %s\n", token.GetId())
					})
				},
			},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
	}
}

func newAuthorizationCommand() *cli.Command {
	return &cli.Command{
		Name: "authorization", Usage: "Inspect RPC identity authorizations",
		Commands: []*cli.Command{
			{
				Name: "list", Usage: "List RPC identity authorizations",
				Flags: []cli.Flag{morphcli.ProfileFlag()},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return withMorphAuthAPI(ctx, cmd, []string{
						morphpb.AuthService_ListAuthorizations_FullMethodName,
					}, func(api rpcclient.AuthAPI) error {
						authorizations, err := api.ListAuthorizations(ctx)
						if err != nil {
							return err
						}
						return writeJSONValue(getAuthorizationOutputs(authorizations))
					})
				},
			},
			{
				Name: "grant", Usage: "Grant a bounded RPC identity authorization",
				Flags: []cli.Flag{
					morphcli.ProfileFlag(),
					&cli.StringFlag{Name: "identity", Required: true},
					&cli.StringFlag{Name: "public-key", Required: true},
					&cli.StringFlag{Name: "owner", Required: true},
					&cli.StringFlag{Name: "user", Required: true},
					&cli.StringSliceFlag{Name: "role", Required: true},
					&cli.StringSliceFlag{Name: "service"},
					&cli.StringSliceFlag{Name: "method"},
					&cli.DurationFlag{Name: "maximum-ttl", Value: time.Hour},
					&cli.UintFlag{Name: "generation", Value: 1},
				},
				Action: grantAuthorization,
			},
			{
				Name: "revoke", Usage: "Revoke an RPC identity authorization",
				ArgsUsage: "<identity-id>",
				Flags:     []cli.Flag{morphcli.ProfileFlag(), &cli.StringFlag{Name: "reason"}},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					identityID := strings.TrimSpace(cmd.Args().First())
					if identityID == "" {
						return errors.New("identity ID is required")
					}
					return withMorphAuthAPI(ctx, cmd, []string{
						morphpb.AuthService_RevokeAuthorization_FullMethodName,
					}, func(api rpcclient.AuthAPI) error {
						authorization, err := api.RevokeAuthorization(
							ctx, identityID, cmd.String("reason"),
						)
						if err != nil {
							return err
						}
						return writeJSONValue(getAuthorizationOutput(authorization))
					})
				},
			},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
	}
}

func newAuditCommand() *cli.Command {
	return &cli.Command{
		Name: "audit", Usage: "Inspect RPC authentication audit events",
		Commands: []*cli.Command{
			{
				Name: "list", Usage: "List RPC authentication audit events",
				Flags: []cli.Flag{morphcli.ProfileFlag(), &cli.IntFlag{Name: "limit", Value: 25}},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return withMorphAuthAPI(ctx, cmd, []string{
						morphpb.AuthService_ListAudit_FullMethodName,
					}, func(api rpcclient.AuthAPI) error {
						events, err := api.ListAudit(ctx, int32(cmd.Int("limit")))
						if err != nil {
							return err
						}
						if cmd.Bool("json") {
							return writeJSONValue(events)
						}
						return writeAuditList(events)
					})
				},
			},
			{
				Name: "prune", Usage: "Prune expired authentication state and audit history",
				Flags: []cli.Flag{
					morphcli.ProfileFlag(),
					&cli.DurationFlag{Name: "older-than", Value: 30 * 24 * time.Hour},
					&cli.IntFlag{Name: "limit", Value: 1000},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return withMorphAuthAPI(ctx, cmd, []string{
						morphpb.AuthService_PruneAudit_FullMethodName,
					}, func(api rpcclient.AuthAPI) error {
						pruned, err := api.PruneAudit(
							ctx, time.Now().Add(-cmd.Duration("older-than")), int32(cmd.Int("limit")),
						)
						if err != nil {
							return err
						}
						return writeSafeJSONOrText(cmd, map[string]any{
							"pruned": pruned,
						}, "pruned: %d\n", pruned)
					})
				},
			},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
	}
}

func newMTLSCommand() *cli.Command {
	return &cli.Command{
		Name: "mtls", Usage: "Inspect RPC TLS configuration",
		Commands: []*cli.Command{{
			Name: "status", Usage: "Show the configured RPC TLS mode",
			Flags: []cli.Flag{morphcli.ProfileFlag()},
			Action: func(_ context.Context, cmd *cli.Command) error {
				cfg, err := loadAuthConfig(cmd)
				if err != nil {
					return err
				}
				return writeSafeJSONOrText(cmd, map[string]any{
					"mode":        cfg.Auth.TLS.Mode,
					"server_name": cfg.Auth.TLS.ServerName,
				}, "mode: %s\nserver name: %s\n", cfg.Auth.TLS.Mode, cfg.Auth.TLS.ServerName)
			},
		}},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
	}
}

func withMorphAuthAPI(
	ctx context.Context,
	cmd *cli.Command,
	methods []string,
	fn func(rpcclient.AuthAPI) error,
) error {
	if _, err := getAuthStore(cmd); err != nil {
		return err
	}
	cfg, err := loadAuthConfig(cmd)
	if err != nil {
		return err
	}
	client, err := newMorphAuthClient(ctx, cfg, methods)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	return fn(client.AuthAPI())
}

func generateToken(_ context.Context, cmd *cli.Command) error {
	identity, err := getEffectiveIdentity(cmd, true)
	if err != nil {
		return err
	}
	cfg, err := loadAuthConfig(cmd)
	if err != nil {
		return err
	}
	ownerID := strings.TrimSpace(cmd.String("owner"))
	if ownerID == "" {
		ownerID = profile.Active().Name
	}
	userID := strings.TrimSpace(cmd.String("user"))
	if userID == "" {
		userID = identity.ID
	}
	sessionID := strings.TrimSpace(cmd.String("session"))
	if sessionID == "" {
		sessionID, err = randomAuthID()
		if err != nil {
			return err
		}
	}
	services := cmd.StringSlice("service")
	methods := cmd.StringSlice("method")
	if len(services) == 0 && len(methods) == 0 {
		services = []string{morphauth.RootScope}
	} else {
		methods = appendMissingAuthMethod(
			methods,
			morphpb.AuthService_OpenSession_FullMethodName,
		)
	}
	ttl := cmd.Duration("ttl")
	if ttl <= 0 || ttl > cfg.Auth.MaximumTokenTTL {
		return fmt.Errorf("token TTL must be greater than zero and at most %s",
			cfg.Auth.MaximumTokenTTL)
	}
	token, _, err := morphauth.SignAccessToken(identity, morphauth.TokenRequest{
		Audience: cfg.Auth.Audience, Subject: userID, SessionID: sessionID,
		OwnerID: ownerID, Source: cmd.String("source"), Roles: cmd.StringSlice("role"),
		Services: services, Methods: methods, TTL: ttl,
		NonceBytes:            cfg.Auth.NonceBytes,
		AuthorizationRevision: uint64(cmd.Uint("authorization-revision")),
	})
	if err != nil {
		return err
	}
	outputPath := strings.TrimSpace(cmd.String("output"))
	if outputPath != "" {
		file, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("write access token: %w", err)
		}
		if _, err := file.WriteString(token + "\n"); err != nil {
			_ = file.Close()
			_ = os.Remove(outputPath)
			return fmt.Errorf("write access token: %w", err)
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(outputPath)
			return fmt.Errorf("write access token: %w", err)
		}
		return nil
	}
	if cmd.Bool("json") {
		return writeJSONValue(map[string]string{"token": token})
	}
	_, err = fmt.Fprintln(authOutput, token)

	return err
}

func appendMissingAuthMethod(methods []string, target string) []string {
	for _, method := range methods {
		if method == target {
			return methods
		}
	}

	return append(methods, target)
}

func getEffectiveIdentity(cmd *cli.Command, create bool) (morphauth.Identity, error) {
	store, err := getAuthStore(cmd)
	if err != nil {
		return morphauth.Identity{}, err
	}
	cfg, err := loadAuthConfig(cmd)
	if err != nil {
		return morphauth.Identity{}, err
	}
	key := strings.TrimSpace(cfg.Auth.Key)
	if key != "" {
		body := []byte(key)
		if !morphauth.IsEncodedIdentity(body) {
			body, err = os.ReadFile(key)
			if err != nil {
				return morphauth.Identity{}, fmt.Errorf("read Morph identity key: %w", err)
			}
		}
		return morphauth.ParseIdentity(body, cfg.Auth.Generation)
	}
	if create {
		return store.LoadOrCreateIdentity()
	}
	record, found, err := store.LoadMorphAuth()
	if err != nil {
		return morphauth.Identity{}, err
	}
	if !found {
		return morphauth.Identity{}, errors.New("morph identity is not initialized")
	}

	return morphauth.ParseIdentity([]byte(record.PrivateKey), record.Generation)
}

func grantAuthorization(ctx context.Context, cmd *cli.Command) error {
	publicKey, err := hex.DecodeString(strings.TrimSpace(cmd.String("public-key")))
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return errors.New("public key must be 64 hexadecimal characters")
	}
	authorization := &morphpb.AuthAuthorization{
		IdentityId: strings.TrimSpace(cmd.String("identity")),
		PublicKey:  publicKey, OwnerId: strings.TrimSpace(cmd.String("owner")),
		UserId: strings.TrimSpace(cmd.String("user")), Roles: cmd.StringSlice("role"),
		Services: cmd.StringSlice("service"), Methods: cmd.StringSlice("method"),
		MaximumTtlSeconds: int64(cmd.Duration("maximum-ttl") / time.Second),
		Generation:        uint64(cmd.Uint("generation")), Status: morphauth.StatusActive,
	}
	return withMorphAuthAPI(ctx, cmd, []string{
		morphpb.AuthService_GrantAuthorization_FullMethodName,
	}, func(api rpcclient.AuthAPI) error {
		result, err := api.GrantAuthorization(ctx, authorization)
		if err != nil {
			return err
		}
		return writeJSONValue(getAuthorizationOutput(result))
	})
}

func randomAuthID() (string, error) {
	body := make([]byte, 24)
	if _, err := rand.Read(body); err != nil {
		return "", err
	}

	return hex.EncodeToString(body), nil
}

func writeIdentityResult(cmd *cli.Command, identity morphauth.Identity) error {
	return writeSafeJSONOrText(cmd, map[string]any{
		"identity_id": identity.ID,
		"generation":  identity.Generation,
	}, "identity: %s\ngeneration: %d\n", identity.ID, identity.Generation)
}

func writeSafeJSONOrText(
	cmd *cli.Command,
	value any,
	format string,
	args ...any,
) error {
	if cmd.Bool("json") {
		return writeJSONValue(value)
	}
	_, err := fmt.Fprintf(authOutput, format, args...)
	return err
}

func writeJSONValue(value any) error {
	encoder := json.NewEncoder(authOutput)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func writeAuditList(events []*morphpb.AuthAuditEvent) error {
	_, err := fmt.Fprint(authOutput, auditListToText(events))
	return err
}

func auditListToText(events []*morphpb.AuthAuditEvent) string {
	if len(events) == 0 {
		return "No RPC authentication audit events found.\n"
	}

	var output strings.Builder
	output.WriteString("RPC authentication audit\n")
	for index, event := range events {
		if index > 0 {
			output.WriteByte('\n')
		}
		fmt.Fprintf(
			&output,
			"[%s] %s\n",
			getAuthDisplayText(event.GetType()),
			getAuthDisplayText(formatProtoTime(event.GetCreatedAt())),
		)
		appendAuditField(&output, "Event ID", event.GetId())
		appendAuditField(&output, "Identity", event.GetIdentityId())
		appendAuditField(&output, "Session", event.GetSessionId())
		appendAuditField(&output, "Token", event.GetTokenId())
		appendAuditField(&output, "Method", event.GetMethod())
		appendAuditField(&output, "Reason", event.GetReason())
	}

	return output.String()
}

func appendAuditField(output *strings.Builder, label, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	fmt.Fprintf(output, "  %-12s %s\n", label+":", getAuthDisplayText(value))
}

func getAuthDisplayText(value string) string {
	if value == "" {
		return "-"
	}
	if strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\t") {
		return strconv.Quote(value)
	}

	return value
}

type authorizationOutput struct {
	*morphpb.AuthAuthorization
	PublicKey string `json:"public_key"`
}

func getAuthorizationOutput(
	authorization *morphpb.AuthAuthorization,
) authorizationOutput {
	return authorizationOutput{
		AuthAuthorization: authorization,
		PublicKey:         hex.EncodeToString(authorization.GetPublicKey()),
	}
}

func getAuthorizationOutputs(
	authorizations []*morphpb.AuthAuthorization,
) []authorizationOutput {
	outputs := make([]authorizationOutput, 0, len(authorizations))
	for _, authorization := range authorizations {
		outputs = append(outputs, getAuthorizationOutput(authorization))
	}

	return outputs
}

func formatProtoTime(value *timestamppb.Timestamp) string {
	if value == nil {
		return ""
	}

	return value.AsTime().Local().Format(time.RFC3339)
}
