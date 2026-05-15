package poolconfig

import (
	"bytes"
	"fmt"

	"github.com/xgr-network/xgr-node/command/helper"
)

type Result struct {
	Validator            string `json:"validator"`
	Enabled              bool   `json:"enabled"`
	MaxTotalDelegatedWei string `json:"maxTotalDelegatedWei"`
	MinDelegatorWei      string `json:"minDelegatorWei"`
	CommissionBps        uint64 `json:"commissionBps"`
}

func (r *Result) GetOutput() string {
	var b bytes.Buffer
	b.WriteString("\n[IBFT POOL-CONFIG]\n")
	b.WriteString(helper.FormatKV([]string{
		fmt.Sprintf("Validator|%s", r.Validator),
		fmt.Sprintf("Enabled|%t", r.Enabled),
		fmt.Sprintf("Max total delegated (wei)|%s", r.MaxTotalDelegatedWei),
		fmt.Sprintf("Min delegator stake (wei)|%s", r.MinDelegatorWei),
		fmt.Sprintf("Commission (bps)|%d", r.CommissionBps),
	}))
	return b.String()
}
