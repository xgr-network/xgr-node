// Package interchain is the immutable, release-reviewed XITA network policy.
// v3.4.3 adds receipt-pinned OP Mainnet chain/domain 10.
// XGRChain, Base, Arbitrum and BSC preserve their existing bindings.
// Validator activation requires explicit controlled rollout; Polygon unsupported.
package interchain

import (
 "fmt"
 "net/url"
 "strings"
)

type FinalityRule string

const (
 FinalityIBFT FinalityRule = "ibft-committed"
 FinalitySafe FinalityRule = "ethereum-safe"
 FinalityFinalized FinalityRule = "ethereum-finalized"
)

// ZeroRegistryAddress represents an undeployed placeholder, NOT a valid
// production binding. Every compiled v3.4.1 chain must reject this address.
const ZeroRegistryAddress = "0x0000000000000000000000000000000000000000"

// hasDeployedRegistry deliberately excludes the zero-address placeholder.
// Invalid nonzero addresses are rejected by evm.makeDestination.
func hasDeployedRegistry(address string) bool {
 return address!=""&&!strings.EqualFold(address,ZeroRegistryAddress)
}

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
 RPCURLs []string
 // IndependentOperatorIDs aligns with RPCURLs. Multiple URLs operated by the
 // same entity count as ONE witness (transport redundancy only).
 IndependentOperatorIDs []string
 Registry string
 SourceRegistry string
 SourceActivationBlock uint64
}

var compiledChains = []ChainPolicy{
 // Source activation = Factory-created SourceRegistry receipt.blockNumber,
 // NEVER Solidity's L1-origin block.number on an L2.
 // xgrchain receipts: f0156bb3... (ValidatorRegistry), 369ce335... (SourceRegistry).
 {Name:"xgr",ChainID:1643,Domain:1643,Finality:FinalityIBFT,
  // XGR has NO external Interchain RPC policy. All read-only verification
  // of its committed IBFT chain is performed directly by the local validator.
  VerifierFormat:"compressed",
  Registry:"0x9b6b92ef1745611f402341965f7fc649cbe0da8c",
  SourceRegistry:"0xea887a7f2f519f291732aaadf578baf3ff374df6",
  SourceActivationBlock:12053797},
 // base receipts: c8b2547d... (ValidatorRegistry), da05fb98... (SourceRegistry).
 {Name:"base",ChainID:8453,Domain:8453,Finality:FinalitySafe,
  VerifierFormat:"eip2537",RPCURLs:[]string{"https://mainnet.base.org","https://gateway.tenderly.co/public/base","https://base-rpc.publicnode.com","https://base.drpc.org"},
  IndependentOperatorIDs:[]string{"base-official","tenderly","publicnode","drpc"},
  Registry:"0x3dc4cc1912792749d320789a67c2c14b7ee4ee33",
  SourceRegistry:"0xf243093350b1b4ef8a9e3b0c13be3495297461b0",
  SourceActivationBlock:53026521},
 // arbitrum receipts: 879889c2... (ValidatorRegistry), 278fb105... (SourceRegistry).
 {Name:"arbitrum",ChainID:42161,Domain:42161,Finality:FinalitySafe,
  VerifierFormat:"eip2537",RPCURLs:[]string{"https://arb1.arbitrum.io/rpc","https://arbitrum-one-rpc.publicnode.com","https://arbitrum.gateway.tenderly.co","https://arbitrum.drpc.org"},
  IndependentOperatorIDs:[]string{"arbitrum-official","publicnode","tenderly","drpc"},
  Registry:"0x1a6610cc7dd0d0f42e22c3608e025d891065814c",
  SourceRegistry:"0x51e3b684ed61fc88083497e475825a6cecaf21f5",
  SourceActivationBlock:517836903},
 // BSC deployed on 2026-10-29. Registry receipt d7258302...,
 // SourceRegistry receipt e1b3a5c1... at RPC-native block 130525341.
 // RPC witness independence, finalized-head and log-history checks must still
 // pass live production preflight before validator activation.
 {Name:"bsc",ChainID:56,Domain:56,Finality:FinalityFinalized,
  VerifierFormat:"eip2537",
  RPCURLs:[]string{"https://bsc-rpc.publicnode.com","https://bsc.drpc.org","https://bnb.api.onfinality.io/public"},
  IndependentOperatorIDs:[]string{"publicnode","drpc","onfinality"},
  Registry:"0x70f5752326735b31641f21d174ba035e904db93c",
  SourceRegistry:"0x14d5d33a65a39f96665ed631b04fecf357bcb576",
  SourceActivationBlock:130525341},
 // OP Mainnet: verified receipt bindings; RPC-native L2 source activation.
 {Name:"optimism",ChainID:10,Domain:10,Finality:FinalitySafe,
  VerifierFormat:"eip2537",
  RPCURLs:[]string{"https://mainnet.optimism.io","https://optimism-rpc.publicnode.com","https://optimism.drpc.org","https://optimism.gateway.tenderly.co"},
  IndependentOperatorIDs:[]string{"optimism-official","publicnode","drpc","tenderly"},
  Registry:"0x70f5752326735b31641f21d174ba035e904db93c",
  SourceRegistry:"0x14d5d33a65a39f96665ed631b04fecf357bcb576",
  SourceActivationBlock:158929283},
}

func validatePolicy(p ChainPolicy) error {
 if p.Name==""||p.ChainID==0||p.Domain==0||uint64(p.Domain)!=p.ChainID||
  (p.VerifierFormat!="compressed"&&p.VerifierFormat!="eip2537") {
  return fmt.Errorf("invalid compiled XITA chain identity: %q",p.Name)
 }
 // XGR's native verified data source deliberately has no RPC addresses.
 // All external chains retain 3+ endpoints and two independent operators.
 if p.ChainID==1643 {
  if len(p.RPCURLs)!=0||len(p.IndependentOperatorIDs)!=0 {
   return fmt.Errorf("XGR native IBFT mode must not contain external RPC endpoints")
  }
 }else if len(p.RPCURLs)<3||len(p.IndependentOperatorIDs)!=len(p.RPCURLs){
  return fmt.Errorf("invalid compiled XITA chain identity: %q",p.Name)
 }
 seen:=make(map[string]bool,len(p.RPCURLs))
 operators:=make(map[string]struct{})
 for i,endpoint:=range p.RPCURLs{
  operator:=p.IndependentOperatorIDs[i]
  words:=strings.Fields(operator)
  if operator==""||len(words)!=1||words[0]!=operator||strings.ContainsAny(operator,"/"){
   return fmt.Errorf("invalid Interchain RPC operator identity on %s",p.Name)
  }
  operators[operator]=struct{}{}
  parsed,err:=url.Parse(endpoint)
  if err!=nil||parsed.Scheme!="https"||parsed.Host==""||
   parsed.User!=nil||parsed.Fragment!=""||seen[endpoint] {
   return fmt.Errorf("invalid or repeated compiled XITA RPC endpoint for %s",p.Name)
  }
  seen[endpoint]=true
 }
 // On external chains, independent operators are mandatory; on XGR there
 // are no remote operators because each validator reads its own IBFT chain.
 if len(operators)<2 && p.ChainID!=1643 {
  return fmt.Errorf("at least two independent Interchain RPC operators required for %s",p.Name)
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
 case 56:
  if p.Name!="bsc"||p.Finality!=FinalityFinalized||p.VerifierFormat!="eip2537" {
   return fmt.Errorf("inconsistent BSC finality policy")
  }
 case 10:
  if p.Name!="optimism"||p.Finality!=FinalitySafe||p.VerifierFormat!="eip2537" {
   return fmt.Errorf("inconsistent OP Mainnet finality policy")
  }
 default:
  return fmt.Errorf("unsupported chain %d: new finality adapter required",p.ChainID)
 }
 // A compiled release with documented deployments never permits zero
 // placeholders, even on BSC. A partial or accidentally erased binding
 // must fail closed instead of silently deactivating a validator worker.
 if strings.EqualFold(p.Registry,ZeroRegistryAddress)||
  strings.EqualFold(p.SourceRegistry,ZeroRegistryAddress){
  return fmt.Errorf("zero-address Registry placeholder is not permitted on %s",p.Name)
 }
 registryBound:=hasDeployedRegistry(p.Registry)
 sourceBound:=hasDeployedRegistry(p.SourceRegistry)
 if registryBound!=sourceBound {
  return fmt.Errorf("incomplete XITA deployment bindings for %s",p.Name)
 }
 if !registryBound {
  return fmt.Errorf("missing reviewed deployment bindings for %s",p.Name)
 }
 if !registryBound&&p.SourceActivationBlock!=0 {
  return fmt.Errorf("unbound XITA registry activation height for %s",p.Name)
 }
 if registryBound&&p.SourceActivationBlock==0 {
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
// v3.4.3 includes OP Mainnet alongside the four receipt-pinned networks.
// Each validator still requires explicit operational rollout.
// This config enables validator workers, NOT token routes or user transfers.
func ActivePolicies() ([]ChainPolicy,error) {
 out:=make([]ChainPolicy,0,len(compiledChains))
 for _,p:=range compiledChains {
  if err:=validatePolicy(p);err!=nil{return nil,err}
  if hasDeployedRegistry(p.Registry)&&hasDeployedRegistry(p.SourceRegistry) {
   out=append(out,p)
  }
 }
 return out,nil
}
