package runtime

import (
    "encoding/json"
    "testing"

    "github.com/stretchr/testify/require"
)

func TestSourceFeeExecutionResultRoundTrip(t *testing.T) {
    in := localGovernanceResult{
        Done: true, ProposalID: "0x1234", Quorum: true,
        TxHash: "0xdeadbeef", Nonce: 7,
    }
    data, err := json.Marshal(in)
    require.NoError(t, err)
    var out localGovernanceResult
    require.NoError(t, json.Unmarshal(data, &out))
    require.Equal(t, in, out)
}
