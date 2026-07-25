package auth

import (
	"sort"

	morphpb "github.com/wandxy/morph/internal/rpc/proto"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func RPCMethodCatalog() []string {
	methods := descriptorMethods(morphpb.File_internal_rpc_proto_morph_proto.Services())
	methods = append(methods, descriptorMethods(healthpb.File_grpc_health_v1_health_proto.Services())...)
	sort.Strings(methods)

	return methods
}

func RPCServiceCatalog() []string {
	seen := make(map[string]struct{})
	for _, method := range RPCMethodCatalog() {
		service := method[:lastSlash(method)]
		seen[service] = struct{}{}
	}
	services := make([]string, 0, len(seen))
	for service := range seen {
		services = append(services, service)
	}
	sort.Strings(services)

	return services
}

func IsKnownRPCMethod(method string) bool {
	normalized, err := NormalizeMethod(method)
	if err != nil {
		return false
	}
	for _, candidate := range RPCMethodCatalog() {
		if candidate == normalized {
			return true
		}
	}

	return false
}

func IsKnownRPCService(service string) bool {
	normalized, err := NormalizeService(service)
	if err != nil {
		return false
	}
	if normalized == RootScope {
		return true
	}
	for _, candidate := range RPCServiceCatalog() {
		if candidate == normalized {
			return true
		}
	}

	return false
}

func descriptorMethods(services protoreflect.ServiceDescriptors) []string {
	var methods []string
	for serviceIndex := range services.Len() {
		service := services.Get(serviceIndex)
		for methodIndex := range service.Methods().Len() {
			method := service.Methods().Get(methodIndex)
			methods = append(methods, "/"+string(service.FullName())+"/"+string(method.Name()))
		}
	}

	return methods
}

func lastSlash(value string) int {
	for index := len(value) - 1; index >= 0; index-- {
		if value[index] == '/' {
			return index
		}
	}

	return 0
}
