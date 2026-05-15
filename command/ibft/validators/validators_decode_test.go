package validators

import (
	"reflect"
	"testing"

	"github.com/xgr-network/xgr-node/contracts/abis"
)

func TestDecodeBytesArray(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     interface{}
		want    [][]byte
		wantErr bool
	}{
		{name: "native bytes", raw: [][]byte{{0x01}, {0x02, 0x03}}, want: [][]byte{{0x01}, {0x02, 0x03}}},
		{name: "hex string array", raw: []string{"0x01", "0203"}, want: [][]byte{{0x01}, {0x02, 0x03}}},
		{name: "fixed byte arrays", raw: [][2]byte{{0x01, 0x02}, {0x03, 0x04}}, want: [][]byte{{0x01, 0x02}, {0x03, 0x04}}},
		{name: "interface fixed array", raw: []interface{}{[2]byte{0x01, 0x02}}, want: [][]byte{{0x01, 0x02}}},
		{name: "unexpected item", raw: []interface{}{123}, wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := decodeBytesArray(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("decodeBytesArray() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("decodeBytesArray() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFirstOutputValue(t *testing.T) {
	t.Parallel()

	m := abis.StakingABI.Methods["validatorBLSPublicKeys"]
	if m == nil {
		t.Fatal("missing method")
	}

	val, err := firstOutputValue(m, map[string]interface{}{"keys": []string{"0x01"}})
	if err != nil {
		t.Fatalf("firstOutputValue() error = %v", err)
	}

	if _, ok := val.([]string); !ok {
		t.Fatalf("firstOutputValue() returned unexpected type %T", val)
	}
}
