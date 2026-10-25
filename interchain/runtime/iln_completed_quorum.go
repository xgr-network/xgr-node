package runtime

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
	"github.com/xgr-network/xgr-node/crypto"
	evm "github.com/xgr-network/xgr-node/interchain/evm"
	"github.com/xgr-network/xgr-node/types"
)

// Replicate a COMPLETE cryptographic quorum, not a peer's unsigned assertion.
// Local signers still require two independent source-RPC witnesses before
// creating any BLS vote. Importing nodes validate the current destination set,
// effective destination ISM, immutable signed route and 2/3 aggregate.
// Destination Mailbox.process and XITA recovery remain independent guards.
const (
	ilnCompleteRepeatInterval = 2 * time.Minute
	ilnCompleteMaxPerTick = 4
	ilnCompleteCandidateCap = 64
	ilnCompleteMaxArchiveBytes = 16 << 10
	ilnCompleteInboundInterval = 100 * time.Millisecond
)

func ilnUnhexBounded(s string, max int) ([]byte, error) {
	if !strings.HasPrefix(s, "0x") || len(s)<4 || len(s)%2!=0 ||
		len(s)>2+max*2 {return nil,fmt.Errorf("invalid bounded Direct-BLS hex")}
	return hex.DecodeString(s[2:])
}

// Pure cryptographic validation; there is no external source-RPC read here.
func verifyImportedILNQuorum(a ilnCheckpointAttestation,
	route *evm.CheckpointRoute, source,destination *evm.Destination,
	set *evm.ValidatorSet, ism types.Address) (protocol.XITADirectMessage,error) {
	var p protocol.XITADirectMessage
	if route==nil||source==nil||destination==nil||set==nil||
		set.SetID==0||len(set.Validators)==0||
		len(set.Validators)!=len(set.BLSPublicKeys)||ism==types.ZeroAddress{
		return p,fmt.Errorf("missing canonical Direct-BLS route or validator set")
	}
	if a.Version!=protocol.XITADirectMessageDomain||
		a.Chain!=route.Name||a.Destination!=destination.Name||
		route.SourceNetwork!=source.Name||route.Destination!=destination.Name{
		return p,fmt.Errorf("Direct-BLS quorum route or protocol mismatch")
	}
	raw,err:=ilnUnhexBounded(a.Payload,2048)
	if err!=nil{return p,err}
	if err=p.UnmarshalBinary(raw);err!=nil{return p,err}
	reencoded,err:=p.MarshalBinary()
	if err!=nil||!bytes.Equal(raw,reencoded){
		return p,fmt.Errorf("noncanonical Direct-BLS signed payload")
	}
	if p.RouteID!=route.RouteID||p.SourceChainID!=source.ChainID||
		p.SourceDomain!=source.Domain||p.DestinationDomain!=destination.Domain||
		p.SourceRegistry!=types.StringToAddress(source.ILNRegistryAddress)||
		p.DestinationRegistry!=types.StringToAddress(destination.RegistryAddress)||
		p.DestinationISM!=ism||p.SetID!=set.SetID||
		p.MessageID==types.ZeroHash||p.SourceBlockNumber==0{
		return p,fmt.Errorf("signed Direct-BLS route, contract or active set mismatch")
	}
	if a.SetID!=p.SetID||a.OriginChainID!=p.SourceChainID||
		a.OriginDomain!=p.SourceDomain||a.DestinationDomain!=p.DestinationDomain||
		a.SourceBlockNumber!=p.SourceBlockNumber||
		!strings.EqualFold(a.RouteID,p.RouteID.String())||
		!strings.EqualFold(a.AuthorizedMessageID,p.MessageID.String())||
		!strings.EqualFold(a.Registry,p.SourceRegistry.String())||
		!strings.EqualFold(a.Gateway,p.Gateway.String())||
		!strings.EqualFold(a.SourceRouter,p.SourceRouter.String())||
		!strings.EqualFold(a.Mailbox,p.SourceMailbox.String())||
		!strings.EqualFold(a.DestinationRouter,p.DestinationRouter.String())||
		!strings.EqualFold(a.DestinationISM,p.DestinationISM.String())||
		!strings.EqualFold(a.DestinationRegistry,p.DestinationRegistry.String())||
		p.ValidatorFeeWei==nil||a.ValidatorFeeWei!=p.ValidatorFeeWei.String(){
		return p,fmt.Errorf("Direct-BLS archive metadata disagrees with signed payload")
	}
	bitmapRaw,err:=ilnUnhexBounded(a.SignerBitmap,256)
	if err!=nil{return p,err}
	bitmap:=new(big.Int).SetBytes(bitmapRaw)
	if bitmap.Sign()<=0||bitmap.BitLen()>len(set.Validators){
		return p,fmt.Errorf("invalid Direct-BLS signer bitmap")
	}
	compressed,err:=ilnUnhexBounded(a.AggregateSignatureCompressed,96)
	if err!=nil{return p,err}
	if len(compressed)!=96{return p,fmt.Errorf("invalid Direct-BLS compressed signature length")}
	expected,err:=crypto.BLSSignatureToEIP2537(compressed)
	if err!=nil{return p,err}
	eip,err:=ilnUnhexBounded(a.AggregateSignature,256)
	if err!=nil{return p,err}
	if !bytes.Equal(expected,eip){
		return p,fmt.Errorf("mismatched EIP-2537 and compressed BLS signatures")
	}
	if err=protocol.VerifyAggregatedQuorum(set.BLSPublicKeys,bitmap,compressed,raw);err!=nil{
		return p,fmt.Errorf("invalid Direct-BLS quorum aggregate: %w",err)
	}
	return p,nil
}

func (w *Worker) acceptILNCompletedQuorum(a ilnCheckpointAttestation)error{
	if w==nil||w.state==nil||!w.state.IsSynchronized(){
		return fmt.Errorf("XGRChain must be synchronized before quorum import")
	}
	raw,err:=ilnUnhexBounded(a.Payload,2048)
	if err!=nil{return err}
	var signed protocol.XITADirectMessage
	if err=signed.UnmarshalBinary(raw);err!=nil{return err}
	// A hint supplied by a gossip peer cannot select an arbitrary route.
	w.mu.Lock()
	var route *evm.CheckpointRoute
	var source,dest *evm.Destination
	for _,candidate:=range w.routes{
		if candidate==nil||candidate.RouteID!=signed.RouteID{continue}
		s,d:=w.destinations[candidate.SourceNetwork],w.destinations[candidate.Destination]
		if s==nil||d==nil||s.ChainID!=signed.SourceChainID||
			s.Domain!=signed.SourceDomain||d.Domain!=signed.DestinationDomain{continue}
		if route!=nil{w.mu.Unlock();return fmt.Errorf("ambiguous canonical Direct-BLS route")}
		route,source,dest=candidate,s,d
	}
	w.mu.Unlock()
	if route==nil{return fmt.Errorf("unknown canonical Direct-BLS route")}
	// Avoid redoing expensive destination reads on duplicate gossip.
	archive:=filepath.Join(w.attestationDir(),route.Name,signed.MessageID.String(),
		fmt.Sprintf("%d.json",signed.SetID))
	if info,e:=os.Stat(archive);e==nil&&info.Mode().IsRegular(){return nil}
	set,err:=getILNValidatorSet(dest)
	if err!=nil{return fmt.Errorf("destination validator set read failed: %w",err)}
	ism,err:=evm.GetDirectILNDestinationISM(dest,signed.DestinationRouter)
	if err!=nil{return fmt.Errorf("destination ISM verification failed: %w",err)}
	p,err:=verifyImportedILNQuorum(a,route,source,dest,set,ism)
	if err!=nil{return err}
	delivered,err:=getILNMessageDelivered(dest,p.DestinationRouter,p.MessageID)
	if err!=nil{return fmt.Errorf("destination delivery status read failed: %w",err)}
	if delivered{return nil}
	if err=w.writeILNCheckpointAttestation(a);err!=nil{return err}
	threshold,err:=protocol.QuorumThreshold(len(set.Validators))
	if err!=nil{return err}
	bitmapRaw,_:=ilnUnhexBounded(a.SignerBitmap,256)
	bitmap:=new(big.Int).SetBytes(bitmapRaw)
	votes:=0
	for i:=0;i<bitmap.BitLen();i++{votes+=int(bitmap.Bit(i))}
	if err:=w.recordILNQuorumProgress(ilnQuorumProgress{
		Route:route.Name,MessageID:p.MessageID.String(),
		SourceBlockNumber:p.SourceBlockNumber,
		Status:"AVAILABLE_UNVERIFIED",Votes:votes,QuorumThreshold:threshold,
	});err!=nil{w.logger.Warn("completed Direct-BLS quorum status write failed","err",err)}
	return nil
}

func (w *Worker) publishILNCompletedQuorum(a ilnCheckpointAttestation){
	if w==nil||w.topic==nil{return}
	key:=a.Chain+"/"+a.AuthorizedMessageID+"/"+fmt.Sprint(a.SetID)
	now:=time.Now()
	w.mu.Lock()
	if w.ilnCompletedPublished==nil{w.ilnCompletedPublished=make(map[string]time.Time)}
	if last:=w.ilnCompletedPublished[key];!last.IsZero()&&now.Sub(last)<ilnCompleteRepeatInterval{
		w.mu.Unlock()
		return
	}
	w.ilnCompletedPublished[key]=now
	w.mu.Unlock()
	if err:=w.publish(wireEnvelope{Type:"iln_completed_quorum",ILNCompletedQuorum:&a});err!=nil{
		w.mu.Lock()
		delete(w.ilnCompletedPublished,key)
		w.mu.Unlock()
		w.logger.Debug("Direct-BLS completed quorum publish retry","err",err)
	}
}

type ilnQuorumCandidate struct{
	path string
	modified time.Time
}
func ilnCompletedCandidates(dir string)[]ilnQuorumCandidate{
	entries,err:=os.ReadDir(dir)
	if err!=nil{return nil}
	out:=make([]ilnQuorumCandidate,0)
	for _,entry:=range entries{
		name:=entry.Name()
		if entry.IsDir()||len(name)!=len("0x")+64+len(".json")||
			!strings.HasSuffix(name,".json"){continue}
		if _,err:=ilnUnhexBounded(strings.TrimSuffix(name,".json"),32);err!=nil{continue}
		info,err:=entry.Info()
		if err!=nil||!info.Mode().IsRegular()||info.Size()>ilnCompleteMaxArchiveBytes{continue}
		out=append(out,ilnQuorumCandidate{filepath.Join(dir,name),info.ModTime()})
	}
	sort.Slice(out,func(i,j int)bool{return out[i].modified.After(out[j].modified)})
	if len(out)>ilnCompleteCandidateCap{out=out[:ilnCompleteCandidateCap]}
	return out
}

func (w *Worker) rebroadcastILNCompletedQuorums(){
	if w==nil||w.topic==nil{return}
	w.mu.Lock()
	routes:=make([]string,0,len(w.routes))
	for name:=range w.routes{routes=append(routes,name)}
	w.mu.Unlock()
	sort.Strings(routes)
	sent:=0
	for _,route:=range routes{
		if sent>=ilnCompleteMaxPerTick{return}
		for _,candidate:=range ilnCompletedCandidates(filepath.Join(w.attestationDir(),route)){
			if sent>=ilnCompleteMaxPerTick{return}
			data,err:=os.ReadFile(candidate.path)
			if err!=nil||len(data)>ilnCompleteMaxArchiveBytes{continue}
			var a ilnCheckpointAttestation
			if json.Unmarshal(data,&a)!=nil||a.SetID==0||a.Chain!=route||
				filepath.Base(candidate.path)!=a.AuthorizedMessageID+".json"{continue}
			key:=a.Chain+"/"+a.AuthorizedMessageID+"/"+fmt.Sprint(a.SetID)
			w.mu.Lock()
			last:=w.ilnCompletedPublished[key]
			w.mu.Unlock()
			if !last.IsZero()&&time.Since(last)<ilnCompleteRepeatInterval{continue}
			w.publishILNCompletedQuorum(a)
			sent++
		}
	}
}
