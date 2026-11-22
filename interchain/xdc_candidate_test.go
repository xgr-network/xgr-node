package interchain

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestExistingMainnetPoliciesRemainBLSOnly(t *testing.T) {
	active, err := ActivePolicies()
	require.NoError(t, err)
	require.Len(t, active, 5)
	for _, p := range active {
		require.Equal(t, "bls", p.SignatureScheme)
		require.Contains(t, []string{"compressed", "eip2537"}, p.VerifierFormat)
		require.NotEqual(t, uint64(50), p.ChainID)
	}
}
func TestXDCMainnetCandidateIsReceiptPinnedButInactive(t *testing.T) {
	p := XDCMainnetCandidate()
	require.Equal(t, uint64(50), p.ChainID)
	require.Equal(t, uint32(50), p.Domain)
	require.Equal(t, "ecdsa", p.SignatureScheme)
	require.Equal(t, "ecdsa-secp256k1", p.VerifierFormat)
	require.Equal(t, "0x0334b4c9b1307fce68f25e72b65ab32dcc83343b", p.Registry)
	require.Equal(t, "0x6d0c5859a80a0927b3e43bf56e532f9f16a13035", p.SourceRegistry)
	require.Equal(t, uint64(109627114), p.SourceActivationBlock)
	require.Equal(t, FinalityFinalized, p.Finality)
	require.Len(t, p.RPCURLs, 3)
	require.Len(t, p.IndependentOperatorIDs, 3)
	require.NoError(t, validatePolicy(p))
	_, err := Policy("xdc")
	require.Error(t, err)
}
func TestBLSSchemeCannotAccidentallyBecomeECDSA(t *testing.T) {
	p, err := Policy("base")
	require.NoError(t, err)
	p.SignatureScheme = "ecdsa"
	require.Error(t, validatePolicy(p))
	p, err = Policy("xgr")
	require.NoError(t, err)
	p.SignatureScheme = ""
	require.Error(t, validatePolicy(p))
}
