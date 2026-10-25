package evm

import (
    "fmt"
    "sort"
    "strings"
    "sync"
    "time"

    
)

// This failover is restricted to Interchain RPC reads. Consensus, IBFT, state
// and transaction broadcasting are not affected. Every evidence witness stays
// pinned to ONE endpoint; operator independence is enforced by the caller.
const primaryProbeInterval = 30*time.Second
const primaryRecoveryProbes = 3
const rpcRateLimitCooldown = 30*time.Second
const rpcTransportCooldown = 5*time.Second
const rpcMaximumCooldown = 3*time.Minute

type rpcHealth struct {
    mu sync.Mutex
    selected int
    lastProbe time.Time
    healthy int
}
var rpcHealthByChain sync.Map

// Provider health is shared by ordinary reads and independent BLS witnesses.
// In particular, single-URL witnesses must respect the same 429 cooldown as
// background scanners; otherwise the quorum loop can bypass backpressure.
type rpcProviderHealth struct {
    mu sync.Mutex
    chainID uint64
    url string
    attempted uint64
    ready uint64
    failed uint64
    rateLimited uint64
    readFailures uint64
    consecutive uint32
    // Read failures survive a successful chain-ID / safe-head preflight:
    // a provider may serve headers while continuously rejecting eth_getLogs.
    readConsecutive uint32
    retryAfter time.Time
}
var rpcProviderHealthByURL sync.Map

// RPCProviderStats are per-process, read-only diagnostics. Attempts count
// verified client acquisitions, NOT raw eth_call/eth_getLogs HTTP requests.
type RPCProviderStats struct {
    ChainID uint64
    URL string
    DialAttempts uint64
    Ready uint64
    Failures uint64
    RateLimited uint64
    ReadFailures uint64
    CooldownUntilUnix int64
}

// InterchainRPCProviderStats exposes provider selection / 429 diagnosis without
// triggering new remote RPC queries. This can be wired into health reporting.
func InterchainRPCProviderStats() []RPCProviderStats {
    out:=make([]RPCProviderStats,0)
    rpcProviderHealthByURL.Range(func(_,value interface{})bool{
        p:=value.(*rpcProviderHealth)
        p.mu.Lock()
        item:=RPCProviderStats{ChainID:p.chainID,URL:p.url,
            DialAttempts:p.attempted,Ready:p.ready,Failures:p.failed,
            RateLimited:p.rateLimited,ReadFailures:p.readFailures}
        if time.Now().Before(p.retryAfter){item.CooldownUntilUnix=p.retryAfter.Unix()}
        p.mu.Unlock()
        out=append(out,item)
        return true
    })
    sort.Slice(out,func(i,j int)bool{
        if out[i].ChainID!=out[j].ChainID{return out[i].ChainID<out[j].ChainID}
        return out[i].URL<out[j].URL
    })
    return out
}

// Sticky fallback is tied to the exact endpoint policy, not just the chain
// and registry. A new endpoint set must never inherit a stale fallback index
// or primary probe timer from an older configuration. This also makes repeated
// go test -count runs independent when httptest providers use new ports.
func rpcStateKey(d *Destination)string{
    return fmt.Sprintf("%d/%s/%s",d.ChainID,d.RegistryAddress,
        strings.Join(rpcEndpoints(d),"\x00"))
}
func rpcState(d *Destination)*rpcHealth{
    state,_:=rpcHealthByChain.LoadOrStore(rpcStateKey(d),&rpcHealth{})
    return state.(*rpcHealth)
}
func rpcEndpoints(d *Destination)[]string{
    if d==nil{return nil}
    if len(d.RPCURLs)>0{return d.RPCURLs}
    if d.RPCURL!=""{return []string{d.RPCURL}}
    return nil
}
func rpcProviderState(d *Destination,url string)*rpcProviderHealth{
    key:=fmt.Sprintf("%d/%s",d.ChainID,url)
    state,_:=rpcProviderHealthByURL.LoadOrStore(key,&rpcProviderHealth{
        chainID:d.ChainID,url:url,
    })
    return state.(*rpcProviderHealth)
}
func rpcRateLimited(err error)bool{
    if err==nil{return false}
    s:=strings.ToLower(err.Error())
    for _,v:=range []string{"429","too many requests","rate limit","rate-limit","quota exceeded"}{
        if strings.Contains(s,v){return true}
    }
    return false
}
func rpcTransientFailure(err error)bool{
    if err==nil{return false}
    if rpcRateLimited(err){return true}
    s:=strings.ToLower(err.Error())
    for _,v:=range []string{"timeout","deadline exceeded","connection","eof","503","502","504","403","temporarily","rpc unavailable","connection reset","network is unreachable"}{
        if strings.Contains(s,v){return true}
    }
    return false
}
func rpcCooldown(d *Destination,url string)error{
    p:=rpcProviderState(d,url)
    p.mu.Lock()
    until:=p.retryAfter
    p.mu.Unlock()
    if time.Now().Before(until){
        return fmt.Errorf("interchain RPC provider in cooldown until %s",until.UTC().Format(time.RFC3339))
    }
    return nil
}
func rpcRecordFailure(d *Destination,url string,err error,read bool){
    if d==nil||url==""||err==nil{return}
    if read&&!rpcTransientFailure(err){return}
    p:=rpcProviderState(d,url)
    p.mu.Lock()
    defer p.mu.Unlock()
    p.failed++
    var streak uint32
    if read {
        p.readFailures++
        if p.readConsecutive<5{p.readConsecutive++}
        streak=p.readConsecutive
    } else {
        if p.consecutive<5{p.consecutive++}
        streak=p.consecutive
    }
    if rpcRateLimited(err){p.rateLimited++}
    delay:=rpcTransportCooldown
    if rpcRateLimited(err){delay=rpcRateLimitCooldown}
    // Retry state is per provider and per observed RPC read. A successful
    // safe-head preflight must not erase repeated eth_getLogs 429 failures.
    for n:=uint32(1);n<streak;n++{delay*=2}
    if delay>rpcMaximumCooldown{delay=rpcMaximumCooldown}
    next:=time.Now().Add(delay)
    if next.After(p.retryAfter){p.retryAfter=next}
}
func rpcRecordReady(d *Destination,url string){
    p:=rpcProviderState(d,url)
    p.mu.Lock()
    p.ready++
    p.consecutive=0
    // The validated safe head is NOT evidence that the provider can serve
    // archive logs. Do not reset readConsecutive here.
    if !time.Now().Before(p.retryAfter){p.retryAfter=time.Time{}}
    p.mu.Unlock()
}
// Only a completed successful RPC read resets the read-specific 429 streak.
// A concurrent failure's cooldown must not be removed by this success.
func rpcRecordReadSuccess(d *Destination,url string){
    if d==nil||url==""{return}
    p:=rpcProviderState(d,url)
    p.mu.Lock()
    p.readConsecutive=0
    if !time.Now().Before(p.retryAfter){p.retryAfter=time.Time{}}
    p.mu.Unlock()
}
// Attribute a read to the actual bounded client rather than a shared sticky
// selection that other concurrent requests may already have rotated.
func recordInterchainRPCReadForClient(d *Destination,client ilnRPCClient,err error){
    if bound,ok:=client.(*boundedILNRPC);ok{
        if err==nil{rpcRecordReadSuccess(d,bound.endpoint)
        }else{recordInterchainRPCReadError(d,bound.endpoint,err)}
        return
    }
    if err!=nil{recordInterchainRPCReadFailure(d)}
}

// rpcDialVerifiedWithHead verifies ChainID and an independently obtained
// finality head once for the selected provider. Callers that need the head
// reuse it, avoiding repeated safe/number/hash checks in the same operation.
func rpcDialVerifiedWithHead(d *Destination,url string)(ilnRPCClient,uint64,error){
    if d==nil{return nil,0,fmt.Errorf("missing Interchain destination")}
    if err:=rpcCooldown(d,url);err!=nil{return nil,0,err}
    p:=rpcProviderState(d,url)
    p.mu.Lock()
    p.attempted++
    p.mu.Unlock()
    client,err:=newBoundedILNRPC(d.ilnRPCParent(),url)
    if err!=nil{
        rpcRecordFailure(d,url,err,false)
        return nil,0,err
    }
    chainID,err:=ilnRPCChainID(client)
    if err!=nil||chainID==nil||!chainID.IsUint64()||chainID.Uint64()!=d.ChainID{
        client.Close()
        failure:=fmt.Errorf("interchain RPC chain identity failed for %s: %v",d.Name,err)
        rpcRecordFailure(d,url,failure,false)
        return nil,0,failure
    }
    head,err:=finalizedILNHead(client,d)
    if err!=nil{
        client.Close()
        failure:=fmt.Errorf("interchain RPC finality unavailable for %s: %w",d.Name,err)
        rpcRecordFailure(d,url,failure,false)
        return nil,0,failure
    }
    rpcRecordReady(d,url)
    return client,head,nil
}
func rpcDialVerified(d *Destination,url string)(ilnRPCClient,error){
    client,_,err:=rpcDialVerifiedWithHead(d,url)
    return client,err
}

// newVerifiedRPCClientWithHead shares the verified source head with its caller.
// Failed providers are skipped during cooldown. Sticky failover avoids repeatedly
// hammering a rate-limited primary; three spaced probes restore the primary.
func newVerifiedRPCClientWithHead(d *Destination)(ilnRPCClient,uint64,error){
    endpoints:=rpcEndpoints(d)
    if len(endpoints)==0{return nil,0,fmt.Errorf("interchain RPC endpoint missing")}
    if len(endpoints)==1{return rpcDialVerifiedWithHead(d,endpoints[0])}
    state:=rpcState(d)
    state.mu.Lock()
    defer state.mu.Unlock()
    now:=time.Now()
    if state.selected<0||state.selected>=len(endpoints){state.selected=0}
    if state.selected>0 {
        if now.Sub(state.lastProbe)>=primaryProbeInterval{
            state.lastProbe=now
            primary,head,err:=rpcDialVerifiedWithHead(d,endpoints[0])
            if err==nil{
                state.healthy++
                if state.healthy>=primaryRecoveryProbes{
                    state.selected=0
                    state.healthy=0
                    return primary,head,nil
                }
                primary.Close()
            } else {state.healthy=0}
        }
        fallback,head,err:=rpcDialVerifiedWithHead(d,endpoints[state.selected])
        if err==nil{return fallback,head,nil}
        state.healthy=0
        var lastErr error=err
        // Continue with all other providers; never retry a cooled-down URL.
        for idx,url:=range endpoints{
            if idx==state.selected{continue}
            candidate,h,e:=rpcDialVerifiedWithHead(d,url)
            if e==nil{
                state.selected=idx
                state.lastProbe=now
                return candidate,h,nil
            }
            lastErr=e
        }
        return nil,0,fmt.Errorf("all interchain RPC endpoints failed for %s: %w",d.Name,lastErr)
    }
    var lastErr error
    for idx,url:=range endpoints{
        candidate,h,err:=rpcDialVerifiedWithHead(d,url)
        if err==nil{
            state.selected=idx
            if idx>0{state.lastProbe=now;state.healthy=0}
            return candidate,h,nil
        }
        lastErr=err
    }
    return nil,0,fmt.Errorf("all interchain RPC endpoints failed for %s: %w",d.Name,lastErr)
}

func newVerifiedRPCClient(d *Destination)(ilnRPCClient,error){
    client,_,err:=newVerifiedRPCClientWithHead(d)
    return client,err
}

// Called on explicitly failed reads. Never retries submitted transactions.
// Read errors for single-provider BLS witnesses MUST also mark that provider.
func recordInterchainRPCReadFailure(d *Destination){
    endpoints:=rpcEndpoints(d)
    if len(endpoints)==0{return}
    if len(endpoints)==1{
        rpcRecordFailure(d,endpoints[0],fmt.Errorf("interchain RPC read unavailable"),true)
        return
    }
    state:=rpcState(d)
    state.mu.Lock()
    idx:=state.selected
    if idx<0||idx>=len(endpoints){idx=0}
    state.selected=(idx+1)%len(endpoints)
    state.healthy=0
    state.lastProbe=time.Now()
    state.mu.Unlock()
    rpcRecordFailure(d,endpoints[idx],fmt.Errorf("interchain RPC read unavailable"),true)
}
func recordInterchainRPCReadError(d *Destination,url string,err error){
    if d==nil||url==""||err==nil{return}
    if !rpcTransientFailure(err){return}
    rpcRecordFailure(d,url,err,true)
    endpoints:=rpcEndpoints(d)
    if len(endpoints)<2{return}
    state:=rpcState(d)
    state.mu.Lock()
    if state.selected<len(endpoints)&&endpoints[state.selected]==url{
        state.selected=(state.selected+1)%len(endpoints)
        state.healthy=0
        state.lastProbe=time.Now()
    }
    state.mu.Unlock()
}

func selectVerifiedRPCURL(d *Destination)(string,error){
    client,_,err:=newVerifiedRPCClientWithHead(d)
    if err!=nil{return "",err}
    defer client.Close()
    // Transaction broadcasts are NEVER retried automatically by this layer.
    urls:=rpcEndpoints(d)
    if len(urls)==1{return urls[0],nil}
    state:=rpcState(d)
    state.mu.Lock()
    idx:=state.selected
    state.mu.Unlock()
    if idx<0||idx>=len(urls){return "",fmt.Errorf("invalid selected interchain RPC index")}
    return urls[idx],nil
}
