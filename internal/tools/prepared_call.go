package tools

import "context"

type preparedCallContextKey struct{}

type preparedCall struct {
	tool  string
	input string
	value any
}

func withPreparedCall(ctx context.Context, tool string, input string, value any) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, preparedCallContextKey{}, preparedCall{
		tool: tool, input: input, value: value,
	})
}

func GetPreparedCall(ctx context.Context, call Call) (any, bool) {
	if ctx == nil {
		return nil, false
	}
	prepared, ok := ctx.Value(preparedCallContextKey{}).(preparedCall)
	if !ok || prepared.tool != call.Name || prepared.input != call.Input {
		return nil, false
	}
	return prepared.value, true
}
