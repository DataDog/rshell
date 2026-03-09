package builtins

import "context"

func builtinTrue(_ context.Context, _ *CallContext, _ []string) Result {
	return Result{}
}

func builtinFalse(_ context.Context, _ *CallContext, _ []string) Result {
	return Result{Code: 1}
}
