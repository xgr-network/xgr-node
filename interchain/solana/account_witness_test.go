package solana

import(
    "context"
    "encoding/base64"
    "encoding/json"
    "errors"
    "io"
    "net/http"
    "strings"
    "testing"
)
const testOwner="TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
const testAddress="SysvarRent111111111111111111111111111111111"
type accountWitnessTransport struct{
    data map[string][]byte
    owners map[string]string
    slots map[string]uint64
    genesis map[string]string
}
func (w accountWitnessTransport)RoundTrip(r *http.Request)(*http.Response,error){
    var req struct{
        Method string `json:"method"`
        Params []json.RawMessage `json:"params"`
    }
    if err:=json.NewDecoder(r.Body).Decode(&req);err!=nil{return nil,err}
    host:=r.URL.Host
    var val any
    switch req.Method{
    case "getGenesisHash":
        val=w.genesis[host]
    case "getAccountInfo":
        var opts struct {
            Commitment string `json:"commitment"`
            Encoding string `json:"encoding"`
            MinContextSlot uint64 `json:"minContextSlot"`
        }
        if len(req.Params)!=2{return nil,errors.New("missing receipt options")}
        if err:=json.Unmarshal(req.Params[1],&opts);err!=nil{return nil,err}
        if opts.Commitment!="finalized"||opts.Encoding!="base64"||
            opts.MinContextSlot!=99{
            return nil,errors.New("receipt query did not demand finalized data")
        }
        val=map[string]any{
            "context":map[string]any{"slot":w.slots[host]},
            "value":map[string]any{
                "data":[]string{base64.StdEncoding.EncodeToString(w.data[host]),"base64"},
                "owner":w.owners[host],"lamports":uint64(4_000_000),
                "executable":false,
            },
        }
    default:return nil,errors.New("unsupported RPC")
    }
    body,_:=json.Marshal(map[string]any{
        "jsonrpc":"2.0","id":1,"result":val,
    })
    return &http.Response{
        StatusCode:200,Body:io.NopCloser(strings.NewReader(string(body))),
        Header:make(http.Header),
    },nil
}
func accountObserver(t *testing.T,data1,data2 []byte,owner2 string,slot2 uint64)*Observer{
    t.Helper()
    a:="operator-one.test";b:="operator-two.test"
    o,err:=NewObserver([]Endpoint{
        {URL:"https://"+a,OperatorID:"one"},
        {URL:"https://"+b,OperatorID:"two"},
    },&http.Client{Transport:accountWitnessTransport{
        data:map[string][]byte{a:data1,b:data2},
        owners:map[string]string{a:testOwner,b:owner2},
        slots:map[string]uint64{a:100,b:slot2},
        genesis:map[string]string{a:MainnetGenesis,b:MainnetGenesis},
    }})
    if err!=nil{t.Fatal(err)}
    return o
}
func TestIndependentFinalizedReceiptAccountWitness(t *testing.T){
    receipt:=[]byte{0x44,1,2,3}
    owner,err:=decodeBase58Pubkey(testOwner)
    if err!=nil||owner==([32]byte{}){t.Fatalf("bad owner decode: %v",err)}
    o:=accountObserver(t,receipt,receipt,testOwner,105)
    a,err:=o.FinalizedReceiptAccount(
        context.Background(),testAddress,99,owner,
    )
    if err!=nil{t.Fatal(err)}
    if a.IndependentWitnesses!=2||a.ObservedSlot!=100||
        a.Owner!=owner||len(a.Data)!=len(receipt){
        t.Fatalf("unexpected account witness: %+v",a)
    }
    changed:=accountObserver(t,receipt,[]byte{0x44,1,2,4},testOwner,105)
    if _,err:=changed.FinalizedReceiptAccount(
        context.Background(),testAddress,99,owner,
    );err==nil{t.Fatal("different finalized receipt accepted")}
    invalidOwner:=accountObserver(t,receipt,receipt,testAddress,105)
    if _,err:=invalidOwner.FinalizedReceiptAccount(
        context.Background(),testAddress,99,owner,
    );err==nil{t.Fatal("foreign account owner accepted")}
    oldSlot:=accountObserver(t,receipt,receipt,testOwner,98)
    if _,err:=oldSlot.FinalizedReceiptAccount(
        context.Background(),testAddress,99,owner,
    );err==nil{t.Fatal("unfinalized receipt accepted")}
}
func TestReceiptBase58AndPolicyRejectMalformedAccounts(t *testing.T){
    if _,err:=decodeBase58Pubkey(strings.Repeat("O",32));err==nil{
        t.Fatal("invalid base58 character accepted")
    }
    if _,err:=decodeBase58Pubkey(strings.Repeat("1",33));err==nil{
        t.Fatal("33-byte pubkey accepted")
    }
    o:=accountObserver(t,[]byte{1},[]byte{1},testOwner,101)
    if _,err:=o.FinalizedReceiptAccount(
        context.Background(),testAddress,99,[32]byte{},
    );err==nil{t.Fatal("unpinned program owner accepted")}
    owner,_:=decodeBase58Pubkey(testOwner)
    if _,err:=o.FinalizedReceiptAccount(
        context.Background(),"malformed",99,owner,
    );err==nil{t.Fatal("invalid account key accepted")}
}
