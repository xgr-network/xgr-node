package evm

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
)

func TestMembershipPollRPCBudget(t *testing.T) {
	address := types.StringToAddress("0x1111111111111111111111111111111111111111")
	secret, err := crypto.GenerateBLSKey()
	require.NoError(t, err)
	publicKey, err := crypto.BLSSecretKeyToPubkeyBytes(secret)
	require.NoError(t, err)
	responses := map[string]string{}
	for method, outputs := range map[string]map[string]interface{}{
		"getValidatorSet": {
			"validators": []ethgo.Address{ethgo.Address(address)}, "blsPublicKeys": [][]byte{publicKey}, "currentSetId": uint64(7),
		},
		"minimumDeactivationReserveWei": {"0": big.NewInt(10)},
		"getValidator":                  {"active": true, "blsPublicKey": publicKey, "reserveWei": big.NewInt(12)},
	} {
		abi := registryABI.Methods[method]
		raw, err := abi.Outputs.Encode(outputs)
		require.NoError(t, err)
		responses["0x"+hex.EncodeToString(abi.ID())] = "0x" + hex.EncodeToString(raw)
	}
	for _, rateLimited := range []bool{false, true} {
		t.Run(fmt.Sprintf("rateLimited=%t", rateLimited), func(t *testing.T) {
			var mu sync.Mutex
			var methods []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					ID     json.RawMessage   `json:"id"`
					Method string            `json:"method"`
					Params []json.RawMessage `json:"params"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					http.Error(w, "invalid RPC request", http.StatusBadRequest)
					return
				}
				mu.Lock()
				methods = append(methods, req.Method)
				mu.Unlock()
				var result interface{}
				switch req.Method {
				case "eth_chainId":
					result = "0x2105"
				case "eth_getBlockByNumber":
					result = json.RawMessage(testFinalityBlock(138, types.Hash{1}.String()))
				case "eth_call":
					if rateLimited {
						w.WriteHeader(http.StatusTooManyRequests)
						return
					}
					var call struct{ Data string }
					if len(req.Params) == 0 || json.Unmarshal(req.Params[0], &call) != nil || len(call.Data) < 10 {
						http.Error(w, "invalid eth_call", http.StatusBadRequest)
						return
					}
					result = responses[call.Data[:10]]
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "result": result})
			}))
			defer server.Close()

			destination := &Destination{
				Name: "base", ChainID: 8453, Domain: 8453, RegistryAddress: address.String(),
				RPCURL: server.URL, VerifierFormat: VerifierFormatEIP2537,
			}
			poll, err := PollMembershipRegistry(destination, address)
			require.NoError(t, err)
			expected := []string{"eth_chainId", "eth_getBlockByNumber", "eth_getBlockByNumber", "eth_call"}
			if rateLimited {
				require.ErrorContains(t, poll.SetError, "HTTP 429")
				require.ErrorContains(t, poll.ReserveError, "cooldown")
			} else {
				expected = append(expected, "eth_call", "eth_call")
				require.NoError(t, poll.SetError)
				require.NoError(t, poll.ReserveError)
				require.Equal(t, uint64(7), poll.Set.SetID)
				require.Equal(t, big.NewInt(10), poll.Minimum)
				require.Equal(t, big.NewInt(12), poll.Local.ReserveWei)
			}
			mu.Lock()
			defer mu.Unlock()
			require.Equal(t, expected, methods)
		})
	}
}
