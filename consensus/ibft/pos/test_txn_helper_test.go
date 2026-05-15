package pos

import "github.com/xgr-network/xgr-node/types"

type memUptimeTxn struct {
	m map[types.Hash]types.Hash
}

func (m *memUptimeTxn) SetState(_ types.Address, k types.Hash, v types.Hash) { m.m[k] = v }
func (m *memUptimeTxn) GetState(_ types.Address, k types.Hash) types.Hash    { return m.m[k] }
