package solana

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type witnessTransport struct {
	genesis map[string]string
	slot    map[string]uint64
	tx      map[string]string
}

func (w witnessTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	var request struct {
		Method string `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		return nil, err
	}
	var result any
	switch request.Method {
	case "getGenesisHash":
		result = w.genesis[r.URL.Host]
	case "getSlot":
		result = w.slot[r.URL.Host]
	case "getTransaction":
		var opts map[string]any
		if len(request.Params) != 2 {
			return nil, errors.New("missing transaction config")
		}
		if err := json.Unmarshal(request.Params[1], &opts); err != nil {
			return nil, err
		}
		if opts["commitment"] != "finalized" || opts["encoding"] != "base64" ||
			opts["maxSupportedTransactionVersion"] != float64(0) {
			return nil, errors.New("non-finalized or non-canonical Solana read")
		}
		result = map[string]any{
			"slot": uint64(99),
			"meta": map[string]any{"err": nil},
			"transaction": []string{w.tx[r.URL.Host], "base64"},
		}
	default:
		return nil, errors.New("unsupported RPC call")
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
}

func testObserver(t *testing.T, secondRaw []byte) *Observer {
	t.Helper()
	tx := append([]byte{1}, make([]byte, 64)...)
	tx = append(tx, 0x80, 0x01)
	urls := []Endpoint{{URL: "https://operator-one.test", OperatorID: "one"}, {URL: "https://operator-two.test", OperatorID: "two"}}
	observer, err := NewObserver(urls, &http.Client{Transport: witnessTransport{
		genesis: map[string]string{"operator-one.test": MainnetGenesis, "operator-two.test": MainnetGenesis},
		slot: map[string]uint64{"operator-one.test": 100, "operator-two.test": 101},
		tx: map[string]string{
			"operator-one.test": base64.StdEncoding.EncodeToString(tx),
			"operator-two.test": base64.StdEncoding.EncodeToString(secondRaw),
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return observer
}

func TestFinalizedTransactionRequiresTwoMatchingWitnesses(t *testing.T) {
	raw := append([]byte{1}, make([]byte, 64)...)
	raw = append(raw, 0x80, 0x01)
	o := testObserver(t, raw)
	got, err := o.FinalizedTransaction(context.Background(), strings.Repeat("1", 64))
	if err != nil {
		t.Fatal(err)
	}
	if got.Slot != 99 || got.IndependentWitnesses != 2 || string(got.RawTransaction) != string(raw) {
		t.Fatalf("unexpected finalized observation: %+v", got)
	}
}

func TestFinalizedTransactionRejectsWitnessDisagreement(t *testing.T) {
	raw := append([]byte{1}, make([]byte, 64)...)
	raw = append(raw, 0x80, 0x02)
	o := testObserver(t, raw)
	if _, err := o.FinalizedTransaction(context.Background(), strings.Repeat("1", 64)); err == nil {
		t.Fatal("different transaction bytes must fail closed")
	}
}

func TestSignatureAndOperatorPolicy(t *testing.T) {
	_, err := NewObserver([]Endpoint{{URL: "https://one.test", OperatorID: "same"}, {URL: "https://two.test", OperatorID: "same"}}, nil)
	if err == nil {
		t.Fatal("one operator cannot count as two witnesses")
	}
	_, err = NewObserver([]Endpoint{{URL: "http://one.test", OperatorID: "one"}, {URL: "https://two.test", OperatorID: "two"}}, nil)
	if err == nil {
		t.Fatal("insecure endpoint accepted")
	}
	raw := append([]byte{1}, make([]byte, 64)...)
	raw = append(raw, 0x80, 0x01)
	o := testObserver(t, raw)
	if _, err := o.FinalizedTransaction(context.Background(), strings.Repeat("O", 64)); err == nil {
		t.Fatal("invalid Solana base58 signature accepted")
	}
}

func TestIndependentOperatorsCannotBeFabricatedWithHostOrCaseAliases(t *testing.T) {
    cases := []struct{
        name string
        first Endpoint
        second Endpoint
    }{
        {
            name:"case-insensitive operator ID",
            first:Endpoint{URL:"https://first.example",OperatorID:"Alpha"},
            second:Endpoint{URL:"https://second.example",OperatorID:"alpha"},
        },
        {
            name:"case-insensitive same host",
            first:Endpoint{URL:"https://SOLANA.EXAMPLE",OperatorID:"one"},
            second:Endpoint{URL:"https://solana.example",OperatorID:"two"},
        },
        {
            name:"default HTTPS port alias",
            first:Endpoint{URL:"https://solana.example",OperatorID:"one"},
            second:Endpoint{URL:"https://solana.example:443",OperatorID:"two"},
        },
        {
            name:"trailing DNS dot alias",
            first:Endpoint{URL:"https://solana.example",OperatorID:"one"},
            second:Endpoint{URL:"https://solana.example.",OperatorID:"two"},
        },
        {
            name:"same host different HTTPS ports",
            first:Endpoint{URL:"https://solana.example:443",OperatorID:"one"},
            second:Endpoint{URL:"https://solana.example:8443",OperatorID:"two"},
        },
    }
    for _,tc:=range cases{
        t.Run(tc.name,func(t *testing.T){
            if _,err:=NewObserver([]Endpoint{tc.first,tc.second},nil);err==nil{
                t.Fatal("two aliases of the same operator/host accepted as independent RPC witnesses")
            }
        })
    }
    if _,err:=NewObserver([]Endpoint{
        {URL:"https://first.example",OperatorID:"one"},
        {URL:"https://second.example",OperatorID:"two"},
    },nil);err!=nil {
        t.Fatalf("genuine distinct witnesses rejected: %v",err)
    }
}
