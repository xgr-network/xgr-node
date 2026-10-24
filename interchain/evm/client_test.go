package evm

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
    "strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"

	"github.com/xgr-network/xgr-node/crypto"
)

func TestVerifyDestinationChainID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
        case "eth_chainId":
            _,_ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":"0x2105"}`, req.ID)
        case "eth_getBlockByNumber":
            _,_ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`,req.ID,
                testFinalityBlock(138,"0x"+strings.Repeat("1",64)))
        default:
            t.Errorf("unexpected RPC method %s",req.Method)
        }
	}))
	defer server.Close()

	d := &Destination{
		Name:            "base",
		ChainID:         8453,
		Domain:          8453,
		RegistryAddress: "0x1000000000000000000000000000000000000001",
        VerifierFormat: VerifierFormatEIP2537,
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
		Name:                      "base",
		ChainID:                   8453,
		Domain:                    8453,
		RegistryAddress:           "0x1000000000000000000000000000000000000001",
        VerifierFormat: VerifierFormatEIP2537,
		RPCURL:                    "https://base.example.invalid",
		MembershipValiditySeconds: 300,
		ExecutorStepDelaySeconds:  10,
	}
	require.NoError(t, d.ValidateSubmission())

	// The activation reserve is always read from the contract, not ENV.
	require.NoError(t, d.ValidateSubmission())
	require.NoError(t, d.ValidateActivation())
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

func TestFormatAggregateSignatureForDestination(t *testing.T) {
	message := []byte("membership")
	sk, err := crypto.GenerateBLSKey()
	require.NoError(t, err)
	raw, err := crypto.SignByBLS(sk, message)
	require.NoError(t, err)
	require.Len(t, raw, 96)

	compressed, err := FormatAggregateSignature(
		&Destination{VerifierFormat: VerifierFormatCompressed},
		raw,
	)
	require.NoError(t, err)
	require.Equal(t, raw, compressed)
	require.Len(t, compressed, 96)

	eip, err := FormatAggregateSignature(
		&Destination{VerifierFormat: VerifierFormatEIP2537},
		raw,
	)
	require.NoError(t, err)
	require.Len(t, eip, 256)
}

func TestFormatAggregateSignatureRejectsUnknownFormat(t *testing.T) {
	_, err := FormatAggregateSignature(
		&Destination{VerifierFormat: "unknown"},
		make([]byte, 96),
	)
	require.Error(t, err)
}
