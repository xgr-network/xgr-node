package interchain

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"

	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	"github.com/xgr-network/xgr-node/contracts/abis"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/secrets"
	"github.com/xgr-network/xgr-node/secrets/local"
	"github.com/xgr-network/xgr-node/types"
)

const testRegistryAddress = "0x1000000000000000000000000000000000000001"

type testRPCServer struct {
	*httptest.Server
	calls atomic.Int64
}

func newTestRPCServer(
	t *testing.T,
	handler func(method string, params json.RawMessage) (interface{}, error),
) *testRPCServer {
	t.Helper()

	srv := &testRPCServer{}
	srv.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		srv.calls.Add(1)

		var request map[string]json.RawMessage
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))

		var method string
		require.NoError(t, json.Unmarshal(request["method"], &method))

		result, err := handler(method, request["params"])
		if err != nil {
			writeRPCError(w, request["id"], err)
			return
		}

		writeRPCResult(w, request["id"], result)
	}))

	t.Cleanup(srv.Close)

	return srv
}

func writeRPCResult(w http.ResponseWriter, id json.RawMessage, result interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, err error) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]interface{}{
			"code":    -32000,
			"message": err.Error(),
		},
	})
}

type testValidatorSecrets struct {
	dataDir string
	address types.Address
	blsPub  []byte
}

func newTestValidatorSecrets(t *testing.T, includeBLS bool) testValidatorSecrets {
	t.Helper()

	dataDir := t.TempDir()
	sm, err := local.SecretsManagerFactory(nil, &secrets.SecretsManagerParams{
		Logger: hclog.NewNullLogger(),
		Extra: map[string]interface{}{
			secrets.Path: dataDir,
		},
	})
	require.NoError(t, err)

	ecdsaKey, ecdsaEncoded, err := crypto.GenerateAndEncodeECDSAPrivateKey()
	require.NoError(t, err)
	require.NoError(t, sm.SetSecret(secrets.ValidatorKey, ecdsaEncoded))

	out := testValidatorSecrets{
		dataDir: dataDir,
		address: crypto.PubKeyToAddress(&ecdsaKey.PublicKey),
	}

	if !includeBLS {
		return out
	}

	blsKey, blsEncoded, err := crypto.GenerateAndEncodeBLSSecretKey()
	require.NoError(t, err)
	require.NoError(t, sm.SetSecret(secrets.ValidatorBLSKey, blsEncoded))

	out.blsPub, err = crypto.BLSSecretKeyToPubkeyBytes(blsKey)
	require.NoError(t, err)

	return out
}

func newXGRRPC(
	t *testing.T,
	exists bool,
	posActive bool,
	registeredBLS []byte,
) *testRPCServer {
	t.Helper()

	method := abis.StakingABI.Methods["validatorInfo"]
	require.NotNil(t, method)

	encodedInfo, err := method.Outputs.Encode(map[string]interface{}{
		"exists":             exists,
		"active":             posActive,
		"stakedAmount":       big.NewInt(2_000_000),
		"deactivatedAtBlock": big.NewInt(0),
		"blsPubKey":          registeredBLS,
	})
	require.NoError(t, err)
	callResult := "0x" + hex.EncodeToString(encodedInfo)

	return newTestRPCServer(t, func(method string, _ json.RawMessage) (interface{}, error) {
		switch method {
		case "eth_chainId":
			return "0x66b", nil
		case "eth_call":
			return callResult, nil
		default:
			return nil, errors.New("unexpected XGR RPC method: " + method)
		}
	})
}

func newDestinationRPC(t *testing.T, active bool, setID uint64) *testRPCServer {
	t.Helper()

	raw := make([]byte, 64)
	if active {
		raw[31] = 1
	}
	binary.BigEndian.PutUint64(raw[56:64], setID)
	result := "0x" + hex.EncodeToString(raw)

	return newTestRPCServer(t, func(method string, _ json.RawMessage) (interface{}, error) {
		if method != "eth_call" {
			return nil, errors.New("unexpected destination RPC method: " + method)
		}
		return result, nil
	})
}

func configureBaseDestination(t *testing.T, rpcURL string) {
	t.Helper()
	t.Setenv("XGR_INTERCHAIN_BASE_DOMAIN", "8453")
	t.Setenv("XGR_INTERCHAIN_BASE_REGISTRY_ADDR", testRegistryAddress)
	t.Setenv("XGR_INTERCHAIN_BASE_RPC", rpcURL)
}

func assertSignedMembershipTransition(
	t *testing.T,
	result *SetActiveResult,
	expectedAction protocol.Action,
	expectedPub []byte,
	expectedSetID uint64,
) {
	t.Helper()

	require.False(t, result.AlreadyInRequestedState)
	require.Equal(t, expectedSetID, result.SetID)
	require.NotEmpty(t, result.PayloadHash)
	require.NotEmpty(t, result.Signature)
	require.Equal(t, "0x"+hex.EncodeToString(expectedPub), result.BLSPublicKey)

	payload := protocol.MembershipPayload{
		OriginChainID:     1643,
		DestinationDomain: 8453,
		SetID:             expectedSetID,
		Action:            expectedAction,
		Validator:         types.StringToAddress(result.Validator),
		BLSPublicKey:      expectedPub,
	}

	message, err := payload.MarshalBinary()
	require.NoError(t, err)
	payloadHash, err := payload.Hash()
	require.NoError(t, err)
	require.Equal(t, payloadHash.String(), result.PayloadHash)

	signature, err := hex.DecodeString(strings.TrimPrefix(result.Signature, "0x"))
	require.NoError(t, err)
	require.NoError(t, crypto.VerifyBLSSignatureFromBytes(expectedPub, signature, message))
}

func TestPrepareSetActiveCreatesSignedAddAndRemoveTransitions(t *testing.T) {
	tests := []struct {
		name          string
		requested     bool
		current       bool
		action        protocol.Action
		setID         uint64
	}{
		{
			name:      "add",
			requested: true,
			current:   false,
			action:    protocol.ActionAddValidator,
			setID:     7,
		},
		{
			name:      "remove",
			requested: false,
			current:   true,
			action:    protocol.ActionRemoveValidator,
			setID:     19,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := newTestValidatorSecrets(t, true)
			xgrRPC := newXGRRPC(t, true, true, validator.blsPub)
			destinationRPC := newDestinationRPC(t, tt.current, tt.setID)
			configureBaseDestination(t, destinationRPC.URL)

			result, err := prepareSetActive(&setActiveParams{
				jsonRPC: xgrRPC.URL,
				dataDir: validator.dataDir,
				chain:   "base",
				active:  tt.requested,
			})
			require.NoError(t, err)
			require.Equal(t, validator.address.String(), result.Validator)
			require.Equal(t, tt.current, result.CurrentInterchainActive)
			require.Equal(t, tt.requested, result.RequestedActive)
			require.Equal(t, uint64(1643), result.OriginChainID)
			require.Equal(t, uint32(8453), result.DestinationDomain)

			assertSignedMembershipTransition(t, result, tt.action, validator.blsPub, tt.setID)
		})
	}
}

func TestPrepareSetActiveIsIdempotentNoop(t *testing.T) {
	tests := []struct {
		name      string
		requested bool
		posActive bool
	}{
		{name: "already active", requested: true, posActive: true},
		{name: "already inactive", requested: false, posActive: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := newTestValidatorSecrets(t, false)
			xgrRPC := newXGRRPC(t, true, tt.posActive, []byte{1, 2, 3})
			destinationRPC := newDestinationRPC(t, tt.requested, 23)
			configureBaseDestination(t, destinationRPC.URL)

			result, err := prepareSetActive(&setActiveParams{
				jsonRPC: xgrRPC.URL,
				dataDir: validator.dataDir,
				chain:   "base",
				active:  tt.requested,
			})
			require.NoError(t, err)
			require.True(t, result.AlreadyInRequestedState)
			require.Equal(t, uint64(23), result.SetID)
			require.Empty(t, result.PayloadHash)
			require.Empty(t, result.Signature)
			require.Empty(t, result.BLSPublicKey)
		})
	}
}

func TestPrepareSetActiveRejectsInactivePoSActivationBeforeDestinationRead(t *testing.T) {
	validator := newTestValidatorSecrets(t, false)
	xgrRPC := newXGRRPC(t, true, false, []byte{1, 2, 3})
	destinationRPC := newDestinationRPC(t, false, 3)
	configureBaseDestination(t, destinationRPC.URL)

	_, err := prepareSetActive(&setActiveParams{
		jsonRPC: xgrRPC.URL,
		dataDir: validator.dataDir,
		chain:   "base",
		active:  true,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "is not active in XGR PoS")
	require.Zero(t, destinationRPC.calls.Load())
}

func TestPrepareSetActiveRejectsBLSMismatch(t *testing.T) {
	validator := newTestValidatorSecrets(t, true)

	otherKey, _, err := crypto.GenerateAndEncodeBLSSecretKey()
	require.NoError(t, err)
	otherPub, err := crypto.BLSSecretKeyToPubkeyBytes(otherKey)
	require.NoError(t, err)

	xgrRPC := newXGRRPC(t, true, true, otherPub)
	destinationRPC := newDestinationRPC(t, false, 5)
	configureBaseDestination(t, destinationRPC.URL)

	_, err = prepareSetActive(&setActiveParams{
		jsonRPC: xgrRPC.URL,
		dataDir: validator.dataDir,
		chain:   "base",
		active:  true,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "local BLS key does not match")
}

func TestPrepareSetActiveRejectsMalformedDestinationStatus(t *testing.T) {
	validator := newTestValidatorSecrets(t, false)
	xgrRPC := newXGRRPC(t, true, true, []byte{1, 2, 3})

	destinationRPC := newTestRPCServer(t, func(method string, _ json.RawMessage) (interface{}, error) {
		if method != "eth_call" {
			return nil, errors.New("unexpected destination RPC method: " + method)
		}
		return "0x01", nil
	})
	configureBaseDestination(t, destinationRPC.URL)

	_, err := prepareSetActive(&setActiveParams{
		jsonRPC: xgrRPC.URL,
		dataDir: validator.dataDir,
		chain:   "base",
		active:  true,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid getValidatorStatus response length")
}

func TestInterchainCLIEndToEndWithMockRPC(t *testing.T) {
	validator := newTestValidatorSecrets(t, true)
	xgrRPC := newXGRRPC(t, true, true, validator.blsPub)
	destinationRPC := newDestinationRPC(t, false, 31)
	configureBaseDestination(t, destinationRPC.URL)

	cmd := getSetActiveCommand()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{
		"--chain", "base",
		"--active", "true",
		"--data-dir", validator.dataDir,
		"--jsonrpc", xgrRPC.URL,
	})

	require.NoError(t, cmd.Execute())
	require.GreaterOrEqual(t, xgrRPC.calls.Load(), int64(2))
	require.Equal(t, int64(1), destinationRPC.calls.Load())
}
