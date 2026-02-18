//go:build !engine_embedded

package xgr

import (
	"context"
	"fmt"
)

var errEngineUnavailable = fmt.Errorf("embedded xgrEngine is unavailable in the stub module")

type XGR struct{}

type Config struct {
	Logger      any
	EthRPCURL   string
	EngineEOA   string
	EnginePub33 []byte
	Sessions    any
}

func New(Config) *XGR { return &XGR{} }

func LoadEngineConfigFlex(any) (string, []byte, error) {
	return "", nil, errEngineUnavailable
}

func (x *XGR) GetPublicSale(context.Context) (any, error)       { return nil, errEngineUnavailable }
func (x *XGR) GetCoreAddrs(context.Context) (any, error)        { return nil, errEngineUnavailable }
func (x *XGR) GetNextProcessId(any) (any, error)                { return nil, errEngineUnavailable }
func (x *XGR) ValidateDataTransfer(any) (any, error)            { return nil, errEngineUnavailable }
func (x *XGR) GetCirculatingSupply(any) (any, error)            { return nil, errEngineUnavailable }
func (x *XGR) EstimateRuleGas(any) (any, error)                 { return nil, errEngineUnavailable }
func (x *XGR) WakeUpProcess(any) (any, error)                   { return nil, errEngineUnavailable }
func (x *XGR) Control(any) (any, error)                         { return nil, errEngineUnavailable }
func (x *XGR) ListSessions(any) (any, error)                    { return nil, errEngineUnavailable }
func (x *XGR) StepExecuted(any, any, any) (any, error)          { return nil, errEngineUnavailable }
func (x *XGR) SessionAlive(any, any) (any, error)               { return nil, errEngineUnavailable }
func (x *XGR) ManageGrants(any) (any, error)                    { return nil, errEngineUnavailable }
func (x *XGR) ListGrants(any) (any, error)                      { return nil, errEngineUnavailable }
func (x *XGR) GetGrantFeePerYear() (any, error)                 { return nil, errEngineUnavailable }
func (x *XGR) GetXRC137Meta(any) (any, error)                   { return nil, errEngineUnavailable }
func (x *XGR) GetEncryptedLogInfo(any) (any, error)             { return nil, errEngineUnavailable }
func (x *XGR) EncryptXRC137(any) (any, error)                   { return nil, errEngineUnavailable }
func (x *XGR) RegisterAPIs(any, any)                            {}
