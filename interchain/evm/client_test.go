package evm

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"
)

func TestVerifyDestinationChainID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, "eth_chainId", req.Method)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":"0x2105"}`, req.ID)
	}))
	defer server.Close()

	d := &Destination{
		Name:            "base",
		ChainID:         8453,
		Domain:          8453,
		RegistryAddress: "0x1000000000000000000000000000000000000001",
		RPCURL:          server.URL,
	}
	require.NoError(t, VerifyDestinationChainID(d))

	d.ChainID = 1
	err := VerifyDestinationChainID(d)
	require.Error(t, err)
	require.Contains(t, err.Error(), "mismatch")
}

func TestSubmissionValidationReserve(t *testing.T) {
	d := &Destination{
		Name:                   "base",
		ChainID:                8453,
		Domain:                 8453,
		RegistryAddress:        "0x1000000000000000000000000000000000000001",
		RPCURL:                 "https://base.example.invalid",
		DeactivationReserveWei: big.NewInt(1),
		Confirmations:            1,
		MembershipValiditySeconds: 300,
		ExecutorStepDelaySeconds:  10,
	}
	require.NoError(t, d.ValidateSubmission())

	d.DeactivationReserveWei = nil
	require.NoError(t, d.ValidateSubmission())
	require.Error(t, d.ValidateActivation())
}


func TestApplyMembershipTupleABIEncode(t *testing.T) {
	method := registryABI.Methods["applyMembership"]
	require.NotNil(t, method)

	encoded, err := method.Inputs.Encode(map[string]interface{}{
		"transition": map[string]interface{}{
			"expectedSetId":       big.NewInt(7),
			"validUntil":          big.NewInt(1234567890),
			"action":              uint8(1),
			"validator":           ethgo.Address{},
			"blsPublicKey":        make([]byte, 48),
			"blsPublicKeyEIP2537": make([]byte, 128),
		},
		"signerBitmap":       []byte{0x03},
		"aggregateSignature": make([]byte, 256),
	})
	require.NoError(t, err)
	require.NotEmpty(t, encoded)
}
