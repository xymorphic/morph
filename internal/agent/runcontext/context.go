package runcontext

import (
	"context"
	"errors"
	"time"

	statecore "github.com/xymorphic/morph/internal/state/core"
	"github.com/xymorphic/morph/pkg/str"
)

const (
	// DefaultSessionID is the canonical fallback session for root runs.
	DefaultSessionID = statecore.DefaultSessionID
	// SessionIDPrefix is the required prefix for generated run session IDs.
	SessionIDPrefix = statecore.SessionIDPrefix
	// StateModeShared lets a child run read and write the same state namespace as its parent.
	StateModeShared = "shared"
	// StateModeIsolated gives a child run a separate state namespace.
	StateModeIsolated = "isolated"
	// StateModeReadonly lets a child run read parent state without writing back to it.
	StateModeReadonly = "readonly"
)

type contextKey struct{}

// Context describes the profile, session, personality, state mode, and lineage
// for one agent-managed run.
//
// A run can have two session identifiers:
//   - Session.PublicID is the user-facing conversation/session.
//   - Session.EffectiveID is the namespace used for mutable run state.
//
// Parent runs usually use the same value for both IDs. Child runs keep the
// public parent session for user-visible provenance while using their own
// effective session when state isolation is requested.
type Context struct {
	ProfileName string
	Session     Session
	Personality Personality
	State       State
	Lineage     Lineage
}

// Session separates user-visible session identity from the state namespace used
// while executing a run.
type Session struct {
	PublicID    string
	EffectiveID string
}

// Personality identifies the active personality overlay for a run.
type Personality struct {
	Name string
}

// State describes how a run should read or write persisted state.
type State struct {
	Mode string
}

// Lineage links child runs back to their parent session and spawning run.
type Lineage struct {
	ParentSessionID string
	ChildSessionID  string
	RunID           string
	SpawnedAt       time.Time
	CompletedAt     time.Time
}

// ChildOptions controls how a child run is derived from a parent context.
type ChildOptions struct {
	ChildSessionID  string
	RunID           string
	PersonalityName string
	StateMode       string
	ProfileName     string
	SpawnedAt       time.Time
	CompletedAt     time.Time
}

// PersonalityOptions controls how a named personality is applied to an existing
// run context without changing the session lineage.
type PersonalityOptions struct {
	PersonalityName string
	StateMode       string
	ProfileName     string
}

// NewParent returns a root run context for a user-facing session ID.
func NewParent(sessionID string) (Context, error) {
	sessionIDValue := str.String(sessionID)
	sessionIDValue2 := str.String(sessionID)
	runCtx := Context{
		Session: Session{
			PublicID:    sessionIDValue.Trim(),
			EffectiveID: sessionIDValue2.Trim(),
		},
	}

	return runCtx.Normalize()
}

// NewChild returns a child run context derived from runCtx.
func (runCtx Context) NewChild(opts ChildOptions) (Context, error) {
	parent, err := runCtx.Normalize()
	if err != nil {
		return Context{}, err
	}
	childSessionIDValue := str.String(opts.ChildSessionID)
	if childSessionIDValue.Trim() == "" {
		return Context{}, errors.New("child session id is required")
	}
	profileNameValue := str.String(opts.ProfileName)
	childSessionIDValue2 := str.String(opts.ChildSessionID)
	personalityNameValue := str.String(opts.PersonalityName)
	stateModeValue := str.String(opts.StateMode)
	childSessionIDValue3 := str.String(opts.ChildSessionID)
	runIDValue := str.String(opts.RunID)
	childCtx := Context{
		ProfileName: profileNameValue.Trim(),
		Session: Session{
			PublicID:    parent.Session.PublicID,
			EffectiveID: childSessionIDValue2.Trim(),
		},
		Personality: Personality{Name: personalityNameValue.Trim()},
		State:       State{Mode: stateModeValue.Trim()},
		Lineage: Lineage{
			ParentSessionID: parent.Session.PublicID,
			ChildSessionID:  childSessionIDValue3.Trim(),
			RunID:           runIDValue.Trim(),
			SpawnedAt:       opts.SpawnedAt,
			CompletedAt:     opts.CompletedAt,
		},
	}
	if childCtx.ProfileName == "" {
		childCtx.ProfileName = parent.ProfileName
	}
	if childCtx.Personality.Name == "" {
		childCtx.Personality.Name = parent.Personality.Name
	}
	if childCtx.State.Mode == "" {
		childCtx.State.Mode = StateModeShared
	}

	return childCtx.Normalize()
}

// NewPersonality returns runCtx with a named personality overlay applied.
func (runCtx Context) NewPersonality(opts PersonalityOptions) (Context, error) {
	parent, err := runCtx.Normalize()
	if err != nil {
		return Context{}, err
	}
	personalityNameValue2 := str.String(opts.PersonalityName)
	if personalityNameValue2.Trim() == "" {
		return Context{}, errors.New("personality name is required")
	}

	personalityCtx := parent
	personalityNameValue3 := str.String(opts.PersonalityName)
	personalityCtx.Personality.Name = personalityNameValue3.Trim()
	stateModeValue2 := str.String(opts.StateMode)
	personalityCtx.State.Mode = stateModeValue2.Trim()
	profileNameValue2 := str.String(opts.ProfileName)
	personalityCtx.ProfileName = profileNameValue2.Trim()
	if personalityCtx.ProfileName == "" {
		personalityCtx.ProfileName = parent.ProfileName
	}

	return personalityCtx.Normalize()
}

// Normalize trims values, fills default session/state fields, and validates all
// session IDs carried by the run context.
func (runCtx Context) Normalize() (Context, error) {
	profileNameValue3 := str.String(runCtx.ProfileName)
	runCtx.ProfileName = profileNameValue3.Trim()
	publicIDValue := str.String(runCtx.Session.PublicID)
	runCtx.Session.PublicID = publicIDValue.Trim()
	if runCtx.Session.PublicID == "" {
		runCtx.Session.PublicID = DefaultSessionID
	}
	if err := ValidateSessionID(runCtx.Session.PublicID); err != nil {
		return Context{}, err
	}
	effectiveIDValue := str.String(runCtx.Session.EffectiveID)
	runCtx.Session.EffectiveID = effectiveIDValue.Trim()
	if runCtx.Session.EffectiveID == "" {
		runCtx.Session.EffectiveID = runCtx.Session.PublicID
	}
	if err := ValidateSessionID(runCtx.Session.EffectiveID); err != nil {
		return Context{}, err
	}
	parentSessionIDValue := str.String(runCtx.Lineage.ParentSessionID)
	runCtx.Lineage.ParentSessionID = parentSessionIDValue.Trim()
	if runCtx.Lineage.ParentSessionID != "" {
		if err := ValidateSessionID(runCtx.Lineage.ParentSessionID); err != nil {
			return Context{}, err
		}
	}
	childSessionIDValue4 := str.String(runCtx.Lineage.ChildSessionID)
	runCtx.Lineage.ChildSessionID = childSessionIDValue4.Trim()
	if runCtx.Lineage.ChildSessionID != "" {
		if err := ValidateSessionID(runCtx.Lineage.ChildSessionID); err != nil {
			return Context{}, err
		}
	}
	runIDValue2 := str.String(runCtx.Lineage.RunID)
	runCtx.Lineage.RunID = runIDValue2.Trim()
	nameValue := str.String(runCtx.Personality.Name)
	runCtx.Personality.Name = nameValue.Trim()
	runCtx.State.Mode = normalizeStateMode(runCtx.State.Mode)

	return runCtx, nil
}

// StateSessionID returns the effective session ID used for state isolation.
func (runCtx Context) StateSessionID() string {
	effectiveIDValue2 := str.String(runCtx.Session.EffectiveID)
	if value := effectiveIDValue2.Trim(); value != "" {
		return value
	}
	publicIDValue2 := str.String(runCtx.Session.PublicID)
	if value := publicIDValue2.Trim(); value != "" {
		return value
	}

	return DefaultSessionID
}

// WithContext stores a normalized run context in ctx.
func WithContext(ctx context.Context, runCtx Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, err := runCtx.Normalize()
	if err != nil {
		return ctx
	}

	return context.WithValue(ctx, contextKey{}, runCtx)
}

// FromContext loads a normalized run context from ctx.
func FromContext(ctx context.Context) (Context, bool) {
	if ctx == nil {
		return Context{}, false
	}

	runCtx, ok := ctx.Value(contextKey{}).(Context)
	if !ok {
		return Context{}, false
	}

	runCtx, err := runCtx.Normalize()
	if err != nil {
		return Context{}, false
	}

	return runCtx, true
}

func normalizeStateMode(value string) string {
	valueText := str.String(value)
	switch valueText.Normalized() {
	case StateModeIsolated:
		return StateModeIsolated
	case StateModeReadonly:
		return StateModeReadonly
	default:
		return StateModeShared
	}
}

// ValidateSessionID validates agent run session IDs with the same rules as
// durable state sessions.
func ValidateSessionID(id string) error {
	return statecore.ValidateSessionID(id)
}
