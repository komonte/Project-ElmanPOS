package web

import "testing"

func TestFormatTime(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"empty", "", "-"},
		{"UTC with microseconds", "2026-11-25T18:31:38.586087Z", "2026-11-25 15:31:38"},
		{"with an offset", "2026-11-25T15:31:38-03:00", "2026-11-25 15:31:38"},
		{"unexpected format is shown as is", "2026-11-25 18:31", "2026-11-25 18:31"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatTime(tt.in); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
