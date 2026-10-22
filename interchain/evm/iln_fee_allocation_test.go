package evm

import (
    "encoding/json"
    "fmt"
    "net/http"
    "net/http/httptest"
    "strings"
    "sync"
    "testing"

    "github.com/stretchr/testify/require"
    protocol "github.com/xgr-network/xgr-node/consensus/ibft/interchain"
    "github.com/xgr-network/xgr-node/crypto"
    "github.com/xgr-network/xgr-node/types"
)

func TestILNFeeVaultAllocationIsOnChainAndIndependentFromHistoricLogs(t *testing.T) {
    registry := types.StringToAddress("0x1111111111111111111111111111111111111111")
    gateway := types.StringToAddress("0x2222222222222222222222222222222222222222")
    vault := types.StringToAddress("0x3333333333333333333333333333333333333333")
    governance := types.StringToAddress("0x4444444444444444444444444444444444444444")
    routeID := types.StringToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
    message := types.StringToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
    wordAddress := func(a types.Address) string { return fmt.Sprintf("0x%064x",a.Bytes()) }
    wordInt := func(n uint64) string {return fmt.Sprintf("0x%064x",n)}
    sel := func(s string) string {return crypto.Keccak256Hash([]byte(s)).String()[:10]}
    var mu sync.Mutex
    allocated := true
    wrongDomain := false
    wrongRoute := false
    wrongGovernance := false
    missingState := false
    logCalls := 0
    receiptCalls := 0
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
        var req struct {
            ID json.RawMessage `json:"id"`
            Method string `json:"method"`
            Params []json.RawMessage `json:"params"`
        }
        require.NoError(t,json.NewDecoder(r.Body).Decode(&req))
        mu.Lock()
        defer mu.Unlock()
        w.Header().Set("Content-Type","application/json")
        var result string
        switch req.Method {
        case "eth_chainId": result = "0x2105"
        case "eth_blockNumber": result = "0x96" // 150 - 12 = 138, checkpoint 128
        case "eth_getLogs":
            logCalls++
            t.Error("allocation proof must not fetch historical Gateway logs")
        case "eth_getTransactionReceipt":
            receiptCalls++
            t.Error("allocation proof must not require historical receipts")
        case "eth_call":
            require.Len(t,req.Params,2)
            var call struct {To string `json:"to"`;Data string `json:"data"`}
            require.NoError(t,json.Unmarshal(req.Params[0],&call))
            var at string
            require.NoError(t,json.Unmarshal(req.Params[1],&at))
            selector := strings.ToLower(call.Data[:10])
            switch selector {
            case sel("feeVault()"):
                require.Equal(t,gateway.String(),strings.ToLower(call.To))
                require.Equal(t,"latest",at)
                result=wordAddress(vault)
            case sel("gateway()"):
                require.Equal(t,"latest",at)
                result=wordAddress(gateway)
            case sel("routeId()"):
                require.Equal(t,"latest",at)
                if wrongRoute {result="0x"+strings.Repeat("0",64)}else{result=routeID.String()}
            case sel("destinationDomain()"):
                require.Equal(t,"latest",at)
                if wrongDomain {result=wordInt(1)}else{result=wordInt(1643)}
            case sel("governanceRegistry()"):
                require.Equal(t,"latest",at)
                result=wordAddress(governance)
            case sel("validatorRegistry()"):
                require.Equal(t,"latest",at)
                if wrongGovernance {result=wordAddress(gateway)}else{result=wordAddress(governance)}
            case sel("allocatedOperation(bytes32)"):
                require.Equal(t,"latest",at)
                require.Equal(t,message.String()[2:],strings.ToLower(call.Data[10:]))
                if missingState {
                    _,_=fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"error":{"code":-32000,"message":"pruned checkpoint state"}}`,req.ID)
                    return
                }
                if allocated {result=wordInt(1)}else{result=wordInt(0)}
            case sel("allocatedSetId(bytes32)"):
                require.Equal(t,"latest",at)
                if allocated {result=wordInt(3)}else{result=wordInt(0)}
            default:
                t.Errorf("unexpected contract selector %s",selector)
                result=wordInt(0)
            }
        default:
            t.Errorf("unexpected source RPC method %s",req.Method)
            result=wordInt(0)
        }
        _,_=fmt.Fprintf(w,`{"jsonrpc":"2.0","id":%s,"result":"%s"}`,req.ID,result)
    }))
    defer srv.Close()
    source := &Destination{
        Name:"base",ChainID:8453,Domain:8453,RPCURL:srv.URL,
        ILNRegistryAddress:registry.String(),Confirmations:12,
    }
    snapshot := &ILNRouteSnapshot{Registry:registry,Route:protocol.ILNRoute{
        Key:protocol.ILNRouteKey{SourceChainID:8453,SourceDomain:8453,
            DestinationDomain:1643,RouteID:routeID},
        Gateway:gateway,
    },BlockNumber:125}
    require.NoError(t,VerifyConfirmedILNFeeAllocation(source,snapshot,message,128))
    mu.Lock();allocated=false;mu.Unlock()
    require.ErrorContains(t,VerifyConfirmedILNFeeAllocation(source,snapshot,message,128),
        "not allocated by route FeeVault")
    mu.Lock();allocated=true;wrongRoute=true;mu.Unlock()
    require.ErrorContains(t,VerifyConfirmedILNFeeAllocation(source,snapshot,message,128),
        "routeId mismatch")
    mu.Lock();wrongRoute=false;wrongDomain=true;mu.Unlock()
    require.ErrorContains(t,VerifyConfirmedILNFeeAllocation(source,snapshot,message,128),
        "destinationDomain mismatch")
    mu.Lock();wrongDomain=false;wrongGovernance=true;mu.Unlock()
    require.ErrorContains(t,VerifyConfirmedILNFeeAllocation(source,snapshot,message,128),
        "governance authority mismatch")
    mu.Lock();wrongGovernance=false;missingState=true;mu.Unlock()
    require.ErrorContains(t,VerifyConfirmedILNFeeAllocation(source,snapshot,message,128),
        "allocatedOperation immutable getter unavailable")
    require.ErrorContains(t,VerifyConfirmedILNFeeAllocation(source,snapshot,message,140),
        "not source confirmed")
    mu.Lock()
    require.Zero(t,logCalls)
    require.Zero(t,receiptCalls)
    mu.Unlock()
}
