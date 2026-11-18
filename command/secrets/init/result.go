package init

import (
	"bytes"
	"fmt"

	"github.com/xgr-network/xgr-node/command/helper"
	"github.com/xgr-network/xgr-node/types"
)

type SecretsInitResult struct {
	Address   types.Address `json:"address"`
	InterchainECDSASigner types.Address `json:"interchain_ecdsa_signer"`
	BLSPubkey string        `json:"bls_pubkey"`
	NodeID    string        `json:"node_id"`
	Insecure  bool          `json:"insecure"`
}

func (r *SecretsInitResult) GetOutput() string {
	var buffer bytes.Buffer

	vals := make([]string, 0, 3)

	vals = append(
		vals,
		fmt.Sprintf("Public key (address)|%s", r.Address.String()),
	)

	if r.BLSPubkey != "" {
		vals = append(
			vals,
			fmt.Sprintf("BLS Public key|%s", r.BLSPubkey),
		)
	}

	if r.InterchainECDSASigner != types.ZeroAddress {
		vals = append(vals,fmt.Sprintf("XITA interchain ECDSA signer|%s",r.InterchainECDSASigner.String()))
	}
	vals = append(vals, fmt.Sprintf("Node ID|%s", r.NodeID))

	if r.Insecure {
		buffer.WriteString("\n[WARNING: INSECURE LOCAL SECRETS - SHOULD NOT BE RUN IN PRODUCTION]\n")
	}

	buffer.WriteString("\n[SECRETS INIT]\n")
	buffer.WriteString(helper.FormatKV(vals))
	buffer.WriteString("\n")

	return buffer.String()
}
