package evm

import (
    "bytes"
    "fmt"
    "reflect"

    "github.com/xgr-network/xgr-node/types"
)

// directILNEvidenceReader reads and verifies a complete source operation from
// ONE RPC endpoint. Test injection does not change the production trust model.
type directILNEvidenceReader func(
    *Destination, *CheckpointRoute, uint32, types.Hash, uint64,
) (*ILNOperation, []byte, *ILNRouteSnapshot, error)

// GetQuorumVerifiedDirectILNOperation requires identical finality-qualified
// evidence from two distinct RPC OPERATORS before a Direct-BLS signature.
// Several endpoints of one operator are failovers, not multiple witnesses.
// This mitigates a single malicious RPC, not collusion or L1 settlement risk.
func GetQuorumVerifiedDirectILNOperation(
    source *Destination, route *CheckpointRoute,
    destinationDomain uint32, messageID types.Hash, sourceBlock uint64,
) (*ILNOperation, []byte, *ILNRouteSnapshot, error) {
    return quorumVerifiedDirectILNOperation(
        source, route, destinationDomain, messageID, sourceBlock,
        GetDirectILNOperation,
    )
}

func quorumVerifiedDirectILNOperation(
    source *Destination, route *CheckpointRoute,
    destinationDomain uint32, messageID types.Hash, sourceBlock uint64,
    reader directILNEvidenceReader,
) (*ILNOperation, []byte, *ILNRouteSnapshot, error) {
    if source == nil || reader == nil {
        return nil, nil, nil, fmt.Errorf("missing source or Direct-BLS evidence reader")
    }
    urls := rpcEndpoints(source)
    if len(urls) < 2 || len(source.IndependentOperatorIDs) != len(urls) {
        return nil, nil, nil, fmt.Errorf("source RPC quorum requires independent operators")
    }

    type observation struct {
        operation *ILNOperation
        message []byte
        snapshot *ILNRouteSnapshot
        operator string
    }
    observations := make([]observation, 0, len(urls))
    successfulOperators := make(map[string]bool, len(urls))

    for i, endpoint := range urls {
        operator := source.IndependentOperatorIDs[i]
        if operator == "" || successfulOperators[operator] {
            continue
        }
        // Bind the ENTIRE receipt/route/FeeVault verification to this one
        // endpoint. Never combine parts of unrelated RPC responses.
        one := *source
        one.RPCURL = endpoint
        one.RPCURLs = []string{endpoint}
        one.IndependentOperatorIDs = []string{operator}
        operation, message, snapshot, err := reader(
            &one, route, destinationDomain, messageID, sourceBlock,
        )
        if err != nil || operation == nil || snapshot == nil ||
           operation.CanonicalBlockHash == types.ZeroHash {
            continue // Try the next endpoint, including same-operator fallback.
        }
        successfulOperators[operator] = true

        for _, previous := range observations {
            if previous.operator != operator &&
                sameILNWitness(
                    previous.operation, previous.message, previous.snapshot,
                    operation, message, snapshot,
                ) {
                return operation, message, snapshot, nil
            }
        }
        observations = append(observations, observation{
            operation: operation, message: message, snapshot: snapshot,
            operator: operator,
        })
    }
    return nil, nil, nil, fmt.Errorf(
        "independent source RPC quorum not reached: two matching operators required",
    )
}

// Exact source-block hash and all relevant receipt/route/fee fields must agree
// across operators, even if their most recent safe heads have different heights.
func sameILNWitness(
    a *ILNOperation, am []byte, as *ILNRouteSnapshot,
    b *ILNOperation, bm []byte, bs *ILNRouteSnapshot,
) bool {
    if a == nil || b == nil || as == nil || bs == nil {
        return false
    }
    if a.CanonicalBlockHash == types.ZeroHash || b.CanonicalBlockHash == types.ZeroHash {
        return false
    }
    return a.CanonicalBlockHash == b.CanonicalBlockHash &&
        a.TransactionHash == b.TransactionHash &&
        a.LogIndex == b.LogIndex &&
        a.MessageID == b.MessageID &&
        a.RouteID == b.RouteID &&
        a.BlockNumber == b.BlockNumber &&
        a.DestinationDomain == b.DestinationDomain &&
        a.ValidatorFeeWei != nil && b.ValidatorFeeWei != nil &&
        a.ValidatorFeeWei.Cmp(b.ValidatorFeeWei) == 0 &&
        bytes.Equal(am, bm) && reflect.DeepEqual(as, bs)
}
