package evm

import (
 "fmt"
 "net/url"
 "strings"

 policy "github.com/xgr-network/xgr-node/interchain"
 "github.com/xgr-network/xgr-node/types"
)

const (
 VerifierFormatEIP2537 = "eip2537"
 VerifierFormatCompressed = "compressed"
)

type Destination struct {
 Name string
 ChainID uint64
 Domain uint32
 RegistryAddress string
 ILNRegistryAddress string
 ILNRegistryActivationBlock uint64
 RPCURL string
 RPCURLs []string
 IndependentOperatorIDs []string
 // FinalityRule is copied from the immutable release policy. Runtime checks
 // always re-resolve the rule by ChainID, never from this mutable field.
 FinalityRule policy.FinalityRule
 MembershipValiditySeconds uint64
 ExecutorStepDelaySeconds uint64
 VerifierFormat string
 NativeXGR NativeXGRAccess
}

func makeDestination(p policy.ChainPolicy) (*Destination,error) {
 d:=&Destination{
  Name:p.Name,ChainID:p.ChainID,Domain:p.Domain,
  RegistryAddress:p.Registry,ILNRegistryAddress:p.SourceRegistry,
  ILNRegistryActivationBlock:p.SourceActivationBlock,
  RPCURLs:append([]string(nil),p.RPCURLs...),
  IndependentOperatorIDs:append([]string(nil),p.IndependentOperatorIDs...),
  FinalityRule:p.Finality,VerifierFormat:p.VerifierFormat,
  MembershipValiditySeconds:300,ExecutorStepDelaySeconds:10,
 }
 if len(d.RPCURLs)>0{d.RPCURL=d.RPCURLs[0]}
 for _,endpoint:=range d.RPCURLs{
  parsed,err:=url.Parse(endpoint)
  if err!=nil||parsed.Scheme!="https"||parsed.Host==""{
   return nil,fmt.Errorf("invalid compiled RPC URL for %s",d.Name)
  }
 }
 for _,v:=range []string{d.RegistryAddress,d.ILNRegistryAddress}{
  if v==""{continue}
  if err:=types.IsValidAddress(v);err!=nil||types.StringToAddress(v)==types.ZeroAddress {
   return nil,fmt.Errorf("invalid compiled XITA contract address for %s",d.Name)
  }
 }
 return d,nil
}

// Load reads no ENV: it may describe an undeployed chain but cannot enable
// signing without the receipt-pinned contract bindings in v3.3.2.
func Load(name string) (*Destination,error) {
 p,err:=policy.Policy(name)
 if err!=nil{return nil,err}
 return makeDestination(p)
}

// LoadAll is the ONLY active worker/RPC destination source.
func LoadAll() ([]*Destination,error) {
 ps,err:=policy.ActivePolicies()
 if err!=nil{return nil,err}
 out:=make([]*Destination,0,len(ps))
 for _,p:=range ps{
  d,err:=makeDestination(p)
  if err!=nil{return nil,err}
  out=append(out,d)
 }
 return out,nil
}

func (d *Destination) ValidateMembershipRead() error {
 if d==nil||d.ChainID==0||d.Domain==0||strings.TrimSpace(d.RegistryAddress)==""||
  (d.ChainID!=1643&&strings.TrimSpace(d.RPCURL)==""){
  return fmt.Errorf("verified destination Registry deployment required")
 }
 if d.VerifierFormat!=VerifierFormatEIP2537&&d.VerifierFormat!=VerifierFormatCompressed{
  return fmt.Errorf("invalid destination verifier format")
 }
 return nil
}

func (d *Destination) ValidateSubmission() error {
 if err:=d.ValidateMembershipRead();err!=nil{return err}
 if d.MembershipValiditySeconds==0||d.MembershipValiditySeconds>600||
  d.ExecutorStepDelaySeconds==0||d.ExecutorStepDelaySeconds>=d.MembershipValiditySeconds{
  return fmt.Errorf("invalid compiled membership timing")
 }
 return nil
}
func (d *Destination) ValidateActivation() error {return d.ValidateSubmission()}
