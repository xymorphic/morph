package client

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const (
	authorizationMetadataKey = "authorization"
	openSessionMethod        = "/morph.v1.AuthService/OpenSession"
	closeSessionMethod       = "/morph.v1.AuthService/CloseSession"
)

func authUnaryClientInterceptor(resolver *authResolver) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		request any,
		reply any,
		connection *grpc.ClientConn,
		invoke grpc.UnaryInvoker,
		callOptions ...grpc.CallOption,
	) error {
		var token string
		var err error
		if method == openSessionMethod {
			token, err = resolver.getToken()
		} else {
			token, err = resolver.prepareAuthenticatedRequest(ctx, method)
		}
		if err != nil {
			return err
		}
		authenticated := withBearerToken(ctx, token)

		err = invoke(authenticated, method, request, reply, connection, callOptions...)
		if err == nil {
			resolver.recordUse()
		}
		return err
	}
}

func authStreamClientInterceptor(resolver *authResolver) grpc.StreamClientInterceptor {
	return func(
		ctx context.Context,
		description *grpc.StreamDesc,
		connection *grpc.ClientConn,
		method string,
		stream grpc.Streamer,
		callOptions ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		token, err := resolver.prepareAuthenticatedRequest(ctx, method)
		if err != nil {
			return nil, err
		}
		authenticated := withBearerToken(ctx, token)

		clientStream, err := stream(
			authenticated, description, connection, method, callOptions...,
		)
		if err == nil {
			resolver.recordUse()
		}
		return clientStream, err
	}
}

func withBearerToken(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, authorizationMetadataKey, "Bearer "+token)
}
