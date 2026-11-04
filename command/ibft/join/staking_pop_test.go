package join

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/abi"
	"github.com/umbracle/ethgo/jsonrpc"
	"github.com/xgr-network/xgr-node/contracts/staking"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/secrets"
	secretsHelper "github.com/xgr-network/xgr-node/secrets/helper"
	"github.com/xgr-network/xgr-node/txrelayer"
	"github.com/xgr-network/xgr-node/types"
)

type popJoinRelayer struct {
	txrelayer.TxRelayer
	client *jsonrpc.Client
	call   func(ethgo.Address, ethgo.Address, []byte) (string, error)
	send   func(*ethgo.Transaction, ethgo.Key) (*ethgo.Receipt, error)
}

func (r *popJoinRelayer) Client() *jsonrpc.Client { return r.client }
func (r *popJoinRelayer) Call(from, to ethgo.Address, input []byte) (string, error) {
	return r.call(from, to, input)
}
func (r *popJoinRelayer) SendTransaction(tx *ethgo.Transaction, key ethgo.Key) (*ethgo.Receipt, error) {
	return r.send(tx, key)
}

func TestJoinAutomaticallySuppliesPoPAndNeverDowngradesRegistration(t *testing.T) {
	sm, err := secretsHelper.SetupLocalSecretsManager(t.TempDir())
	require.NoError(t, err)
	_, err = secretsHelper.InitECDSAValidatorKey(sm)
	require.NoError(t, err)
	pub, err := secretsHelper.InitBLSValidatorKey(sm)
	require.NoError(t, err)
	ecdsaKey, pubFromFiles, err := loadKeysFromSecrets(sm)
	require.NoError(t, err)
	require.Equal(t, pub, pubFromFiles)
	addr := types.Address(ecdsaKey.Address())
	legacy, candidate, err := staking.PoPBytecodes()
	require.NoError(t, err)
	for _, name := range []string{"candidate", "candidate-reverted", "legacy", "unknown"} {
		t.Run(name, func(t *testing.T) {
			code := candidate
			if name == "legacy" {
				code = legacy
			}
			if name == "unknown" {
				code = []byte{0x00}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				var result string
				switch request.Method {
				case "eth_getCode":
					result = "0x" + hex.EncodeToString(code)
				case "eth_chainId":
					result = "0x66b"
				default:
					t.Errorf("unexpected local fixture RPC %s", request.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": request.ID, "result": result})
			}))
			defer server.Close()
			client, err := jsonrpc.NewClient(server.URL)
			require.NoError(t, err)
			count := 0
			relayer := &popJoinRelayer{client: client}
			relayer.call = func(from, to ethgo.Address, input []byte) (string, error) {
				require.Equal(t, ecdsaKey.Address(), from)
				require.Equal(t, ethgo.Address(staking.AddrStakingContract), to)
				require.True(t, bytes.Equal(input[:4], crypto.Keccak256([]byte("blsPoPNonce(address)"))[:4]))
				require.Equal(t, addr.Bytes(), input[16:])
				return "0x" + fmt.Sprintf("%064x", 5), nil
			}
			relayer.send = func(tx *ethgo.Transaction, _ ethgo.Key) (*ethgo.Receipt, error) {
				count++
				if name == "legacy" {
					require.True(t, bytes.Equal(tx.Input[:4], crypto.Keccak256([]byte("registerBLSPublicKey(bytes)"))[:4]))
				} else {
					require.True(t, bytes.Equal(tx.Input[:4], crypto.Keccak256([]byte("registerBLSPublicKey(bytes,bytes)"))[:4]))
					decoded, err := abi.MustNewType("tuple(bytes,bytes)").Decode(tx.Input[4:])
					require.NoError(t, err)
					values := decoded.(map[string]interface{})
					require.NoError(t, crypto.VerifyStakingPoP(big.NewInt(1643), staking.AddrStakingContract, addr, values["0"].([]byte), values["1"].([]byte), big.NewInt(5)))
				}
				if name == "candidate-reverted" {
					return nil, fmt.Errorf("local rejection fixture")
				}
				return &ethgo.Receipt{Status: 1}, nil
			}
			err = sendRegisterBLS(relayer, ecdsaKey, pub, sm)
			if name == "unknown" {
				require.Error(t, err)
				require.Zero(t, count)
			} else if name == "candidate-reverted" {
				require.Error(t, err)
				require.Equal(t, 1, count)
			} else {
				require.NoError(t, err)
				require.Equal(t, 1, count)
			}
			secretAfter, err := sm.GetSecret(secrets.ValidatorBLSKey)
			require.NoError(t, err)
			require.NotEmpty(t, secretAfter)
		})
	}
}
