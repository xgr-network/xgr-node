// Package interchain is the immutable, release-reviewed XITA network policy.
// v3.3.2 contains no deployed contract bindings. v3.3.3 will pin receipt-
// verified addresses and RPC-native activation blocks after deployment.
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
 {Name:"xgr",ChainID:1643,Domain:1643,Finality:FinalityIBFT,
  VerifierFormat:"compressed",RPCURL:"https://rpc.xgr.network"},
 {Name:"base",ChainID:8453,Domain:8453,Finality:FinalitySafe,
  VerifierFormat:"eip2537",RPCURL:"https://mainnet.base.org"},
 {Name:"arbitrum",ChainID:42161,Domain:42161,Finality:FinalitySafe,
  VerifierFormat:"eip2537",RPCURL:"https://arb1.arbitrum.io/rpc"},
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

// ActivePolicies returns only entirely receipt-pinned deployments.
// In v3.3.2 this is deliberately EMPTY: no signatures until 3.3.3.
func ActivePolicies() ([]ChainPolicy,error) {
 out:=make([]ChainPolicy,0,len(compiledChains))
 for _,p:=range compiledChains {
  if err:=validatePolicy(p);err!=nil{return nil,err}
  if p.Registry!=""&&p.SourceRegistry!="" {out=append(out,p)}
 }
 return out,nil
}
