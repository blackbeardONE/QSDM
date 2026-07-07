package logging

import "testing"

func TestLogStdoutEnabled(t *testing.T) {
	for _, test := range []struct {
		value string
		want  bool
	}{
		{value: "", want: true},
		{value: "1", want: true},
		{value: "false", want: false},
		{value: "OFF", want: false},
		{value: "0", want: false},
	} {
		t.Run(test.value, func(t *testing.T) {
			t.Setenv("QSDM_LOG_STDOUT", test.value)
			if got := logStdoutEnabled(); got != test.want {
				t.Fatalf("logStdoutEnabled() = %v, want %v", got, test.want)
			}
		})
	}
}
