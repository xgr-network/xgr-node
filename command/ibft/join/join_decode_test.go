package join

import (
	"math/big"
	"reflect"
	"testing"
)

func TestToBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     interface{}
		want    []byte
		wantErr bool
	}{
		{name: "native bytes", raw: []byte{0x01, 0x02}, want: []byte{0x01, 0x02}},
		{name: "hex string", raw: "0x0102", want: []byte{0x01, 0x02}},
		{name: "fixed byte array", raw: [2]byte{0x01, 0x02}, want: []byte{0x01, 0x02}},
		{name: "unexpected type", raw: 123, wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := toBytes(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("toBytes() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("toBytes() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNormalizeJSONRPCAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		out  string
	}{
		{name: "rewrites all interfaces host", in: "http://0.0.0.0:8545", out: "http://127.0.0.1:8545"},
		{name: "keeps localhost", in: "http://127.0.0.1:8545", out: "http://127.0.0.1:8545"},
		{name: "keeps domain", in: "https://rpc.example.org:443", out: "https://rpc.example.org:443"},
		{name: "keeps invalid url", in: ":://broken", out: ":://broken"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizeJSONRPCAddress(tt.in); got != tt.out {
				t.Fatalf("normalizeJSONRPCAddress() = %q, want %q", got, tt.out)
			}
		})
	}
}

func TestWeiToXGRString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		wei  string
		dec  int
		want string
	}{
		{name: "whole", wei: "2000000000000000000000000", dec: 18, want: "2000000"},
		{name: "fraction", wei: "1500000000000000000", dec: 18, want: "1.5"},
		{name: "small", wei: "1", dec: 18, want: "0.000000000000000001"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v, ok := new(big.Int).SetString(tt.wei, 10)
			if !ok {
				t.Fatalf("invalid test wei")
			}
			if got := weiToXGRString(v, tt.dec); got != tt.want {
				t.Fatalf("weiToXGRString() = %q, want %q", got, tt.want)
			}
		})
	}
}
