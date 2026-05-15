package withdraw

import (
	"errors"
	"strings"
	"testing"
)

func TestToWei(t *testing.T) {
	v, err := toWei("123", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := v.String(); got != "12300" {
		t.Fatalf("expected 12300, got %s", got)
	}
}

func TestToWei_InvalidAmount(t *testing.T) {
	if _, err := toWei("abc", 18); err == nil {
		t.Fatal("expected error for invalid amount")
	}
}

func TestToWei_InvalidDecimals(t *testing.T) {
	if _, err := toWei("1", -1); err == nil {
		t.Fatal("expected error for invalid decimals")
	}
}

func TestMapWithdrawError_KnownSelectors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "must deactivate",
			err:  errors.New(`execution reverted","data":"0x34637cc9"`),
			want: "must be deactivated first",
		},
		{
			name: "too early",
			err:  errors.New(`execution reverted","data":"0x5c940c69"`),
			want: "too early after deactivation",
		},
		{
			name: "below min stake",
			err:  errors.New(`execution reverted","data":"0x6eda21dd"`),
			want: "below MIN_STAKE",
		},
		{
			name: "invalid amount",
			err:  errors.New(`execution reverted","data":"0xdb73cdf0"`),
			want: "invalid amount",
		},
		{
			name: "validator missing",
			err:  errors.New(`execution reverted","data":"0x580e542f"`),
			want: "validator not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapWithdrawError(tt.err)
			if got == nil || !strings.Contains(got.Error(), tt.want) {
				t.Fatalf("expected %q in error, got %v", tt.want, got)
			}
		})
	}
}

func TestMapWithdrawError_Fallback(t *testing.T) {
	err := errors.New("some other rpc failure")
	got := mapWithdrawError(err)
	if got == nil || !strings.Contains(got.Error(), "withdraw tx failed") {
		t.Fatalf("expected wrapped withdraw tx failed error, got %v", got)
	}
}
