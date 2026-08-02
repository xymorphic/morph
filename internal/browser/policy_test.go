package browser

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/permissions"
)

func TestNetworkPolicy_RejectsUnsafeAndMixedDNSAnswers(t *testing.T) {
	policy := NetworkPolicy{
		Strict: true,
		ResolveHost: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{
				netip.MustParseAddr("93.184.216.34"),
				netip.MustParseAddr("127.0.0.1"),
			}, nil
		},
	}
	target, err := permissions.NetworkTargetFromURL(
		"https://example.com", "GET", permissions.NetworkRequestNavigation,
	)
	require.NoError(t, err)

	_, err = policy.Resolve(context.Background(), target)
	require.EqualError(t, err, "browser target resolves to a blocked address")
}

func TestNetworkPolicy_AllowsExplicitDevelopmentHostAndCIDR(t *testing.T) {
	strict := true
	policy, err := NewNetworkPolicy(config.BrowserNetworkConfig{
		Strict: &strict, DevelopmentAllowedHosts: []string{"LOCALHOST"},
		DevelopmentAllowedCIDRs: []string{"10.0.0.0/8"},
	})
	require.NoError(t, err)
	policy.ResolveHost = func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "localhost" {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("10.1.2.3")}, nil
	}

	localhost, err := permissions.NetworkTargetFromURL(
		"http://localhost", "GET", permissions.NetworkRequestNavigation,
	)
	require.NoError(t, err)
	addresses, err := policy.Resolve(context.Background(), localhost)
	require.NoError(t, err)
	require.Equal(t, []netip.Addr{netip.MustParseAddr("127.0.0.1")}, addresses)

	private, err := permissions.NetworkTargetFromURL(
		"http://internal.test", "GET", permissions.NetworkRequestNavigation,
	)
	require.NoError(t, err)
	addresses, err = policy.Resolve(context.Background(), private)
	require.NoError(t, err)
	require.Equal(t, []netip.Addr{netip.MustParseAddr("10.1.2.3")}, addresses)
}

func TestNewNetworkPolicy_RejectsInvalidDevelopmentExceptions(t *testing.T) {
	_, err := NewNetworkPolicy(config.BrowserNetworkConfig{DevelopmentAllowedHosts: []string{"bad/host"}})
	require.EqualError(t, err, "browser development allowed host is invalid")
	_, err = NewNetworkPolicy(config.BrowserNetworkConfig{DevelopmentAllowedCIDRs: []string{"bad"}})
	require.EqualError(t, err, "browser development allowed CIDR is invalid")
}

func TestNetworkPolicy_HandlesPublicLiteralResolutionFailuresAndEmptyAnswers(t *testing.T) {
	public, err := permissions.NetworkTargetFromURL(
		"https://93.184.216.34", "GET", permissions.NetworkRequestNavigation,
	)
	require.NoError(t, err)
	addresses, err := (NetworkPolicy{Strict: true}).Resolve(context.Background(), public)
	require.NoError(t, err)
	require.Equal(t, []netip.Addr{netip.MustParseAddr("93.184.216.34")}, addresses)

	private, err := permissions.NetworkTargetFromURL(
		"http://127.0.0.1", "GET", permissions.NetworkRequestNavigation,
	)
	require.NoError(t, err)
	_, err = (NetworkPolicy{Strict: true}).Resolve(context.Background(), private)
	require.EqualError(t, err, "browser target resolves to a blocked address")

	target, err := permissions.NetworkTargetFromURL(
		"https://example.com", "GET", permissions.NetworkRequestNavigation,
	)
	require.NoError(t, err)
	expected := errors.New("resolver failed")
	_, err = (NetworkPolicy{Strict: true, ResolveHost: func(context.Context, string) ([]netip.Addr, error) {
		return nil, expected
	}}).Resolve(context.Background(), target)
	require.ErrorIs(t, err, expected)
	_, err = (NetworkPolicy{Strict: true, ResolveHost: func(context.Context, string) ([]netip.Addr, error) {
		return nil, nil
	}}).Resolve(context.Background(), target)
	require.EqualError(t, err, "browser target resolved to no addresses")
}
