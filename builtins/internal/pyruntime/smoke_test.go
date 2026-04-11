// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package pyruntime

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"testing"
)

func noFileOpen(ctx context.Context, path string, flags int, mode os.FileMode) (io.ReadWriteCloser, error) {
	return nil, fmt.Errorf("no file access")
}

func TestSmokeEval(t *testing.T) {
	tests := []struct {
		name   string
		code   string
		expect string
	}{
		{"hello", `print("hello world")`, "hello world\n"},
		{"arithmetic", `print(2 + 3 * 4)`, "14\n"},
		{"list comp", `print([x*2 for x in range(5)])`, "[0, 2, 4, 6, 8]\n"},
		{"fib", `
def fib(n):
    if n <= 1:
        return n
    return fib(n-1) + fib(n-2)
print(fib(10))`, "55\n"},
		{"class", `
class Dog:
    def __init__(self, name):
        self.name = name
    def bark(self):
        return "Woof! " + self.name

d = Dog("Rex")
print(d.bark())`, "Woof! Rex\n"},
		{"generator", `
def gen():
    for i in range(3):
        yield i * i
print(list(gen()))`, "[0, 1, 4]\n"},
		{"exception", `
try:
    raise ValueError("oops")
except ValueError as e:
    print("caught:", e)`, "caught: oops\n"},
		{"closure", `
def make_adder(n):
    def adder(x):
        return x + n
    return adder
add5 = make_adder(5)
print(add5(3))`, "8\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			var ebuf bytes.Buffer
			code := Run(context.Background(), RunOpts{
				Source:     tt.code,
				SourceName: "<test>",
				Stdout:     &buf,
				Stderr:     &ebuf,
				Open:       noFileOpen,
			})
			got := buf.String()
			if code != 0 || got != tt.expect {
				t.Errorf("code=%d, got=%q, want=%q, stderr=%q", code, got, tt.expect, ebuf.String())
			}
		})
	}
}
