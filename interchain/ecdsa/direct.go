// Package ecdsa provides an isolated, non-signing XITA secp256k1
// authorization codec and quorum verifier. Production worker signing is
// intentionally NOT wired until canonical XDC mainnet source/destination
// evidence, active registry and RPC finality checks are independently proven.
package ecdsa

import (
 "bytes"
 "errors"
 "math/big"

 protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
 "github.com/xgr-network/xgr-node/crypto"
 "github.com/xgr-network/xgr-node/types"
)

const Domain = "XITA_DIRECT_MESSAGE_ECDSA_V1"
const MaxValidators = 64
const SignatureSize = 65

var ErrInvalidAuthorization = errors.New("invalid XITA direct ECDSA authorization")

// EncodeDirect preserves the entire existing canonical 268-byte message tail,
// substituting a DIFFERENT signature-scheme domain. It cannot modify the
// BLS wire format, signer, or destination registry.
func EncodeDirect(message protocol.XITADirectMessage) ([]byte,error) {
 bls,err:=message.MarshalBinary()
 if err!=nil{return nil,err}
 if !bytes.HasPrefix(bls,[]byte(protocol.XITADirectMessageDomain)) ||
   len(bls)!=len(protocol.XITADirectMessageDomain)+268 {
  return nil,ErrInvalidAuthorization
 }
 raw:=make([]byte,0,len(Domain)+268)
 raw=append(raw,[]byte(Domain)...)
 raw=append(raw,bls[len(protocol.XITADirectMessageDomain):]...)
 return raw,nil
}

func Digest(raw []byte) (types.Hash,error) {
 if len(raw)!=len(Domain)+268 || !bytes.HasPrefix(raw,[]byte(Domain)) {
  return types.ZeroHash,ErrInvalidAuthorization
 }
 return crypto.Keccak256Hash(raw),nil
}

// VerifyQuorum checks packed 65-byte r,s,v signatures in ascending bitmap
// index order (v must be 27/28). Validators must come from the separately
// verified, current chain-local DESTINATION registry snapshot.
func VerifyQuorum(raw []byte, validators []types.Address, bitmap,sigs []byte) bool {
 digest,err:=Digest(raw)
 if err!=nil{return false}
 n:=len(validators)
 if n==0||n>MaxValidators||len(bitmap)!=(n+7)/8{return false}
 count:=0
 for i:=0;i<len(bitmap)*8;i++{
  if bitmap[i/8]&(byte(1)<<uint(i%8))==0{continue}
  if i>=n{return false}
  count++
 }
 if count<(2*n+2)/3||len(sigs)!=count*SignatureSize{return false}
 offset:=0
 for i,expect:=range validators{
  if bitmap[i/8]&(byte(1)<<uint(i%8))==0{continue}
  if expect==types.ZeroAddress{return false}
  sig:=append([]byte(nil),sigs[offset:offset+SignatureSize]...)
  if sig[64]!=27&&sig[64]!=28{return false}
  r:=new(big.Int).SetBytes(sig[:32])
  s:=new(big.Int).SetBytes(sig[32:64])
  if !crypto.ValidateSignatureValues(big.NewInt(int64(sig[64]-27)),r,s,true){return false}
  sig[64]-=27 // node recovery expects 0/1, not Solidity 27/28
  pub,err:=crypto.RecoverPubkey(sig,digest.Bytes())
  if err!=nil||crypto.PubKeyToAddress(pub)!=expect{return false}
  offset+=SignatureSize
 }
 return offset==len(sigs)
}
