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
	"time"

	cli "github.com/urfave/cli/v3"

	morphauth "github.com/xymorphic/morph/internal/auth"
	morphcli "github.com/xymorphic/morph/internal/cli"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/credential"
	"github.com/xymorphic/morph/internal/datadir"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/internal/profile"
	rpcclient "github.com/xymorphic/morph/internal/rpc/client"
	morphpb "github.com/xymorphic/morph/internal/rpc/proto"
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

var generateMorphIdentity = morphauth.GenerateIdentity

type generatedKeyOutput struct {
	IdentityID   string `json:"identity_id"`
	PrivateKey32 string `json:"private_key_32"`
	PrivateKey64 string `json:"private_key_64"`
	PublicKey    string `json:"public_key"`
}

func newGenerateKeyCommand() *cli.Command {
	return &cli.Command{
		Name:  "genkey",
		Usage: "Generate and print an Ed25519 RPC identity keypair",
		Action: func(_ context.Context, cmd *cli.Command) error {
			identity, err := generateMorphIdentity(1)
			if err != nil {
				return err
			}
			output := generatedKeyOutput{
				IdentityID:   identity.ID,
				PrivateKey32: hex.EncodeToString(identity.PrivateKey.Seed()),
				PrivateKey64: hex.EncodeToString(identity.PrivateKey),
				PublicKey:    hex.EncodeToString(identity.PublicKey),
			}
			if cmd.Bool("json") {
				return writeJSONValue(output)
			}
			_, err = fmt.Fprintf(
				authOutput,
				"identity: %s\nprivate key (32-byte seed): %s\nprivate key (64 bytes): %s\npublic key (32 bytes): %s\n",
				output.IdentityID,
				output.PrivateKey32,
				output.PrivateKey64,
				output.PublicKey,
			)
			return err
		},
	}
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
				Flags: []cli.Flag{
					morphcli.ProfileFlag(),
					&cli.IntFlag{Name: "limit", Value: 25},
					&cli.StringFlag{Name: "status"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return withMorphAuthAPI(ctx, cmd, []string{
						morphpb.AuthService_ListSessions_FullMethodName,
					}, func(api rpcclient.AuthAPI) error {
						sessions, err := api.ListSessions(ctx, rpcclient.AuthSessionListOptions{
							Limit: int32(cmd.Int("limit")), Status: cmd.String("status"),
						})
						if err != nil {
							return err
						}
						if cmd.Bool("json") {
							return writeJSONValue(sessions)
						}
						return writeSessionList(sessions)
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
				Flags: []cli.Flag{
					morphcli.ProfileFlag(),
					&cli.IntFlag{Name: "limit", Value: 25},
					&cli.StringFlag{Name: "status"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return withMorphAuthAPI(ctx, cmd, []string{
						morphpb.AuthService_ListTokens_FullMethodName,
					}, func(api rpcclient.AuthAPI) error {
						tokens, err := api.ListTokens(ctx, rpcclient.AuthTokenListOptions{
							Limit: int32(cmd.Int("limit")), Status: cmd.String("status"),
						})
						if err != nil {
							return err
						}
						if cmd.Bool("json") {
							return writeJSONValue(tokens)
						}
						return writeTokenList(tokens)
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
				Flags: []cli.Flag{
					morphcli.ProfileFlag(),
					&cli.StringFlag{Name: "status"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return withMorphAuthAPI(ctx, cmd, []string{
						morphpb.AuthService_ListAuthorizations_FullMethodName,
					}, func(api rpcclient.AuthAPI) error {
						authorizations, err := api.ListAuthorizations(
							ctx,
							rpcclient.AuthAuthorizationListOptions{Status: cmd.String("status")},
						)
						if err != nil {
							return err
						}
						if cmd.Bool("json") {
							return writeJSONValue(getAuthorizationOutputs(authorizations))
						}
						return writeAuthorizationList(authorizations)
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
				Flags: []cli.Flag{
					morphcli.ProfileFlag(),
					&cli.IntFlag{Name: "limit", Value: 25},
					&cli.StringFlag{Name: "type"},
					&cli.StringFlag{Name: "identity"},
					&cli.StringFlag{Name: "session"},
					&cli.StringFlag{Name: "token"},
					&cli.StringFlag{Name: "method"},
					&cli.DurationFlag{Name: "since"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					sinceDuration := cmd.Duration("since")
					if sinceDuration < 0 {
						return errors.New("audit since duration must not be negative")
					}
					var since time.Time
					if sinceDuration > 0 {
						since = time.Now().Add(-sinceDuration)
					}
					return withMorphAuthAPI(ctx, cmd, []string{
						morphpb.AuthService_ListAudit_FullMethodName,
					}, func(api rpcclient.AuthAPI) error {
						events, err := api.ListAudit(ctx, rpcclient.AuthAuditListOptions{
							Limit:      int32(cmd.Int("limit")),
							Type:       cmd.String("type"),
							IdentityID: cmd.String("identity"),
							SessionID:  cmd.String("session"),
							TokenID:    cmd.String("token"),
							Method:     cmd.String("method"),
							Since:      since,
						})
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
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
	}
}

type authPruneOutput struct {
	Tokens         int32 `json:"tokens"`
	Sessions       int32 `json:"sessions"`
	Authorizations int32 `json:"authorizations"`
	AuditEvents    int32 `json:"audit_events"`
	Total          int32 `json:"total"`
	DryRun         bool  `json:"dry_run"`
}

func newPruneCommand() *cli.Command {
	return &cli.Command{
		Name: "prune", Usage: "Prune old terminal RPC authentication records",
		Flags: []cli.Flag{
			morphcli.ProfileFlag(),
			&cli.DurationFlag{Name: "older-than", Value: 30 * 24 * time.Hour},
			&cli.IntFlag{Name: "limit", Value: 1000},
			&cli.BoolFlag{Name: "dry-run"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Duration("older-than") < 0 {
				return errors.New("auth prune older-than duration must not be negative")
			}
			if cmd.Int("limit") <= 0 || cmd.Int("limit") > morphauth.MaximumPruneLimit {
				return fmt.Errorf(
					"auth prune limit must be between 1 and %d",
					morphauth.MaximumPruneLimit,
				)
			}
			return withMorphAuthAPI(ctx, cmd, []string{
				morphpb.AuthService_Prune_FullMethodName,
			}, func(api rpcclient.AuthAPI) error {
				result, err := api.Prune(ctx, rpcclient.AuthPruneOptions{
					Before: time.Now().Add(-cmd.Duration("older-than")),
					Limit:  int32(cmd.Int("limit")),
					DryRun: cmd.Bool("dry-run"),
				})
				if err != nil {
					return err
				}
				return writeAuthPruneResult(cmd, result)
			})
		},
	}
}

func writeAuthPruneResult(cmd *cli.Command, result *morphpb.PruneAuthResponse) error {
	output := authPruneOutput{
		Tokens:         result.GetTokens(),
		Sessions:       result.GetSessions(),
		Authorizations: result.GetAuthorizations(),
		AuditEvents:    result.GetAuditEvents(),
		DryRun:         result.GetDryRun(),
	}
	output.Total = output.Tokens + output.Sessions + output.Authorizations + output.AuditEvents
	if cmd.Bool("json") {
		return writeJSONValue(output)
	}
	_, err := fmt.Fprintf(
		authOutput,
		"Tokens:         %d\nSessions:       %d\nAuthorizations: %d\nAudit events:   %d\nTotal:          %d\n",
		output.Tokens,
		output.Sessions,
		output.Authorizations,
		output.AuditEvents,
		output.Total,
	)
	if err != nil || !output.DryRun {
		return err
	}
	_, err = fmt.Fprintln(authOutput, "Dry run:        true")
	return err
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
		if cmd.Bool("json") {
			return writeJSONValue(getAuthorizationOutput(result))
		}
		_, err = fmt.Fprintf(authOutput, "authorization granted for %s\n", result.GetIdentityId())
		return err
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

func writeAuthorizationList(authorizations []*morphpb.AuthAuthorization) error {
	_, err := fmt.Fprint(authOutput, authorizationListToText(authorizations))
	return err
}

func authorizationListToText(authorizations []*morphpb.AuthAuthorization) string {
	if len(authorizations) == 0 {
		return "No RPC identity authorizations found.\n"
	}

	var output strings.Builder
	for index, authorization := range authorizations {
		if index > 0 {
			output.WriteByte('\n')
		}
		fmt.Fprintf(
			&output,
			"[%s] %s\n",
			getAuthDisplayText(authorization.GetStatus()),
			getAuthDisplayText(authorization.GetIdentityId()),
		)
		appendAuthField(&output, "Public key", hex.EncodeToString(authorization.GetPublicKey()))
		appendAuthField(&output, "Owner", authorization.GetOwnerId())
		appendAuthField(&output, "User", authorization.GetUserId())
		appendAuthField(&output, "Roles", strings.Join(authorization.GetRoles(), ", "))
		appendAuthList(&output, "Services", authorization.GetServices())
		appendAuthList(&output, "Methods", authorization.GetMethods())
		appendAuthField(
			&output,
			"Maximum TTL",
			formatAuthSeconds(authorization.GetMaximumTtlSeconds()),
		)
		appendAuthField(
			&output,
			"Generation",
			strconv.FormatUint(authorization.GetGeneration(), 10),
		)
		appendAuthField(&output, "Revision", strconv.FormatUint(authorization.GetRevision(), 10))
		appendAuthField(&output, "Created", formatProtoTime(authorization.GetCreatedAt()))
		appendAuthField(&output, "Updated", formatProtoTime(authorization.GetUpdatedAt()))
		appendAuthField(&output, "Revoked at", formatProtoTime(authorization.GetRevokedAt()))
		appendAuthField(&output, "Reason", authorization.GetRevocationNote())
	}

	return output.String()
}

func writeSessionList(sessions []*morphpb.AuthSession) error {
	_, err := fmt.Fprint(authOutput, sessionListToText(sessions))
	return err
}

func sessionListToText(sessions []*morphpb.AuthSession) string {
	if len(sessions) == 0 {
		return "No RPC authentication sessions found.\n"
	}

	var output strings.Builder
	for index, session := range sessions {
		if index > 0 {
			output.WriteByte('\n')
		}
		fmt.Fprintf(
			&output,
			"[%s] %s\n",
			getAuthDisplayText(session.GetStatus()),
			getAuthDisplayText(session.GetId()),
		)
		appendAuthField(&output, "Identity", session.GetIdentityId())
		appendAuthField(&output, "User", session.GetUserId())
		appendAuthField(&output, "Source", session.GetSource())
		appendAuthField(&output, "Created", formatProtoTime(session.GetCreatedAt()))
		appendAuthField(&output, "Last seen", formatProtoTime(session.GetLastSeenAt()))
		appendAuthField(&output, "Idle expires", formatProtoTime(session.GetIdleExpiresAt()))
		appendAuthField(&output, "Expires", formatProtoTime(session.GetAbsoluteExpiresAt()))
		appendAuthField(&output, "Revoked at", formatProtoTime(session.GetRevokedAt()))
		appendAuthField(&output, "Reason", session.GetRevocationNote())
	}

	return output.String()
}

func writeTokenList(tokens []*morphpb.AuthToken) error {
	_, err := fmt.Fprint(authOutput, tokenListToText(tokens))
	return err
}

func tokenListToText(tokens []*morphpb.AuthToken) string {
	if len(tokens) == 0 {
		return "No RPC access tokens found.\n"
	}

	var output strings.Builder
	for index, token := range tokens {
		if index > 0 {
			output.WriteByte('\n')
		}
		fmt.Fprintf(
			&output,
			"[%s] %s\n",
			getAuthDisplayText(token.GetStatus()),
			getAuthDisplayText(token.GetId()),
		)
		appendAuthField(&output, "Session", token.GetSessionId())
		appendAuthField(&output, "Identity", token.GetIdentityId())
		appendAuthField(&output, "User", token.GetUserId())
		appendAuthField(&output, "Expires", formatProtoTime(token.GetExpiresAt()))
		appendAuthField(&output, "Last used", formatProtoTime(token.GetLastUsedAt()))
		appendAuthField(&output, "Uses", strconv.FormatUint(token.GetUseCount(), 10))
		appendAuthField(&output, "Revoked at", formatProtoTime(token.GetRevokedAt()))
		appendAuthField(&output, "Reason", token.GetRevocationNote())
	}

	return output.String()
}

func auditListToText(events []*morphpb.AuthAuditEvent) string {
	if len(events) == 0 {
		return "No RPC authentication audit events found.\n"
	}

	var output strings.Builder
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
		appendAuthField(&output, "Event ID", event.GetId())
		appendAuthField(&output, "Identity", event.GetIdentityId())
		appendAuthField(&output, "Session", event.GetSessionId())
		appendAuthField(&output, "Token", event.GetTokenId())
		appendAuthField(&output, "Method", event.GetMethod())
		appendAuthField(&output, "Reason", event.GetReason())
	}

	return output.String()
}

func appendAuthField(output *strings.Builder, label, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	fmt.Fprintf(output, "  %-12s %s\n", label+":", getAuthDisplayText(value))
}

func appendAuthList(output *strings.Builder, label string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(output, "  %-12s %s\n", label+":", getAuthDisplayText(values[0]))
	for _, value := range values[1:] {
		fmt.Fprintf(output, "%15s%s\n", "", getAuthDisplayText(value))
	}
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

func formatAuthSeconds(value int64) string {
	if value <= 0 {
		return ""
	}
	return strconv.FormatInt(value, 10) + "s"
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
