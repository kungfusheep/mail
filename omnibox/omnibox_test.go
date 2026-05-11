package omnibox

import "testing"

func TestMaxVisibleRows(t *testing.T) {
	tests := []struct {
		name   string
		height int
		want   int
	}{
		{name: "small screens still show one command", height: 8, want: 1},
		{name: "medium screens use sixty percent budget", height: 40, want: 3},
		{name: "tall screens grow the command budget", height: 80, want: 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maxVisibleRows(tt.height)
			if got != tt.want {
				t.Fatalf("maxVisibleRows(%d) = %d, want %d", tt.height, got, tt.want)
			}
		})
	}
}
