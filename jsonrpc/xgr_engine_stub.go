//go:build !engine_embedded

package jsonrpc

import (
	"fmt"

	"github.com/hashicorp/go-hclog"
	xgrsvc "github.com/xgr-network/xgr-node/jsonrpc/xgr"
)

func newXGREndpointEmbedded(hclog.Logger, string, string, []byte) (*xgrsvc.XGR, error) {
	return nil, fmt.Errorf("engine.mode=embedded requires build with -tags engine_embedded")
}
