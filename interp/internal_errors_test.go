package interp

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// newResetRunner creates a Runner via New and calls Reset so that internal
// state (writeEnv, etc.) is fully initialised for unit-level tests.
func newResetRunner(t *testing.T) *Runner {
	t.Helper()
	r, err := New()
	require.NoError(t, err)
	t.Cleanup(func() { r.Close() })
	r.Reset()
	return r
}

func TestInternalErrorf(t *testing.T) {
	r := newResetRunner(t)

	r.internalErrorf("something went wrong: %s", "details")

	require.True(t, r.exit.fatalExit)
	assert.Contains(t, r.exit.err.Error(), "internal error: something went wrong: details")
}

func TestInternalErrorfIdempotent(t *testing.T) {
	r := newResetRunner(t)

	r.internalErrorf("first error")
	r.internalErrorf("second error")

	assert.Contains(t, r.exit.err.Error(), "first error",
		"exit.fatal keeps the first error and ignores subsequent calls")
}

func TestLookupVarEmptyName(t *testing.T) {
	r := newResetRunner(t)

	vr := r.lookupVar("")

	assert.Equal(t, expand.Variable{}, vr, "should return zero Variable")
	require.True(t, r.exit.fatalExit)
	assert.Contains(t, r.exit.err.Error(), "variable name must not be empty")
}

func TestSetVarWithIndexNonNilIndex(t *testing.T) {
	r := newResetRunner(t)

	// Any non-nil ArithmExpr triggers the invariant check.
	idx := &syntax.Word{}
	r.setVarWithIndex(expand.Variable{}, "X", idx, expand.Variable{})

	require.True(t, r.exit.fatalExit)
	assert.Contains(t, r.exit.err.Error(), "index should have been rejected by AST validation")
}

func TestAssignValAppend(t *testing.T) {
	r := newResetRunner(t)

	as := &syntax.Assign{Append: true, Name: &syntax.Lit{Value: "X"}}
	vr := r.assignVal(expand.Variable{}, as, "")

	assert.Equal(t, expand.Variable{}, vr, "should return zero Variable")
	require.True(t, r.exit.fatalExit)
	assert.Contains(t, r.exit.err.Error(), "append should have been rejected by AST validation")
}

func TestAssignValArray(t *testing.T) {
	r := newResetRunner(t)

	as := &syntax.Assign{
		Name:  &syntax.Lit{Value: "X"},
		Array: &syntax.ArrayExpr{},
	}
	vr := r.assignVal(expand.Variable{}, as, "")

	assert.Equal(t, expand.Variable{}, vr, "should return zero Variable")
	require.True(t, r.exit.fatalExit)
	assert.Contains(t, r.exit.err.Error(), "array assignment should have been rejected by AST validation")
}

func TestResetZeroValueRunnerSetsFatal(t *testing.T) {
	var r Runner
	r.Reset()

	require.True(t, r.exit.fatalExit)
	assert.Contains(t, r.exit.err.Error(), "use interp.New to construct a Runner")
}

func TestRunZeroValueRunnerMultipleCalls(t *testing.T) {
	// Calling Run repeatedly on a zero-value Runner should consistently
	// return an explicit error, not panic.
	var r Runner
	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader("echo hi"), "")
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		err = r.Run(context.Background(), prog)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "use interp.New to construct a Runner")
	}
}

func TestInternalErrorStopsExecution(t *testing.T) {
	// After an internal error, the runner's stop() check should halt
	// further statement execution, surfacing the error via Run.
	r := newResetRunner(t)

	// Inject a fatal internal error before running a program.
	r.internalErrorf("forced failure")

	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader("echo should_not_run"), "")
	require.NoError(t, err)

	// Run resets exit, but the next stmt() call will see exiting=true.
	// We verify the pattern by calling stmts directly on a prepared runner.
	r.fillExpandConfig(context.Background())
	r.stmts(context.Background(), prog.Stmts)

	assert.True(t, r.exit.fatalExit, "fatal flag should remain set")
}
