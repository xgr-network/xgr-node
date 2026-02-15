//go:build engine_embedded

package xgr

import xgrext "github.com/xgr-network/xgrEngine/jsonrpc/xgr"

type XGR = xgrext.XGR
type Config = xgrext.Config

var New = xgrext.New
var LoadEngineConfigFlex = xgrext.LoadEngineConfigFlex

func EmbeddedAvailable() bool { return true }
