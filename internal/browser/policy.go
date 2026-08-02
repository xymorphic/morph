package browser

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"strings"

	"github.com/xymorphic/morph/internal/config"
	"github.com/xymorphic/morph/internal/permissions"
	"github.com/xymorphic/morph/pkg/netpolicy"
	"golang.org/x/net/idna"
)

type ResolveHost func(context.Context, string) ([]netip.Addr, error)

type NetworkPolicy struct {
	Strict       bool
	AllowedHosts []string
	AllowedCIDRs []netip.Prefix
	ResolveHost  ResolveHost
}

func NewNetworkPolicy(cfg config.BrowserNetworkConfig) (NetworkPolicy, error) {
	policy := NetworkPolicy{Strict: cfg.StrictEnabled()}
	for _, raw := range cfg.DevelopmentAllowedHosts {
		host, err := normalizePolicyHost(raw)
		if err != nil {
			return NetworkPolicy{}, err
		}
		if !slices.Contains(policy.AllowedHosts, host) {
			policy.AllowedHosts = append(policy.AllowedHosts, host)
		}
	}
	for _, raw := range cfg.DevelopmentAllowedCIDRs {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return NetworkPolicy{}, errors.New("browser development allowed CIDR is invalid")
		}
		policy.AllowedCIDRs = append(policy.AllowedCIDRs, prefix.Masked())
	}

	return policy, nil
}

func (p NetworkPolicy) Resolve(ctx context.Context, target permissions.NetworkTarget) ([]netip.Addr, error) {
	target, err := target.Normalize()
	if err != nil {
		return nil, err
	}
	if addr, parseErr := netip.ParseAddr(target.Host); parseErr == nil {
		if p.isAllowedAddress(target.Host, addr) {
			return []netip.Addr{addr.Unmap()}, nil
		}
		return nil, errors.New("browser target resolves to a blocked address")
	}

	resolve := p.ResolveHost
	if resolve == nil {
		resolve = func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		}
	}
	addresses, err := resolve(ctx, target.Host)
	if err != nil {
		return nil, err
	}
	if len(addresses) == 0 {
		return nil, errors.New("browser target resolved to no addresses")
	}
	result := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if !p.isAllowedAddress(target.Host, address) {
			return nil, errors.New("browser target resolves to a blocked address")
		}
		if !slices.Contains(result, address) {
			result = append(result, address)
		}
	}

	return result, nil
}

func (p NetworkPolicy) isAllowedAddress(host string, address netip.Addr) bool {
	if !p.Strict {
		return address.IsValid()
	}
	if slices.Contains(p.AllowedHosts, host) {
		return true
	}
	for _, prefix := range p.AllowedCIDRs {
		if prefix.Contains(address) {
			return true
		}
	}

	return netpolicy.SafeAddr(address, nil)
}

func normalizePolicyHost(raw string) (string, error) {
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
	if address, err := netip.ParseAddr(host); err == nil {
		return address.Unmap().String(), nil
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil || ascii == "" || strings.ContainsAny(ascii, "/:@") {
		return "", errors.New("browser development allowed host is invalid")
	}

	return ascii, nil
}
