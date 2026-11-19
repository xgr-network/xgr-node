package evm

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/wallet"
	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/interchain/authorization"
	"github.com/xgr-network/xgr-node/types"
)

func authorizationFixture(t *testing.T, scheme string, n int) (*Destination, *authorization.Adapter, authorization.Set, []authorization.Keys, authorization.Membership) {
	t.Helper()
	adapter, err := authorization.ForScheme(scheme)
	require.NoError(t, err)
	destination := &Destination{ChainID: 50, Domain: 50, SignatureScheme: scheme, VerifierFormat: VerifierFormatECDSA,
		RegistryAddress: "0x1111111111111111111111111111111111111111"}
	if scheme == authorization.SchemeBLS {
		destination.VerifierFormat = VerifierFormatEIP2537
	}
	set := authorization.Set{SetID: 7}
	keys := make([]authorization.Keys, n)
	for i := range keys {
		key, err := crypto.GenerateECDSAKey()
		require.NoError(t, err)
		keys[i].ECDSA = key
		addr := crypto.PubKeyToAddress(&key.PublicKey)
		set.Validators = append(set.Validators, addr)
		if scheme == authorization.SchemeECDSA {
			set.PublicKeys = append(set.PublicKeys, addr.Bytes())
		} else {
			bls, err := crypto.GenerateBLSKey()
			require.NoError(t, err)
			raw, err := bls.MarshalBinary()
			require.NoError(t, err)
			keys[i].BLS = []byte(hex.EncodeToString(raw))
			pub, err := crypto.BLSSecretKeyToPubkeyBytes(bls)
			require.NoError(t, err)
			set.PublicKeys = append(set.PublicKeys, pub)
		}
	}
	m := authorization.Membership{OriginChainID: 1643, DestinationDomain: 50, Registry: types.StringToAddress(destination.RegistryAddress),
		ExpectedSetID: set.SetID, ValidUntil: uint64(time.Now().Unix()) + 300, Action: protocol.ActionAddValidator, Validator: set.Validators[0]}
	if scheme == authorization.SchemeBLS {
		m.BLSPublicKey = set.PublicKeys[0]
		m.BLSPublicKeyEIP2537, err = crypto.BLSPublicKeyToEIP2537(m.BLSPublicKey)
		require.NoError(t, err)
	}
	return destination, adapter, set, keys, m
}

func membershipProof(t *testing.T, a *authorization.Adapter, set authorization.Set, keys []authorization.Keys, m authorization.Membership, indices []int) authorization.Proof {
	t.Helper()
	votes := make([]authorization.Vote, 0, len(indices))
	for _, i := range indices {
		sig, err := a.SignMembership(keys[i], m, time.Now())
		require.NoError(t, err)
		votes = append(votes, authorization.Vote{ValidatorIndex: i, Signature: sig})
	}
	proof, err := a.AssembleMembershipQuorum(set, votes, m)
	require.NoError(t, err)
	return proof
}

func TestAuthorizationAdapterRequiresExplicitSchemeFormatPair(t *testing.T) {
	for _, tt := range []struct{ scheme, format, want string }{
		{"", VerifierFormatCompressed, authorization.SchemeBLS},
		{"", VerifierFormatEIP2537, authorization.SchemeBLS},
		{authorization.SchemeBLS, VerifierFormatCompressed, authorization.SchemeBLS},
		{authorization.SchemeECDSA, VerifierFormatECDSA, authorization.SchemeECDSA},
		{"", VerifierFormatECDSA, ""},
		{authorization.SchemeBLS, VerifierFormatECDSA, ""},
		{authorization.SchemeECDSA, VerifierFormatEIP2537, ""},
		{"unknown", VerifierFormatCompressed, ""},
		{"", "", ""},
	} {
		t.Run(tt.scheme+"/"+tt.format, func(t *testing.T) {
			a, err := (&Destination{SignatureScheme: tt.scheme, VerifierFormat: tt.format}).AuthorizationAdapter()
			if tt.want == "" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, a.Scheme())
		})
	}
}

func TestRegistrySetReadUsesConfiguredSchemeAndCanonicalKeys(t *testing.T) {
	for _, scheme := range []string{authorization.SchemeBLS, authorization.SchemeECDSA} {
		t.Run(scheme, func(t *testing.T) {
			d, _, set, _, _ := authorizationFixture(t, scheme, 3)
			encode := func(addresses []types.Address, keys [][]byte, epoch uint64) []byte {
				ethAddresses := make([]ethgo.Address, len(addresses))
				for i, a := range addresses {
					ethAddresses[i] = ethgo.Address(a)
				}
				raw, err := registryABI.Methods["getValidatorSet"].Outputs.Encode(map[string]interface{}{
					"validators": ethAddresses, "blsPublicKeys": keys, "currentSetId": epoch})
				require.NoError(t, err)
				return raw
			}
			raw := encode(set.Validators, set.PublicKeys, set.SetID)
			got, err := decodeRegistryValidatorSet(d, raw)
			require.NoError(t, err)
			require.Equal(t, scheme, got.Scheme)
			require.Equal(t, set, got.AuthorizationSet())
			if scheme == authorization.SchemeBLS {
				require.Equal(t, set.PublicKeys, got.BLSPublicKeys)
			} else {
				require.Nil(t, got.BLSPublicKeys)
			}
			wrong := *d
			if scheme == authorization.SchemeBLS {
				wrong.SignatureScheme = authorization.SchemeECDSA
				wrong.VerifierFormat = VerifierFormatECDSA
			} else {
				wrong.SignatureScheme = authorization.SchemeBLS
				wrong.VerifierFormat = VerifierFormatEIP2537
			}
			_, err = decodeRegistryValidatorSet(&wrong, raw)
			require.Error(t, err, "a response must not select its own signature scheme")
			duplicate := append([]types.Address(nil), set.Validators...)
			duplicate[1] = duplicate[0]
			_, err = decodeRegistryValidatorSet(d, encode(duplicate, set.PublicKeys, set.SetID))
			require.Error(t, err)
			_, err = decodeRegistryValidatorSet(d, encode(set.Validators, set.PublicKeys, 0))
			require.Error(t, err)
			_, err = decodeRegistryValidatorSet(d, encode(set.Validators, set.PublicKeys[:2], set.SetID))
			require.Error(t, err)
		})
	}
}

func TestBLSMembershipCalldataPreservesV3ABIAndSignature(t *testing.T) {
	d, a, set, keys, m := authorizationFixture(t, authorization.SchemeBLS, 3)
	proof := membershipProof(t, a, set, keys, m, []int{2, 0})
	got, err := EncodeMembershipCalldata(d, m, proof)
	require.NoError(t, err)
	sig, err := crypto.BLSSignatureToEIP2537(proof.Signature)
	require.NoError(t, err)
	method := registryABI.Methods["applyMembership"]
	want, err := method.Inputs.Encode(map[string]interface{}{
		"transition": map[string]interface{}{"expectedSetId": new(big.Int).SetUint64(m.ExpectedSetID), "validUntil": new(big.Int).SetUint64(m.ValidUntil),
			"action": uint8(m.Action), "validator": ethgo.Address(m.Validator), "blsPublicKey": m.BLSPublicKey, "blsPublicKeyEIP2537": m.BLSPublicKeyEIP2537},
		"signerBitmap": proof.Bitmap, "aggregateSignature": sig})
	require.NoError(t, err)
	require.Equal(t, append(method.ID(), want...), got)
	require.Equal(t, crypto.Keccak256([]byte("applyMembership((uint64,uint64,uint8,address,bytes,bytes),bytes,bytes)"))[:4], got[:4])
}

func TestECDSAMembershipAndGovernanceKeepMultiByteBitmapOrder(t *testing.T) {
	d, a, set, keys, m := authorizationFixture(t, authorization.SchemeECDSA, 9)
	indices := []int{8, 6, 5, 4, 3, 2}
	proof := membershipProof(t, a, set, keys, m, indices)
	require.Equal(t, []byte{0x7c, 0x01}, proof.Bitmap)
	got, err := EncodeMembershipCalldata(d, m, proof)
	require.NoError(t, err)
	require.Equal(t, crypto.Keccak256([]byte("applyMembership((uint64,uint64,uint8,address),bytes,bytes)"))[:4], got[:4])
	decoded, err := ecdsaMembershipABI.Methods["applyMembership"].Inputs.Decode(got[4:])
	require.NoError(t, err)
	values := decoded.(map[string]interface{})
	require.Equal(t, proof.Bitmap, values["signerBitmap"])
	require.Equal(t, proof.Signature, values["signatures"])
	// Destination adapters copy proof bytes; changing a formatted response may
	// not corrupt the durable quorum used for recovery/rebroadcast.
	formatted, err := FormatQuorumProof(d, proof)
	require.NoError(t, err)
	formatted.Bitmap[0] = 0
	formatted.Signature[0] ^= 1
	require.Equal(t, byte(0x7c), proof.Bitmap[0])
	require.NotEqual(t, formatted.Signature, proof.Signature)

	proposal := protocol.ILNSourceFeeProposal{SourceChainID: 50, SourceDomain: 50, Registry: m.Registry, SetID: set.SetID,
		Nonce: 1, ValidUntil: m.ValidUntil, ValidatorFeeWei: big.NewInt(8000000000000000)}
	votes := make([]authorization.Vote, 0, len(indices))
	for _, i := range indices {
		sig, err := a.SignSourceFee(keys[i], proposal)
		require.NoError(t, err)
		votes = append(votes, authorization.Vote{ValidatorIndex: i, Signature: sig})
	}
	feeProof, err := a.AssembleSourceFeeQuorum(set, votes, proposal)
	require.NoError(t, err)
	data, err := EncodeILNGovernanceAuthorizationCalldata(d, proposal, feeProof)
	require.NoError(t, err)
	decoded, err = ilnApplyGovernanceABI.Methods["applySourceFee"].Inputs.Decode(data[4:])
	require.NoError(t, err)
	values = decoded.(map[string]interface{})
	require.Equal(t, []byte{0x7c, 0x01}, values["signerBitmap"])
	require.Equal(t, feeProof.Signature, values["aggregateSignature"])
}

func TestPossessionAdapterUsesCandidateAndConfiguredRegistry(t *testing.T) {
	for _, scheme := range []string{authorization.SchemeBLS, authorization.SchemeECDSA} {
		t.Run(scheme, func(t *testing.T) {
			d, a, _, keys, m := authorizationFixture(t, scheme, 1)
			proof, err := a.SignPossession(keys[0], m)
			require.NoError(t, err)
			data, err := EncodePossessionCalldata(d, m, proof)
			require.NoError(t, err)
			method := possessionABI.Methods["provePossession"]
			if scheme == authorization.SchemeECDSA {
				method = ecdsaPossessionABI.Methods["provePossession"]
			}
			require.Equal(t, method.ID(), data[:4])
			decoded, err := method.Inputs.Decode(data[4:])
			require.NoError(t, err)
			values := decoded.(map[string]interface{})
			require.Equal(t, ethgo.Address(m.Validator), values["validator"])
			if scheme == authorization.SchemeECDSA {
				require.Equal(t, proof, values["proof"])
			} else {
				expanded, err := crypto.BLSSignatureToEIP2537(proof)
				require.NoError(t, err)
				require.Equal(t, expanded, values["possessionProof"])
			}
			other := *d
			other.RegistryAddress = "0x2222222222222222222222222222222222222222"
			_, err = EncodePossessionCalldata(&other, m, proof)
			require.ErrorContains(t, err, "registry")
			moved := m
			moved.Registry = types.StringToAddress(other.RegistryAddress)
			if scheme == authorization.SchemeECDSA {
				_, err = EncodePossessionCalldata(&other, moved, proof)
				require.ErrorContains(t, err, "possession")
			}
			otherKey, err := crypto.GenerateECDSAKey()
			require.NoError(t, err)
			err = EnsureValidatorPossessionAuthorization(d, wallet.NewKey(otherKey), m, proof)
			require.ErrorContains(t, err, "not the local validator candidate")
		})
	}
}

func TestECDSAProofFormattingRejectsMalformedSignatureAndScheme(t *testing.T) {
	d, a, set, keys, m := authorizationFixture(t, authorization.SchemeECDSA, 3)
	proof := membershipProof(t, a, set, keys, m, []int{0, 2})
	for _, bad := range []authorization.Proof{
		{Bitmap: nil, Signature: proof.Signature},
		{Bitmap: []byte{0}, Signature: proof.Signature},
		{Bitmap: []byte{1}, Signature: proof.Signature},
		{Bitmap: bytes.Repeat([]byte{1}, 9), Signature: proof.Signature},
		{Bitmap: proof.Bitmap, Signature: proof.Signature[:64]},
	} {
		_, err := FormatQuorumProof(d, bad)
		require.Error(t, err)
	}
	bad := authorization.Proof{Bitmap: proof.Bitmap, Signature: append([]byte(nil), proof.Signature...)}
	bad.Signature[64] = 0
	_, err := FormatQuorumProof(d, bad)
	require.Error(t, err)
	_, err = FormatAggregateSignature(d, proof.Signature)
	require.Error(t, err)
	_, err = SubmitMembership(d, nil, m.BLS(), big.NewInt(1), proof.Signature, nil)
	require.ErrorContains(t, err, "BLS membership API")
	_, err = SubmitILNGovernance(d, nil, protocol.ILNSourceFeeProposal{}, big.NewInt(1), proof.Signature)
	require.ErrorContains(t, err, "BLS governance API")
}
