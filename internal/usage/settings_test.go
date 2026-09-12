package usage

import (
	"testing"

	"github.com/strahe/profiledeck/internal/apperror"
)

func TestNormalizeUsageSyncInterval(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		input int
		want  int
	}{
		{name: "retired five seconds", input: 5, want: 15},
		{name: "fifteen seconds", input: 15, want: 15},
		{name: "thirty seconds", input: 30, want: 30},
		{name: "one minute", input: 60, want: 60},
		{name: "two minutes", input: 120, want: 120},
		{name: "five minutes", input: 300, want: 300},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeUsageSyncInterval(test.input)
			if err != nil || got != test.want {
				t.Fatalf("NormalizeUsageSyncInterval(%d) = %d, %v, want %d", test.input, got, err, test.want)
			}
		})
	}

	if _, err := NormalizeUsageSyncInterval(10); err == nil {
		t.Fatal("unsupported interval was accepted")
	} else if err.Code != apperror.SettingInvalid {
		t.Fatalf("unsupported interval error = %v", err)
	}
}
