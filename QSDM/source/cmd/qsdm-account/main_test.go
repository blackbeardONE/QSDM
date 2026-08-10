package main

import "testing"

func TestCheckConfigOnly(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		checkOnly bool
		wantError bool
	}{
		{name: "serve by default"},
		{name: "check configuration", args: []string{"--check-config"}, checkOnly: true},
		{name: "reject unknown argument", args: []string{"--unknown"}, wantError: true},
		{name: "reject extra argument", args: []string{"--check-config", "extra"}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := checkConfigOnly(test.args)
			if (err != nil) != test.wantError {
				t.Fatalf("error=%v wantError=%t", err, test.wantError)
			}
			if got != test.checkOnly {
				t.Fatalf("checkOnly=%t want=%t", got, test.checkOnly)
			}
		})
	}
}
