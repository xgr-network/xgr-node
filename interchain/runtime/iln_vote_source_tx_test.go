package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	"github.com/xgr-network/xgr-node/crypto"
	evm "github.com/xgr-network/xgr-node/interchain/evm"
	"github.com/xgr-network/xgr-node/types"
)

func TestILNStoredVoteCanRecoverOriginalSourceTx(t *testing.T) {
	worker := &Worker{dataDir: t.TempDir(), ilnLocalVotes: make(map[types.Hash]*ilnLocalVoteState)}
	vote := f20Vote(7, types.Address{1})
	var message protocol.XITADirectMessage
	require.NoError(t, message.UnmarshalBinary(vote.Payload))
	route := &evm.CheckpointRoute{Name: vote.Route}
	hash := crypto.Keccak256Hash(vote.Payload)
	require.NoError(t, worker.persistILNLocalVote(hash, vote))
	require.NoError(t, worker.recoverILNLocalVotes())

	request := requestedILNQuorum{
		Route: route.Name, MessageID: message.MessageID.String(), SourceBlockNumber: message.SourceBlockNumber,
		SourceTxHash: "0x" + strings.Repeat("a", 64),
	}
	path := filepath.Join(worker.ilnDir(), "quorum-requests", route.Name, request.MessageID+".json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o770))
	raw, err := json.Marshal(request)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o660))

	tx, err := worker.localILNVoteSourceTx(worker.ilnLocalVotes[hash].vote, route, message)
	require.NoError(t, err)
	require.Equal(t, request.SourceTxHash, tx.String())
	unmatched := message
	unmatched.SourceBlockNumber++
	tx, err = worker.localILNVoteSourceTx(vote, route, unmatched)
	require.NoError(t, err)
	require.Equal(t, types.ZeroHash, tx)

	vote.SourceTxHash = request.SourceTxHash
	require.NoError(t, worker.persistILNLocalVote(hash, vote))
	require.NoError(t, os.Remove(path))
	require.NoError(t, worker.recoverILNLocalVotes())
	recovered := worker.ilnLocalVotes[hash].vote
	tx, err = worker.localILNVoteSourceTx(recovered, route, message)
	require.NoError(t, err)
	require.Equal(t, request.SourceTxHash, tx.String())
	require.Equal(t, vote.Payload, recovered.Payload)
	require.Equal(t, vote.Signature, recovered.Signature)
}

func TestILNSourceTxLocatorRejectsMalformedWireHint(t *testing.T) {
	_, err := parseILNSourceTxLocator("0x" + strings.Repeat("0", 64))
	require.Error(t, err)
	_, err = parseILNSourceTxLocator("0x" + strings.Repeat("z", 64))
	require.Error(t, err)
	tx, err := parseILNSourceTxLocator("")
	require.NoError(t, err)
	require.Equal(t, types.ZeroHash, tx)
}
