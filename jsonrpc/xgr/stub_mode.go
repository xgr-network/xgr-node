//go:build !engine_embedded

package xgr

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/hashicorp/go-hclog"
	"github.com/xgr-network/xgr-node/contracts"
)

var errEmbeddedUnavailable = fmt.Errorf("engine.mode=embedded requires a build with -tags engine_embedded")
var errEngineDisabled = fmt.Errorf("xgr engine is disabled (stub mode)")

type XGR struct {
	logger hclog.Logger
}

type Config struct {
	Logger      hclog.Logger
	EthRPCURL   string
	EngineEOA   string
	EnginePub33 []byte
	Sessions    any
}

func New(cfg Config) *XGR {
	return &XGR{logger: cfg.Logger}
}

func LoadEngineConfigFlex(any) (string, []byte, error) {
	return "", nil, errEmbeddedUnavailable
}

func EmbeddedAvailable() bool { return false }

type publicSaleResp struct {
	PublicSale string `json:"publicSale"`
}

type coreAddrsResp struct {
	Grants     string `json:"grants"`
	PublicSale string `json:"publicSale"`
	Precompile string `json:"precompile"`
	ChainID    string `json:"chainId"`
}

type getNextPidReq struct {
	Owner string `json:"owner"`
}
type getNextPidRes struct {
	Owner string `json:"owner"`
	Next  string `json:"next"`
}

func (x *XGR) GetPublicSale(context.Context) (*publicSaleResp, error) {
	return &publicSaleResp{PublicSale: strings.ToLower(strings.TrimSpace(os.Getenv("XGR_PUBLIC_SALE")))}, nil
}

func (x *XGR) GetCoreAddrs(context.Context) (*coreAddrsResp, error) {
	return &coreAddrsResp{
		Grants:     strings.ToLower(strings.TrimSpace(os.Getenv("XGR_GRANTS_ADDR"))),
		PublicSale: strings.ToLower(strings.TrimSpace(os.Getenv("XGR_PUBLIC_SALE"))),
		Precompile: contracts.EngineExecutePrecompile.String(),
		ChainID:    "",
	}, nil
}

func (x *XGR) GetNextProcessId(req getNextPidReq) (*getNextPidRes, error) {
	return &getNextPidRes{Owner: strings.ToLower(strings.TrimSpace(req.Owner)), Next: "0x1"}, nil
}

// Engine-backed calls are intentionally stubbed in public builds.
func (x *XGR) ValidateDataTransfer(any) (any, error) { return nil, errEngineDisabled }
func (x *XGR) GetCirculatingSupply(any) (any, error) { return nil, errEngineDisabled }
func (x *XGR) EstimateRuleGas(any) (any, error)      { return nil, errEngineDisabled }
func (x *XGR) WakeUpProcess(any) (any, error)        { return nil, errEngineDisabled }
func (x *XGR) Control(any) (any, error)              { return nil, errEngineDisabled }
func (x *XGR) ListSessions(any) (any, error)         { return nil, errEngineDisabled }
func (x *XGR) StepExecuted(any, any, any) (any, error) {
	return nil, errEngineDisabled
}
func (x *XGR) SessionAlive(any, any) (any, error)   { return nil, errEngineDisabled }
func (x *XGR) ManageGrants(any) (any, error)        { return nil, errEngineDisabled }
func (x *XGR) ListGrants(any) (any, error)          { return nil, errEngineDisabled }
func (x *XGR) GetGrantFeePerYear() (any, error)     { return nil, errEngineDisabled }
func (x *XGR) GetXRC137Meta(any) (any, error)       { return nil, errEngineDisabled }
func (x *XGR) GetEncryptedLogInfo(any) (any, error) { return nil, errEngineDisabled }
func (x *XGR) EncryptXRC137(any) (any, error)       { return nil, errEngineDisabled }
