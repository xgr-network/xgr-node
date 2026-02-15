package ethrpc

import (
	"context"
	"encoding/hex"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/xgr-network/xgr-node/types"
)

// EthCall führt einen eth_call mit Background-Context aus.
func EthCall(ethRPC string, to types.Address, data []byte) ([]byte, error) {
	return EthCallCtx(context.Background(), ethRPC, to, data)
}

// EthCallCtx führt einen eth_call mit controllbarem Context aus.
func EthCallCtx(ctx context.Context, ethRPC string, to types.Address, data []byte) ([]byte, error) {
	cl, err := rpc.DialContext(ctx, ethRPC)
	if err != nil {
		return nil, err
	}
	defer cl.Close()
	msg := map[string]string{
		"to":   to.String(),
		"data": "0x" + hex.EncodeToString(data),
	}
	var out hexutil.Bytes
	if err := cl.CallContext(ctx, &out, "eth_call", msg, "latest"); err != nil {
		return nil, err
	}
	return out, nil
}
