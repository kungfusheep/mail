package provider

import "testing"

func TestAddressString(t *testing.T) {
	tests := []struct {
		addr Address
		want string
	}{
		{Address{Email: "test@example.com"}, "test@example.com"},
		{Address{Name: "Test User", Email: "test@example.com"}, "Test User <test@example.com>"},
		{Address{Name: "", Email: "test@example.com"}, "test@example.com"},
	}

	for _, tt := range tests {
		got := tt.addr.String()
		if got != tt.want {
			t.Errorf("Address%+v.String() = %q, want %q", tt.addr, got, tt.want)
		}
	}
}
