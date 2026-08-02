package execution

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessCodec_ClassifiesIdentityFailures(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	owner := Owner{
		Profile:            "default",
		ActorKind:          "local_owner",
		Surface:            "cli",
		PublicSessionID:    "default",
		EffectiveSessionID: "default",
	}
	codec, err := NewProcessCodec(key, "generation", "daemon")
	require.NoError(t, err)
	handle, err := codec.Encode(owner, "container", "token")
	require.NoError(t, err)
	identity, err := codec.Decode(handle, owner, "container")
	require.NoError(t, err)
	require.Equal(t, "token", identity.Token)

	other := owner
	other.ActorID = "different"
	_, err = codec.DecodeCurrent(handle, other)
	require.ErrorIs(t, err, ErrProcessDenied)
	stale, err := NewProcessCodec(key, "next", "daemon")
	require.NoError(t, err)
	_, err = stale.DecodeCurrent(handle, owner)
	require.ErrorIs(t, err, ErrProcessStale)
	_, err = codec.Verify(handle + "broken")
	require.ErrorIs(t, err, ErrInvalidProcessID)
}

func TestOwner_FingerprintExcludesRunID(t *testing.T) {
	owner := Owner{
		Profile:            "default",
		ActorKind:          "local_owner",
		Surface:            "tui",
		PublicSessionID:    "default",
		EffectiveSessionID: "default",
		RunID:              "run-one",
	}
	nextTurn := owner
	nextTurn.RunID = "run-two"
	require.Equal(t, owner.Fingerprint(), nextTurn.Fingerprint())
}
