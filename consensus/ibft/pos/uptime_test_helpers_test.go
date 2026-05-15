package pos

import (
	"github.com/hashicorp/go-hclog"
	"github.com/xgr-network/xgr-node/consensus/ibft/signer"
	xcrypto "github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

// deriveLastProposer is test helper that reconstructs previous proposer context.
func deriveLastProposer(set validators.Validators, round uint64, proposer types.Address) types.Address {
	if set == nil || set.Len() == 0 {
		return types.ZeroAddress
	}

	idx := set.Index(proposer)
	if idx == -1 {
		return types.ZeroAddress
	}

	l := uint64(set.Len())
	lastIdx := (uint64(idx) + l - ((round+1)%l)) % l

	return set.At(lastIdx).Addr()
}

// collectSignedAddresses is a test helper for seal/bitmap signer extraction.
func collectSignedAddresses(extra *signer.IstanbulExtra, blockHash types.Hash, _ hclog.Logger) map[types.Address]struct{} {
	seen := make(map[types.Address]struct{})
	if extra == nil || extra.Validators == nil || extra.ParentCommittedSeals == nil {
		return seen
	}

	switch seals := extra.ParentCommittedSeals.(type) {
	case *signer.AggregatedSeal:
		if seals.Bitmap == nil {
			return seen
		}

		for i := 0; i < extra.Validators.Len(); i++ {
			if seals.Bitmap.Bit(i) == 0 {
				continue
			}

			seen[extra.Validators.At(uint64(i)).Addr()] = struct{}{}
		}
	case *signer.SerializedSeal:
		digest := signer.LegacyCommitDigest(blockHash.Bytes())

		for _, sig := range *seals {
			pub, err := xcrypto.RecoverPubkey(sig, digest)
			if err != nil {
				continue
			}

			seen[xcrypto.PubKeyToAddress(pub)] = struct{}{}
		}
	}

	return seen
}

// shouldSkipUptimeAccounting mirrors old guard behavior used by dedicated tests.
func shouldSkipUptimeAccounting(extra *signer.IstanbulExtra, _ hclog.Logger, signed map[types.Address]struct{}) bool {
	if extra == nil || extra.ParentCommittedSeals == nil {
		return true
	}

	switch seals := extra.ParentCommittedSeals.(type) {
	case *signer.AggregatedSeal:
		if seals.Bitmap != nil && seals.Bitmap.Sign() > 0 {
			if extra.Validators == nil || extra.Validators.Len() == 0 {
				return true
			}

			return len(signed) == 0
		}
	}

	return false
}
