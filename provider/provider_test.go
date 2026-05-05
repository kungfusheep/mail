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

func TestParseAddressList(t *testing.T) {
	got := ParseAddressList("Pete <pete@example.com>, test@example.com, , Support <help@example.com>")
	want := []Address{
		{Name: "Pete", Email: "pete@example.com"},
		{Email: "test@example.com"},
		{Name: "Support", Email: "help@example.com"},
	}

	if len(got) != len(want) {
		t.Fatalf("len(ParseAddressList) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ParseAddressList[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseAddressListEmpty(t *testing.T) {
	if got := ParseAddressList(""); got != nil {
		t.Fatalf("ParseAddressList(\"\") = %+v, want nil", got)
	}
}
