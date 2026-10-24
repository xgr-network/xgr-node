// Package interchain is the immutable, release-reviewed XITA network policy.
// v3.3.2 pins only verified Direct-BLS deployments for XGRChain, Base and
// Arbitrum (XITA deployment inventory xgr-network/xgr-interchain @
// 8b4b078fda4edf5592b6f83510d14c0ef9a88830, 2026-10-24).
// Polygon remains deliberately unsupported until its separate reviewed release.
package interchain

import (
 "fmt"
 "strings"
)

type FinalityRule string

const (
 FinalityIBFT FinalityRule = "ibft-committed"
 FinalitySafe FinalityRule = "ethereum-safe"
)

// ChainPolicy MUST NOT contain validator-specific security settings.
// Addresses below are populated only by a reviewed light release.
// There is one active ValidatorRegistry per chain; token routes are permissionless.
// PRE-S03 (decision 2026-10-24): membership signatures do NOT bind the exact
// destination Registry contract address. Valid 2/3 quorum transitions could be
// replayed across separately deployed Registries sharing the same chain/domain,
// set ID and validator keys. This residual risk is explicitly accepted under
// the ONE-active-Registry-per-chain invariant; it is not a cryptographic fix.
// Only the receipt-pinned Registry in this policy and its associated ISM may be
// active. Old Registry deployments must not be configured concurrently.
// Leave membership wire encoding unchanged; reassess if policy changes.
type ChainPolicy struct {
 Name string
 ChainID uint64
 Domain uint32
 Finality FinalityRule
 VerifierFormat string
 RPCURL string
 Registry string
 SourceRegistry string
 SourceActivationBlock uint64
}

var compiledChains = []ChainPolicy{
 // Source activation = Factory-created SourceRegistry receipt.blockNumber,
 // NEVER Solidity's L1-origin block.number on an L2.
 // xgrchain receipts: f0156bb3... (ValidatorRegistry), 369ce335... (SourceRegistry).
 {Name:"xgr",ChainID:1643,Domain:1643,Finality:FinalityIBFT,
  VerifierFormat:"compressed",RPCURL:"https://rpc.xgr.network",
  Registry:"0x9b6b92ef1745611f402341965f7fc649cbe0da8c",
  SourceRegistry:"0xea887a7f2f519f291732aaadf578baf3ff374df6",
  SourceActivationBlock:12053797},
 // base receipts: c8b2547d... (ValidatorRegistry), da05fb98... (SourceRegistry).
 {Name:"base",ChainID:8453,Domain:8453,Finality:FinalitySafe,
  VerifierFormat:"eip2537",RPCURL:"https://mainnet.base.org",
  Registry:"0x3dc4cc1912792749d320789a67c2c14b7ee4ee33",
  SourceRegistry:"0xf243093350b1b4ef8a9e3b0c13be3495297461b0",
  SourceActivationBlock:53026521},
 // arbitrum receipts: 879889c2... (ValidatorRegistry), 278fb105... (SourceRegistry).
 {Name:"arbitrum",ChainID:42161,Domain:42161,Finality:FinalitySafe,
  VerifierFormat:"eip2537",RPCURL:"https://arb1.arbitrum.io/rpc",
  Registry:"0x1a6610cc7dd0d0f42e22c3608e025d891065814c",
  SourceRegistry:"0x51e3b684ed61fc88083497e475825a6cecaf21f5",
  SourceActivationBlock:517836903},
}

func validatePolicy(p ChainPolicy) error {
 if p.Name==""||p.ChainID==0||p.Domain==0||uint64(p.Domain)!=p.ChainID||
  p.RPCURL==""||(p.VerifierFormat!="compressed"&&p.VerifierFormat!="eip2537") {
  return fmt.Errorf("invalid compiled XITA chain identity: %q",p.Name)
 }
 switch p.ChainID {
 case 1643:
  if p.Name!="xgr"||p.Finality!=FinalityIBFT||p.VerifierFormat!="compressed" {
   return fmt.Errorf("inconsistent XGR IBFT finality policy")
  }
 case 8453:
  if p.Name!="base"||p.Finality!=FinalitySafe||p.VerifierFormat!="eip2537" {
   return fmt.Errorf("inconsistent Base finality policy")
  }
 case 42161:
  if p.Name!="arbitrum"||p.Finality!=FinalitySafe||p.VerifierFormat!="eip2537" {
   return fmt.Errorf("inconsistent Arbitrum finality policy")
  }
 default:
  return fmt.Errorf("unsupported chain %d: new finality adapter required",p.ChainID)
 }
 if (p.Registry=="")!=(p.SourceRegistry=="") {
  return fmt.Errorf("incomplete XITA deployment bindings for %s",p.Name)
 }
 if p.Registry==""&&p.SourceActivationBlock!=0 {
  return fmt.Errorf("unbound XITA registry activation height for %s",p.Name)
 }
 if p.Registry!=""&&p.SourceActivationBlock==0 {
  return fmt.Errorf("missing RPC-native deployment height for %s",p.Name)
 }
 return nil
}

// PolicyForChainID returns the release-reviewed chain identity and finality.
func PolicyForChainID(id uint64) (ChainPolicy,error) {
 for _,p:=range compiledChains {
  if p.ChainID==id{
   if err:=validatePolicy(p);err!=nil{return ChainPolicy{},err}
   return p,nil
  }
 }
 return ChainPolicy{},fmt.Errorf("unsupported chain %d",id)
}

func Policy(name string) (ChainPolicy,error) {
 name=strings.ToLower(strings.TrimSpace(name))
 if name=="xgrchain" {name="xgr"}
 for _,p:=range compiledChains {
  if p.Name==name {
   if err:=validatePolicy(p);err!=nil{return ChainPolicy{},err}
   return p,nil
  }
 }
 return ChainPolicy{},fmt.Errorf("unsupported XITA chain %q",name)
}

// ActivePolicies returns only fully receipt-pinned Direct-BLS deployments.
// v3.3.2 activates the three reviewed chains; Polygon has no binding.
// This config enables validator workers, NOT token routes or user transfers.
func ActivePolicies() ([]ChainPolicy,error) {
 out:=make([]ChainPolicy,0,len(compiledChains))
 for _,p:=range compiledChains {
  if err:=validatePolicy(p);err!=nil{return nil,err}
  if p.Registry!=""&&p.SourceRegistry!="" {out=append(out,p)}
 }
 return out,nil
}
