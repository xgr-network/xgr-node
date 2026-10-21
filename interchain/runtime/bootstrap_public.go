package runtime

// Bootstrap possession proofs are PUBLIC and destination-domain-bound. The
// existing authenticated libp2p interchain topic transports them, but no
// peer assertion is signing authority: every received proof is verified
// against the canonical bootstrap payload and active XGR PoS BLS key.
import (
 "bytes"
 "encoding/hex"
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "sort"
 "strconv"
 "strings"
 "time"

 protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
 "github.com/xgr-network/xgr-node/crypto"
 "github.com/xgr-network/xgr-node/types"
)

const bootstrapPublicOrigin uint64 = 1643
const bootstrapPublicBroadcastInterval = 2 * time.Minute

type bootstrapPublicProofWire struct {
 Validator string `json:"validator"`
 OriginChainID uint64 `json:"originChainId"`
 DestinationDomain uint32 `json:"destinationDomain"`
 BLSPublicKey string `json:"blsPublicKey"`
 BLSPublicKeyEIP2537 string `json:"blsPublicKeyEIP2537"`
 Payload string `json:"payload"`
 PossessionProof string `json:"possessionProof"`
 CompressedProof string `json:"compressedProof"`
}

// The explicit allowlist is local node operator policy, not a peer request.
// It permits proof publication BEFORE a new chain has a ValidatorRegistry or
// RPC configured. Merely knowing an untrusted destination ID is insufficient.
func parseILNBootstrapDomains(raw string)([]uint32,error){
 if strings.TrimSpace(raw)=="" {return nil,nil}
 fields:=strings.Split(raw,",")
 if len(fields)>16 {return nil,fmt.Errorf("too many bootstrap destinations")}
 seen:=make(map[uint32]bool)
 out:=make([]uint32,0,len(fields))
 for _,field:=range fields {
  trimmed:=strings.TrimSpace(field)
  if trimmed=="" {return nil,fmt.Errorf("empty bootstrap destination domain")}
  n,err:=strconv.ParseUint(trimmed,10,32)
  if err!=nil||n==0{return nil,fmt.Errorf("invalid bootstrap destination domain %q",trimmed)}
  domain:=uint32(n)
  if seen[domain] {return nil,fmt.Errorf("duplicate bootstrap destination domain %d",domain)}
  seen[domain]=true
  out=append(out,domain)
 }
 sort.Slice(out,func(i,j int)bool{return out[i]<out[j]})
 return out,nil
}

func (w *Worker) bootstrapDomainAllowed(domain uint32)bool{
 if domain==0{return false}
 for _,v:=range w.bootstrapDomains {if v==domain{return true}}
 return false
}

func bootstrapHex(input string,size int)([]byte,error){
 if len(input)!=2+2*size||!strings.HasPrefix(input,"0x"){
  return nil,fmt.Errorf("invalid public bootstrap field length")
 }
 bytes,err:=hex.DecodeString(input[2:])
 if err!=nil||len(bytes)!=size{return nil,fmt.Errorf("invalid public bootstrap hex encoding")}
 return bytes,nil
}

func verifyBootstrapPublicProof(p bootstrapPublicProofWire) (types.Address,[]byte,error){
 if p.OriginChainID!=bootstrapPublicOrigin||p.DestinationDomain==0 {
  return types.ZeroAddress,nil,fmt.Errorf("bootstrap origin or destination domain mismatch")
 }
 if len(p.Validator)!=42||!strings.HasPrefix(p.Validator,"0x"){
  return types.ZeroAddress,nil,fmt.Errorf("invalid bootstrap validator address")
 }
 rawAddr,err:=bootstrapHex(p.Validator,20)
 if err!=nil{return types.ZeroAddress,nil,err}
 addr:=types.BytesToAddress(rawAddr)
 if addr==types.ZeroAddress{return types.ZeroAddress,nil,fmt.Errorf("zero bootstrap validator")}
 pub,err:=bootstrapHex(p.BLSPublicKey,48)
 if err!=nil{return types.ZeroAddress,nil,err}
 eipPub,err:=bootstrapHex(p.BLSPublicKeyEIP2537,128)
 if err!=nil{return types.ZeroAddress,nil,err}
 proof,err:=bootstrapHex(p.CompressedProof,96)
 if err!=nil{return types.ZeroAddress,nil,err}
 eipProof,err:=bootstrapHex(p.PossessionProof,256)
 if err!=nil{return types.ZeroAddress,nil,err}
 payload,err:=protocol.MarshalBootstrapPayload(p.OriginChainID,p.DestinationDomain,addr,pub,eipPub)
 if err!=nil{return types.ZeroAddress,nil,err}
 expectedPayload,err:=bootstrapHex(p.Payload,len(payload))
 if err!=nil||!bytes.Equal(payload,expectedPayload) {
  return types.ZeroAddress,nil,fmt.Errorf("bootstrap payload mismatch")
 }
 transformed,err:=crypto.BLSSignatureToEIP2537(proof)
 if err!=nil||!bytes.Equal(transformed,eipProof){
  return types.ZeroAddress,nil,fmt.Errorf("EIP-2537 bootstrap signature does not match compressed signature")
 }
 if err:=crypto.VerifyBLSSignatureFromBytes(pub,proof,payload);err!=nil{
  return types.ZeroAddress,nil,fmt.Errorf("invalid bootstrap BLS proof: %w",err)
 }
 return addr,pub,nil
}

func (w *Worker) makeBootstrapPublicProof(domain uint32)(bootstrapPublicProofWire,error){
 if !w.bootstrapDomainAllowed(domain) || w.state==nil ||
  w.state.OriginChainID()!=bootstrapPublicOrigin {
  return bootstrapPublicProofWire{},fmt.Errorf("bootstrap signing domain is not explicitly enabled")
 }
 status,err:=w.state.ValidatorInfo(w.localAddr)
 if err!=nil{return bootstrapPublicProofWire{},err}
 if status==nil||!status.Exists||!status.Active||
  !bytes.Equal(status.BLSPubKey,w.blsPubKey){
  return bootstrapPublicProofWire{},fmt.Errorf("local validator not an active matching XGR PoS BLS signer")
 }
 secret,err:=crypto.BytesToBLSSecretKey(w.blsRaw)
 if err!=nil{return bootstrapPublicProofWire{},err}
 eipPub,err:=crypto.BLSPublicKeyToEIP2537(w.blsPubKey)
 if err!=nil{return bootstrapPublicProofWire{},err}
 payload,err:=protocol.MarshalBootstrapPayload(bootstrapPublicOrigin,domain,w.localAddr,w.blsPubKey,eipPub)
 if err!=nil{return bootstrapPublicProofWire{},err}
 sig,err:=crypto.SignByBLS(secret,payload)
 if err!=nil{return bootstrapPublicProofWire{},err}
 eipSig,err:=crypto.BLSSignatureToEIP2537(sig)
 if err!=nil{return bootstrapPublicProofWire{},err}
 h:=func(data []byte)string{return "0x"+hex.EncodeToString(data)}
 proof:=bootstrapPublicProofWire{
  Validator:w.localAddr.String(),OriginChainID:bootstrapPublicOrigin,
  DestinationDomain:domain,BLSPublicKey:h(w.blsPubKey),
  BLSPublicKeyEIP2537:h(eipPub),Payload:h(payload),
  CompressedProof:h(sig),PossessionProof:h(eipSig),
 }
 if _,_,err:=verifyBootstrapPublicProof(proof);err!=nil{return bootstrapPublicProofWire{},err}
 return proof,nil
}

func (w *Worker) bootstrapPublicProofPath(domain uint32,address types.Address)string{
 return filepath.Join(w.dataDir,"interchain","bootstrap-public",strconv.FormatUint(uint64(domain),10),
  strings.ToLower(address.String())+".json")
}

func (w *Worker) acceptBootstrapPublicProof(p bootstrapPublicProofWire)error{
 if w==nil||w.state==nil||!w.bootstrapDomainAllowed(p.DestinationDomain)||
  w.state.OriginChainID()!=bootstrapPublicOrigin {
  return fmt.Errorf("unapproved bootstrap destination or origin")
 }
 address,public,err:=verifyBootstrapPublicProof(p)
 if err!=nil{return err}
 info,err:=w.state.ValidatorInfo(address)
 if err!=nil{return err}
 if info==nil||!info.Exists||!info.Active||!bytes.Equal(info.BLSPubKey,public){
  return fmt.Errorf("bootstrap signer is not an active XGR PoS validator with matching BLS key")
 }
 path:=w.bootstrapPublicProofPath(p.DestinationDomain,address)
 raw,err:=json.Marshal(p)
 if err!=nil{return err}
 if len(raw)>4096{return fmt.Errorf("oversized public bootstrap proof")}
 dir:=filepath.Dir(path)
 if err:=os.MkdirAll(dir,0o770);err!=nil{return err}
 tmp,err:=os.CreateTemp(dir,".public-bls-*")
 if err!=nil{return err}
 defer os.Remove(tmp.Name())
 if err:=tmp.Chmod(0o660);err!=nil{tmp.Close();return err}
 if _,err:=tmp.Write(raw);err!=nil{tmp.Close();return err}
 if err:=tmp.Sync();err!=nil{tmp.Close();return err}
 if err:=tmp.Close();err!=nil{return err}
 return os.Rename(tmp.Name(),path)
}

func (w *Worker) publishBootstrapPublicProofs(){
 if w.topic==nil||len(w.bootstrapDomains)==0{return}
 for _,domain:=range w.bootstrapDomains{
  p,err:=w.makeBootstrapPublicProof(domain)
  if err!=nil {
   w.logger.Debug("bootstrap public proof not ready","domain",domain,"err",err)
   continue
  }
  if err:=w.acceptBootstrapPublicProof(p);err!=nil{
   w.logger.Warn("persist local public bootstrap proof failed","domain",domain,"err",err)
   continue
  }
  if err:=w.publish(wireEnvelope{Type:"bootstrap_public_proof",BootstrapPublicProof:&p});err!=nil{
   w.logger.Debug("bootstrap proof gossip unavailable","domain",domain,"err",err)
  }
 }
}
