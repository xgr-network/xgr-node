package jsonrpc

// Public, read-only bootstrap evidence collected through validator P2P.
// The endpoint never signs, deploys, requests quorum or accesses secret keys.
import (
 "bytes"
 "encoding/hex"
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "strconv"
 "strings"

 protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
 "github.com/xgr-network/xgr-node/crypto"
 "github.com/xgr-network/xgr-node/types"
)

type ilnBootstrapPublicProofRPC struct {
 Validator string `json:"validator"`
 OriginChainID uint64 `json:"originChainId"`
 DestinationDomain uint32 `json:"destinationDomain"`
 BLSPublicKey string `json:"blsPublicKey"`
 BLSPublicKeyEIP2537 string `json:"blsPublicKeyEIP2537"`
 Payload string `json:"payload"`
 PossessionProof string `json:"possessionProof"`
 CompressedProof string `json:"compressedProof"`
}
type ilnBootstrapProofsRPC struct {
 OriginChainID uint64 `json:"originChainId"`
 DestinationDomain uint32 `json:"destinationDomain"`
 Proofs []ilnBootstrapPublicProofRPC `json:"proofs"`
}
func rpcBootstrapHex(s string,size int)([]byte,error){
 if len(s)!=size*2+2||!strings.HasPrefix(s,"0x"){return nil,fmt.Errorf("invalid bootstrap field")}
 b,e:=hex.DecodeString(s[2:])
 if e!=nil||len(b)!=size{return nil,fmt.Errorf("invalid bootstrap hex")}
 return b,nil
}
func verifyRPCBootstrapPublicProof(p ilnBootstrapPublicProofRPC,domain uint32)error{
 if domain==0||p.DestinationDomain!=domain||p.OriginChainID!=1643{
  return fmt.Errorf("bootstrap evidence wrong chain or domain")
 }
 addrRaw,err:=rpcBootstrapHex(p.Validator,20)
 if err!=nil{return err}
 addr:=types.BytesToAddress(addrRaw)
 if addr==types.ZeroAddress{return fmt.Errorf("zero validator")}
 pub,err:=rpcBootstrapHex(p.BLSPublicKey,48)
 if err!=nil{return err}
 eipPub,err:=rpcBootstrapHex(p.BLSPublicKeyEIP2537,128)
 if err!=nil{return err}
 sig,err:=rpcBootstrapHex(p.CompressedProof,96)
 if err!=nil{return err}
 eipSig,err:=rpcBootstrapHex(p.PossessionProof,256)
 if err!=nil{return err}
 payload,err:=protocol.MarshalBootstrapPayload(1643,domain,addr,pub,eipPub)
 if err!=nil{return err}
 raw,err:=rpcBootstrapHex(p.Payload,len(payload))
 if err!=nil||!bytes.Equal(payload,raw){return fmt.Errorf("bootstrap payload mismatch")}
 expected,err:=crypto.BLSSignatureToEIP2537(sig)
 if err!=nil||!bytes.Equal(expected,eipSig){return fmt.Errorf("bootstrap proof wire mismatch")}
 if err:=crypto.VerifyBLSSignatureFromBytes(pub,sig,payload);err!=nil{return err}
 return nil
}

func (x *xgrEndpoint) GetILNBootstrapProofs(destinationDomain uint64)(*ilnBootstrapProofsRPC,error){
 if x==nil||x.dataDir==""||destinationDomain==0||destinationDomain>4294967295 {
  return nil,fmt.Errorf("invalid public bootstrap target domain")
 }
 domain:=uint32(destinationDomain)
 result:=&ilnBootstrapProofsRPC{
  OriginChainID:1643,DestinationDomain:domain,
  Proofs:make([]ilnBootstrapPublicProofRPC,0),
 }
 directory:=filepath.Join(x.dataDir,"interchain","bootstrap-public",
  strconv.FormatUint(destinationDomain,10))
 entries,err:=os.ReadDir(directory)
 if os.IsNotExist(err){return result,nil}
 if err!=nil{return nil,err}
 if len(entries)>128{return nil,fmt.Errorf("too many public bootstrap files")}
 for _,entry:=range entries{
  if !strings.HasSuffix(entry.Name(),".json")||entry.IsDir() {
   return nil,fmt.Errorf("unexpected public bootstrap evidence file")
  }
  path:=filepath.Join(directory,entry.Name())
  stat,err:=os.Lstat(path)
  if err!=nil{return nil,err}
  if !stat.Mode().IsRegular()||stat.Size()<=0||stat.Size()>4096{
   return nil,fmt.Errorf("invalid public bootstrap evidence file")
  }
  raw,err:=os.ReadFile(path)
  if err!=nil{return nil,err}
  var p ilnBootstrapPublicProofRPC
  if err:=json.Unmarshal(raw,&p);err!=nil{return nil,err}
  if strings.ToLower(p.Validator)+".json"!=entry.Name(){
   return nil,fmt.Errorf("public bootstrap filename and validator mismatch")
  }
  if err:=verifyRPCBootstrapPublicProof(p,domain);err!=nil{
   return nil,fmt.Errorf("invalid stored bootstrap proof: %w",err)
  }
  result.Proofs=append(result.Proofs,p)
 }
 return result,nil
}
