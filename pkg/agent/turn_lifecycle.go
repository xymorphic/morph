package agent

import (
	"context"
	"errors"

	"github.com/xymorphic/morph/pkg/str"
)

type TurnCloser interface {
	Close()
}

type TurnLifecycle struct {
	Load                  func(context.Context, RespondOptions) error
	SetRequestInstruction func(string)
	Open                  func(context.Context, RespondOptions) (TurnCloser, error)
	Prepare               func(context.Context) error
	CheckInput            func(context.Context, string) (InputCheck, error)
	AcceptUserMessage     func(context.Context, string) error
	LoadMemory            func(context.Context, string) error
	ConsumeIteration      func() bool
	RunStep               func(context.Context) (LoopDecision, error)
	OnExhausted           func(context.Context) (string, error)
}

type InputCheck struct {
	Reply   string
	Blocked bool
}

func RunTurnLifecycle(
	ctx context.Context,
	message string,
	opts RespondOptions,
	lifecycle TurnLifecycle,
) (string, error) {
	if lifecycle.Load == nil {
		return "", errors.New("turn loader is required")
	}
	if lifecycle.RunStep == nil {
		return "", errors.New("turn step is required")
	}

	if err := lifecycle.Load(ctx, opts); err != nil {
		return "", err
	}

	if lifecycle.SetRequestInstruction != nil {
		instructValue := str.String(opts.Instruct)
		lifecycle.SetRequestInstruction(instructValue.Trim())
	}

	closer, err := openTurnLifecycle(ctx, opts, lifecycle)
	if err != nil {
		return "", err
	}
	if closer != nil {
		defer closer.Close()
	}

	if lifecycle.Prepare != nil {
		if err := lifecycle.Prepare(ctx); err != nil {
			return "", err
		}
	}

	if lifecycle.CheckInput != nil {
		check, err := lifecycle.CheckInput(ctx, message)
		if err != nil {
			return "", err
		}
		if check.Blocked {
			return check.Reply, nil
		}
	}

	if lifecycle.AcceptUserMessage != nil {
		if err := lifecycle.AcceptUserMessage(ctx, message); err != nil {
			return "", err
		}
	}

	if lifecycle.LoadMemory != nil {
		if err := lifecycle.LoadMemory(ctx, message); err != nil {
			return "", err
		}
	}

	return RunLoop(ctx, LoopOptions{
		Consume:     lifecycle.ConsumeIteration,
		RunStep:     lifecycle.RunStep,
		OnExhausted: lifecycle.OnExhausted,
	})
}

func openTurnLifecycle(
	ctx context.Context,
	opts RespondOptions,
	lifecycle TurnLifecycle,
) (TurnCloser, error) {
	if lifecycle.Open == nil {
		return nil, nil
	}

	return lifecycle.Open(ctx, opts)
}
