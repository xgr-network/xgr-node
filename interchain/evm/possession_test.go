package evm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
    "strings"
	"testing"

	"github.com/stretchr/testify/require"

	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
)

// Independent manual canonical encoding checks the critical Solidity
// keccak256(abi.encode(address, bytes, bytes)) dynamic ABI offsets.
func TestV3PossessionKeyHashUsesCanonicalSolidityEncoding(t *testing.T) {
	var validator types.Address
	for i := range validator {
		validator[i] = byte(i + 1)
	}
	pub := bytes.Repeat([]byte{0x11}, 48)
	eip := bytes.Repeat([]byte{0x22}, 128)
	payload := protocol.MembershipPayload{
		Validator: validator, BLSPublicKey: pub, BLSPublicKeyEIP2537: eip,
	}
	got, err := possessionKeyHash(payload)
	require.NoError(t, err)

	canonical := make([]byte, 352)
	copy(canonical[12:32], validator.Bytes())
	big.NewInt(96).FillBytes(canonical[32:64])
	big.NewInt(192).FillBytes(canonical[64:96])
	big.NewInt(48).FillBytes(canonical[96:128])
	copy(canonical[128:176], pub)
	big.NewInt(128).FillBytes(canonical[192:224])
	copy(canonical[224:352], eip)
	require.Equal(t, crypto.Keccak256Hash(canonical), got)
}

func TestV3PossessionABIBindingsExist(t *testing.T) {
	require.NotNil(t, possessionABI.Methods["provePossession"])
	require.NotNil(t, possessionABI.Methods["possessionVerified"])
}

// Verify the actual public Registry V3 mapping lookup before any registration
// transaction. Unavailable or malformed RPC responses must fail closed.
func TestV3PossessionVerifiedRPCLookup(t *testing.T) {
	var validator types.Address
	validator[19] = 0x45
	payload := protocol.MembershipPayload{
		Validator:           validator,
		BLSPublicKey:        bytes.Repeat([]byte{0x11}, 48),
		BLSPublicKeyEIP2537: bytes.Repeat([]byte{0x22}, 128),
	}
	keyHash, err := possessionKeyHash(payload)
	require.NoError(t, err)
	method := possessionABI.Methods["possessionVerified"]
	args, err := method.Inputs.Encode(map[string]interface{}{"keyHash": [32]byte(keyHash)})
	require.NoError(t, err)
	expectedData := "0x" + fmt.Sprintf("%x", append(method.ID(), args...))
	scenarios := []struct {
		name   string
		chain  string
		answer string
		want   bool
		fails  bool
	}{
		{name: "verified", chain: "0x2105", answer: "0x" + fmt.Sprintf("%064x", 1), want: true},
		{name: "unverified", chain: "0x2105", answer: "0x" + fmt.Sprintf("%064x", 0)},
		{name: "malformed", chain: "0x2105", answer: "0x" + fmt.Sprintf("%064x", 2), fails: true},
		{name: "wrong destination chain", chain: "0x1", fails: true},
	}
	for _, tt := range scenarios {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					ID     json.RawMessage   `json:"id"`
					Method string            `json:"method"`
					Params []json.RawMessage `json:"params"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("decode JSON-RPC request: %v", err)
					return
				}
				response := tt.answer
				switch req.Method {
				case "eth_chainId":
					response = tt.chain
                case "eth_getBlockByNumber":
                    w.Header().Set("Content-Type","application/json")
                    _,_ = fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"result":%s}`,req.ID,
                        testFinalityBlock(138,"0x"+strings.Repeat("1",64)))
                    return
				case "eth_call":
					if len(req.Params) == 0 {
						t.Error("eth_call without params")
						return
					}
					var call struct {
						To   string `json:"to"`
						Data string `json:"data"`
					}
					if err := json.Unmarshal(req.Params[0], &call); err != nil {
						t.Errorf("decode eth_call: %v", err)
						return
					}
					if call.Data != expectedData {
						t.Errorf("unexpected possession key getter calldata: %s", call.Data)
					}
				default:
					t.Errorf("unexpected JSON-RPC method: %s", req.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%q}`, req.ID, response)
			}))
			defer server.Close()
			dest := &Destination{Name: "base", ChainID: 8453, Domain: 8453,
				RegistryAddress: "0x1000000000000000000000000000000000000001", RPCURL: server.URL, VerifierFormat:VerifierFormatEIP2537}
			got, err := readPossessionVerified(dest, payload)
			if tt.fails {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
