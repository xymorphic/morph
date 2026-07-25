package auth

import (
	"errors"
	"strings"
)

const RootScope = "*"

func NormalizeMethod(method string) (string, error) {
	method = strings.TrimSpace(method)
	if method == "" || !strings.HasPrefix(method, "/") {
		return "", errors.New("canonical RPC method is required")
	}
	parts := strings.Split(strings.TrimPrefix(method, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" ||
		strings.ContainsAny(parts[0], "* \t\r\n") || strings.ContainsAny(parts[1], "* \t\r\n") {
		return "", errors.New("canonical RPC method must have the form /package.Service/Method")
	}

	return "/" + parts[0] + "/" + parts[1], nil
}

func NormalizeService(service string) (string, error) {
	service = strings.TrimSpace(service)
	if service == "" {
		return "", errors.New("canonical RPC service is required")
	}
	if service == RootScope {
		return RootScope, nil
	}
	service = strings.TrimSuffix(service, "/*")
	if !strings.HasPrefix(service, "/") || strings.ContainsAny(service, "* \t\r\n") ||
		strings.Count(strings.TrimPrefix(service, "/"), "/") != 0 {
		return "", errors.New("canonical RPC service must have the form /package.Service")
	}

	return service, nil
}

func AllowsMethod(services, methods []string, method string, allowRoot bool) bool {
	method, err := NormalizeMethod(method)
	if err != nil {
		return false
	}
	for _, candidate := range methods {
		normalized, normalizeErr := NormalizeMethod(candidate)
		if normalizeErr == nil && normalized == method {
			return true
		}
	}
	service := method[:strings.LastIndex(method, "/")]
	for _, candidate := range services {
		normalized, normalizeErr := NormalizeService(candidate)
		if normalizeErr != nil {
			continue
		}
		if normalized == service || normalized == RootScope && allowRoot {
			return true
		}
	}

	return false
}

func IsSubset(requested, allowed []string) bool {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, value := range allowed {
		allowedSet[value] = struct{}{}
	}
	for _, value := range requested {
		if _, ok := allowedSet[value]; !ok {
			return false
		}
	}

	return true
}

func ScopesAreSubset(
	requestedServices, requestedMethods, allowedServices, allowedMethods []string,
	allowRoot bool,
) bool {
	for _, service := range requestedServices {
		normalized, err := NormalizeService(service)
		if err != nil || !allowsService(allowedServices, normalized, allowRoot) {
			return false
		}
	}
	for _, method := range requestedMethods {
		if !AllowsMethod(allowedServices, allowedMethods, method, allowRoot) {
			return false
		}
	}

	return true
}

func allowsService(allowed []string, service string, allowRoot bool) bool {
	for _, candidate := range allowed {
		normalized, err := NormalizeService(candidate)
		if err == nil && (normalized == service || normalized == RootScope && allowRoot) {
			return true
		}
	}

	return false
}
