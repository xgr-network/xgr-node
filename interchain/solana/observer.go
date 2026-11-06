// Package solana contains read-only Solana RPC observations for XITA.
// It is deliberately NOT connected to the Direct-BLS signing path: a finalized
// transaction is not proof of a canonical XITA route, escrow or paid fee.
package solana

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// MainnetGenesis identifies Solana mainnet-beta, not an EVM chain ID.
const MainnetGenesis = "5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d"

// MainnetDomain is the Solana mainnet domain in the Hyperlane namespace.
// It is a messaging domain, NOT an eth_chainId response.
const MainnetDomain uint32 = 1399811149

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// Endpoint identifies one configured RPC operator. Two URLs from the same
// operator are only redundancy and MUST share one OperatorID.
type Endpoint struct {
	URL        string
	OperatorID string
}

// Observation contains agreement between two independently configured
// finalized RPC witnesses. It is NOT an Interchain message authorization.
type Observation struct {
	Signature          string
	Slot               uint64
	RawTransaction     []byte
	TransactionSHA256  [32]byte
	IndependentWitnesses int
}

type Observer struct {
	endpoints []Endpoint
	client    *http.Client
}

// NewObserver only accepts HTTPS endpoints with distinct operator identities.
// Endpoint/operator attribution must still be audited by node release policy.
func NewObserver(endpoints []Endpoint, client *http.Client) (*Observer, error) {
	if len(endpoints) < 2 {
		return nil, errors.New("Solana requires at least two independent RPC witnesses")
	}
	ids, urls := make(map[string]bool), make(map[string]bool)
	for _, ep := range endpoints {
		u, err := url.Parse(ep.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil ||
			u.Fragment != "" || u.RawQuery != "" || u.Path != "" {
			return nil, errors.New("Solana RPC must be a distinct HTTPS origin without URL credentials")
		}
		id := strings.TrimSpace(ep.OperatorID)
		if id == "" || strings.ContainsAny(id, " /\\") || ids[id] || urls[ep.URL] {
			return nil, errors.New("Solana RPC witness operator identities must be distinct")
		}
		ids[id], urls[ep.URL] = true, true
	}
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	return &Observer{endpoints: append([]Endpoint(nil), endpoints...), client: client}, nil
}

type rpcReply struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (o *Observer) call(ctx context.Context, endpoint Endpoint, method string, params []any, result any) error {
	payload, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  []any  `json:"params"`
	}{JSONRPC: "2.0", ID: 1, Method: method, Params: params})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.URL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := o.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s RPC transport: %w", method, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s RPC HTTP %d", method, res.StatusCode)
	}
	dec := json.NewDecoder(io.LimitReader(res.Body, 2<<20))
	var reply rpcReply
	if err := dec.Decode(&reply); err != nil {
		return fmt.Errorf("%s malformed RPC reply: %w", method, err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("%s RPC contains trailing data", method)
	}
	if reply.JSONRPC != "2.0" || reply.ID != 1 || reply.Error != nil ||
		len(reply.Result) == 0 || string(reply.Result) == "null" {
		return fmt.Errorf("%s RPC returned missing or invalid result", method)
	}
	if err := json.Unmarshal(reply.Result, result); err != nil {
		return fmt.Errorf("%s invalid result: %w", method, err)
	}
	return nil
}

// decodeSignature parses exactly one 64-byte Solana transaction signature.
func decodeSignature(encoded string) ([64]byte, error) {
	var signature [64]byte
	if len(encoded) < 64 || len(encoded) > 88 {
		return signature, errors.New("invalid Solana signature length")
	}
	number := new(big.Int)
	radix := big.NewInt(58)
	for _, c := range encoded {
		index := strings.IndexRune(base58Alphabet, c)
		if index < 0 {
			return signature, errors.New("invalid Solana base58 signature")
		}
		number.Mul(number, radix)
		number.Add(number, big.NewInt(int64(index)))
	}
	leading := 0
	for leading < len(encoded) && encoded[leading] == '1' {
		leading++
	}
	decoded := append(bytes.Repeat([]byte{0}, leading), number.Bytes()...)
	if len(decoded) != len(signature) {
		return signature, errors.New("Solana signature must decode to 64 bytes")
	}
	copy(signature[:], decoded)
	return signature, nil
}

type transactionReply struct {
	Slot        uint64 `json:"slot"`
	Meta        *struct {
		Err json.RawMessage `json:"err"`
	} `json:"meta"`
	Transaction []string `json:"transaction"`
}

// observe uses finalized only; no confirmed/latest fallback. Transaction
// bytes are returned as base64 rather than jsonParsed to compare exact bytes.
func (o *Observer) observe(ctx context.Context, ep Endpoint, signature string, sig [64]byte) (Observation, error) {
	var genesis string
	if err := o.call(ctx, ep, "getGenesisHash", []any{}, &genesis); err != nil {
		return Observation{}, err
	}
	if genesis != MainnetGenesis {
		return Observation{}, errors.New("Solana RPC genesis hash differs from mainnet-beta")
	}
	var finalizedSlot uint64
	if err := o.call(ctx, ep, "getSlot", []any{map[string]any{"commitment": "finalized"}}, &finalizedSlot); err != nil {
		return Observation{}, err
	}
	if finalizedSlot == 0 {
		return Observation{}, errors.New("Solana RPC has no finalized slot")
	}
	var tx transactionReply
	if err := o.call(ctx, ep, "getTransaction", []any{signature, map[string]any{
		"encoding": "base64", "commitment": "finalized", "maxSupportedTransactionVersion": 0,
	}}, &tx); err != nil {
		return Observation{}, err
	}
	if tx.Slot == 0 || tx.Slot > finalizedSlot || tx.Meta == nil ||
		len(tx.Meta.Err) == 0 || string(tx.Meta.Err) != "null" ||
		len(tx.Transaction) != 2 || tx.Transaction[1] != "base64" {
		return Observation{}, errors.New("Solana transaction is not successful and finalized")
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(tx.Transaction[0])
	if err != nil || len(raw) < 66 || raw[0] != 1 ||
		!bytes.Equal(raw[1:65], sig[:]) {
		return Observation{}, errors.New("Solana wire transaction signature mismatch")
	}
	return Observation{
		Signature: signature, Slot: tx.Slot, RawTransaction: raw,
		TransactionSHA256: sha256.Sum256(raw),
	}, nil
}

// FinalizedTransaction requires complete identical transaction bytes and slot
// from distinct operators. This is ONLY an RPC observation: release-pinned
// program IDs, decoded instructions/events, fees, route and escrow proofs are
// required before any validator may attest to an XITA message.
func (o *Observer) FinalizedTransaction(ctx context.Context, signature string) (Observation, error) {
	if o == nil || len(o.endpoints) < 2 {
		return Observation{}, errors.New("Solana witness policy is not initialized")
	}
	sig, err := decodeSignature(signature)
	if err != nil {
		return Observation{}, err
	}
	var first Observation
	for i, ep := range o.endpoints {
		got, err := o.observe(ctx, ep, signature, sig)
		if err != nil {
			return Observation{}, fmt.Errorf("Solana witness %s: %w", ep.OperatorID, err)
		}
		if i == 0 {
			first = got
		} else if got.Slot != first.Slot ||
			got.TransactionSHA256 != first.TransactionSHA256 ||
			!bytes.Equal(got.RawTransaction, first.RawTransaction) {
			return Observation{}, errors.New("independent Solana witnesses disagree")
		}
	}
	first.IndependentWitnesses = len(o.endpoints)
	return first, nil
}
