package signer

import "github.com/xgr-network/xgr-node/crypto"

// LegacyCommitDigest returns the *exact* digest used by the legacy IBFT committed-seal scheme.
// Consensus-critical: must stay bit-identical to CreateCommittedSeal / VerifyParentCommittedSeals.
func LegacyCommitDigest(hash []byte) []byte {
	// Same as in signer.go: crypto.Keccak256(wrapCommitHash(hash))
	return crypto.Keccak256(wrapCommitHash(hash))
}
