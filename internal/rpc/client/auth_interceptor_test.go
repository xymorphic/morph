package client

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestAuthUnaryClientInterceptor_AttachesTokenForBootstrapAndActiveCalls(t *testing.T) {
	resolver := newAuthResolver(Options{AuthToken: "explicit-token"})
	interceptor := authUnaryClientInterceptor(resolver)
	invoke := func(
		ctx context.Context,
		_ string,
		_, _ any,
		_ *grpc.ClientConn,
		_ ...grpc.CallOption,
	) error {
		values, ok := metadata.FromOutgoingContext(ctx)
		require.True(t, ok)
		require.Equal(t, []string{"Bearer explicit-token"}, values.Get(authorizationMetadataKey))
		return nil
	}

	require.NoError(t, interceptor(
		context.Background(),
		openSessionMethod,
		nil,
		nil,
		nil,
		invoke,
	))
	resolver.active.Store(true)
	require.NoError(t, interceptor(
		context.Background(),
		"/morph.v1.SessionService/List",
		nil,
		nil,
		nil,
		invoke,
	))
	require.False(t, resolver.lastUse.IsZero())
}

func TestAuthUnaryClientInterceptor_RejectsCallsAfterCloseStarts(t *testing.T) {
	resolver := newAuthResolver(Options{AuthToken: "explicit-token"})
	resolver.closing.Store(true)
	called := false

	err := authUnaryClientInterceptor(resolver)(
		context.Background(),
		"/morph.v1.SessionService/List",
		nil,
		nil,
		nil,
		func(
			context.Context,
			string,
			any,
			any,
			*grpc.ClientConn,
			...grpc.CallOption,
		) error {
			called = true
			return nil
		},
	)
	require.ErrorContains(t, err, "resolver is closing")
	require.False(t, called)
}

func TestAuthStreamClientInterceptor_AttachesToken(t *testing.T) {
	resolver := newAuthResolver(Options{AuthToken: "explicit-token"})
	resolver.active.Store(true)

	stream, err := authStreamClientInterceptor(resolver)(
		context.Background(),
		&grpc.StreamDesc{},
		nil,
		"/morph.v1.SessionService/Observe",
		func(
			ctx context.Context,
			_ *grpc.StreamDesc,
			_ *grpc.ClientConn,
			_ string,
			_ ...grpc.CallOption,
		) (grpc.ClientStream, error) {
			values, ok := metadata.FromOutgoingContext(ctx)
			require.True(t, ok)
			require.Equal(t, []string{"Bearer explicit-token"}, values.Get(authorizationMetadataKey))
			return nil, nil
		},
	)
	require.NoError(t, err)
	require.Nil(t, stream)
	require.False(t, resolver.lastUse.IsZero())
}
