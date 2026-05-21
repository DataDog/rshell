package truecmd

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func TestVulnHuntBuiltinTruePureStatusIgnoresAllArguments(t *testing.T) {
	cases := [][]string{
		nil,
		{},
		{"--help"},
		{"-h"},
		{"--unknown"},
		{"--"},
		{"--", "--help"},
		{"name\nFORGED_TRUE_ROW=1"},
		{"$(echo should-not-run)", ";", "echo", "pwned"},
		{strings.Repeat("A", 1<<20)},
	}

	for _, args := range cases {
		result := run(context.Background(), nil, args)
		if result.Code != 0 {
			t.Fatalf("true(%q) Code = %d, want 0", args, result.Code)
		}
		if result.Exiting || result.BreakN != 0 || result.ContinueN != 0 {
			t.Fatalf("true(%q) produced control-flow result: %+v", args, result)
		}
	}
}

func TestVulnHuntBuiltinTrueCanceledContextStillHasNoIOSurface(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := run(ctx, nil, []string{"--help", "ignored"})
	if result.Code != 0 {
		t.Fatalf("true with canceled context Code = %d, want 0", result.Code)
	}
	if result.Exiting || result.BreakN != 0 || result.ContinueN != 0 {
		t.Fatalf("true with canceled context produced control-flow result: %+v", result)
	}
}

func TestVulnHuntBuiltinTrueConcurrentRunsDoNotShareState(t *testing.T) {
	const workers = 64

	var wg sync.WaitGroup
	errs := make(chan string, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := run(context.Background(), nil, []string{"--help", "ignored"})
			if result.Code != 0 || result.Exiting || result.BreakN != 0 || result.ContinueN != 0 {
				errs <- "unexpected result"
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatal(err)
	}
}
