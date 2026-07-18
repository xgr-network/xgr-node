package signer

import (
	"bytes"
	"fmt"
	"math/big"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/umbracle/fastrlp"
	"github.com/xgr-network/xgr-node/crypto"
	"github.com/xgr-network/xgr-node/types"
	"github.com/xgr-network/xgr-node/validators"
)

func TestIBFTExtraBLSRepackingPreservesAllFields(t *testing.T) {
	vals := testBLSValidators(5, 0)
	parent := testAggregatedSeal(0, "parent", 0x1f)
	oldCommitted := testAggregatedSeal(0, "old", 0x03)
	newCommitted := testAggregatedSeal(0, "new", 0x1c)
	proposer := []byte("proposer-seal")
	header := &types.Header{Number: 2, ExtraData: getTestExtraBytes(vals, nil, oldCommitted, parent, nil)}
	s := testBLSExtraSigner(proposer, newCommitted)

	_, err := s.WriteProposerSeal(header)
	require.NoError(t, err)
	assertBLSExtra(t, s, header, vals, proposer, oldCommitted, parent, nil)

	round := uint64(42)
	_, err = s.WriteCommittedSeals(header, round, map[types.Address][]byte{vals.At(0).Addr(): []byte("seal")})
	require.NoError(t, err)
	assertBLSExtra(t, s, header, vals, proposer, newCommitted, parent, &round)
}

func TestIBFTExtraBLSRepackingStress(t *testing.T) {
	const iterations = 100000
	vals := testBLSValidators(5, 0)
	parent := testAggregatedSeal(0, "parent", 0x1f)
	committed := testAggregatedSeal(0, "committed", 0x1f)
	proposer := []byte("proposer")
	s := testBLSExtraSigner(proposer, committed)

	for i := 0; i < iterations; i++ {
		header := &types.Header{Number: 2, ExtraData: getTestExtraBytes(vals, nil, &AggregatedSeal{}, parent, nil)}
		require.NoError(t, writeAndVerifyBLSExtra(s, header, vals, proposer, committed, parent, uint64(i)))
	}
}

func TestConcurrentIBFTExtraParserArenaPoolContention(t *testing.T) {
	const (
		workers    = 32
		iterations = 2000
	)

	start := make(chan struct{})
	errs := make(chan error, workers)
	var ready sync.WaitGroup
	var workersWG sync.WaitGroup
	ready.Add(workers)
	workersWG.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func(worker int) {
			defer workersWG.Done()
			vals := testBLSValidators(5, worker*17)
			parent := testAggregatedSeal(worker, "parent", 0x1f)
			committed := testAggregatedSeal(worker, "committed", 0x1f)
			proposer := []byte(fmt.Sprintf("proposer-%d", worker))
			s := testBLSExtraSigner(proposer, committed)
			ready.Done()
			<-start
			for iteration := 0; iteration < iterations; iteration++ {
				header := &types.Header{Number: 2, ExtraData: getTestExtraBytes(vals, nil, &AggregatedSeal{}, parent, nil)}
				if err := writeAndVerifyBLSExtra(s, header, vals, proposer, committed, parent, uint64(worker*iterations+iteration)); err != nil {
					errs <- fmt.Errorf("worker %d iteration %d: %w", worker, iteration, err)
					return
				}
			}
		}(worker)
	}
	ready.Wait()
	close(start)
	workersWG.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}

func TestIBFTExtraUpdateRejectsMalformedDataWithoutMutation(t *testing.T) {
	s := testBLSExtraSigner([]byte("proposer"), &AggregatedSeal{})
	for _, update := range []struct {
		name string
		fn   func(*types.Header) (*types.Header, error)
	}{
		{"proposer seal", s.WriteProposerSeal},
		{"committed seals", func(h *types.Header) (*types.Header, error) {
			return s.WriteCommittedSeals(h, 1, map[types.Address][]byte{types.StringToAddress("1"): []byte("seal")})
		}},
	} {
		t.Run(update.name, func(t *testing.T) {
			header := &types.Header{Number: 2, ExtraData: append(make([]byte, IstanbulExtraVanity), 0xc0)}
			original := append([]byte(nil), header.ExtraData...)
			updated, err := update.fn(header)
			require.Error(t, err)
			require.Nil(t, updated)
			require.Equal(t, original, header.ExtraData)
		})
	}
}

func TestIstanbulExtraLegacyLayoutsRemainReadableAndCanonicalizeOnUpdate(t *testing.T) {
	for _, test := range []struct {
		name           string
		fields         int
		number         uint64
		writeCommitted bool
		expectedParent bool
		expectedRound  *uint64
	}{
		{name: "three fields", fields: 3, number: 1},
		{name: "four fields", fields: 4, number: 2, writeCommitted: true, expectedParent: true},
		{name: "five fields", fields: 5, number: 2, writeCommitted: true, expectedParent: true, expectedRound: uint64Ptr(7)},
	} {
		t.Run(test.name, func(t *testing.T) {
			vals := testBLSValidators(5, test.fields*31)
			oldProposer := []byte(fmt.Sprintf("old-proposer-%d", test.fields))
			newProposer := []byte(fmt.Sprintf("new-proposer-%d", test.fields))
			oldCommitted := testAggregatedSeal(test.fields, "old", 0x03)
			parent := testAggregatedSeal(test.fields, "parent", 0x1f)
			newCommitted := testAggregatedSeal(test.fields, "new", 0x1c)
			header := &types.Header{Number: test.number, ExtraData: legacyExtraData(vals, oldProposer, oldCommitted, parent, test.expectedRound, test.fields)}
			s := testBLSExtraSigner(newProposer, newCommitted)

			assertBLSExtra(t, s, header, vals, oldProposer, oldCommitted, parentFor(test.expectedParent, parent), test.expectedRound)
			_, err := s.WriteProposerSeal(header)
			require.NoError(t, err)
			assertCanonicalExtraElementCount(t, header, 5)
			assertBLSExtra(t, s, header, vals, newProposer, oldCommitted, parentFor(test.expectedParent, parent), test.expectedRound)

			if test.writeCommitted {
				round := uint64(100 + test.fields)
				_, err = s.WriteCommittedSeals(header, round, map[types.Address][]byte{vals.At(0).Addr(): []byte("seal")})
				require.NoError(t, err)
				assertCanonicalExtraElementCount(t, header, 5)
				assertBLSExtra(t, s, header, vals, newProposer, newCommitted, parent, &round)
			}
		})
	}
}

func TestIstanbulExtraDecodersRejectUnsupportedElementCounts(t *testing.T) {
	vals := testBLSValidators(5, 0)
	for _, fields := range []int{2, 6} {
		t.Run(fmt.Sprintf("%d fields", fields), func(t *testing.T) {
			header := &types.Header{Number: 2, ExtraData: legacyExtraData(vals, []byte("proposer"), testAggregatedSeal(0, "committed", 1), testAggregatedSeal(0, "parent", 1), uint64Ptr(1), fields)}
			s := testBLSExtraSigner(nil, nil)
			_, err := s.GetIBFTExtra(header)
			require.Error(t, err)
			_, err = s.GetParentCommittedSeals(header)
			require.Error(t, err)
		})
	}
}

func writeAndVerifyBLSExtra(s *SignerImpl, header *types.Header, vals validators.Validators, proposer []byte, committed, parent *AggregatedSeal, round uint64) error {
	if _, err := s.WriteProposerSeal(header); err != nil {
		return fmt.Errorf("write proposer seal: %w", err)
	}
	if _, err := s.WriteCommittedSeals(header, round, map[types.Address][]byte{vals.At(0).Addr(): []byte("seal")}); err != nil {
		return fmt.Errorf("write committed seals: %w", err)
	}
	return checkBLSExtra(s, header, vals, proposer, committed, parent, &round)
}

func testBLSExtraSigner(proposerSeal []byte, committed Seals) *SignerImpl {
	km := &MockKeyManager{
		TypeFunc:                   func() validators.ValidatorType { return validators.BLSValidatorType },
		NewEmptyValidatorsFunc:     func() validators.Validators { return validators.NewBLSValidatorSet() },
		NewEmptyCommittedSealsFunc: func() Seals { return &AggregatedSeal{} },
		SignProposerSealFunc:       func([]byte) ([]byte, error) { return append([]byte(nil), proposerSeal...), nil },
		GenerateCommittedSealsFunc: func(map[types.Address][]byte, validators.Validators) (Seals, error) { return committed, nil },
	}
	return newTestSingleKeyManagerSigner(km)
}

func testBLSValidators(count, offset int) validators.Validators {
	vals := make([]*validators.BLSValidator, count)
	for i := range vals {
		address := make([]byte, types.AddressLength)
		publicKey := make([]byte, 48)
		for j := range address {
			address[j] = byte(offset + i + j + 1)
		}
		for j := range publicKey {
			publicKey[j] = byte(offset + i + j + 11)
		}
		vals[i] = validators.NewBLSValidator(types.BytesToAddress(address), publicKey)
	}
	return validators.NewBLSValidatorSet(vals...)
}

func testAggregatedSeal(worker int, kind string, bitmap int64) *AggregatedSeal {
	return &AggregatedSeal{Bitmap: big.NewInt(bitmap), Signature: []byte(fmt.Sprintf("%s-%d", kind, worker))}
}

func legacyExtraData(vals validators.Validators, proposer []byte, committed, parent *AggregatedSeal, round *uint64, fields int) []byte {
	body := types.MarshalRLPTo(func(ar *fastrlp.Arena) *fastrlp.Value {
		v := ar.NewArray()
		if fields >= 1 {
			v.Set(vals.MarshalRLPWith(ar))
		}
		if fields >= 2 {
			v.Set(ar.NewCopyBytes(proposer))
		}
		if fields >= 3 {
			v.Set(committed.MarshalRLPWith(ar))
		}
		if fields >= 4 {
			v.Set(parent.MarshalRLPWith(ar))
		}
		if fields >= 5 {
			if round == nil {
				v.Set(ar.NewNull())
			} else {
				v.Set(ar.NewBytes(toRoundBytes(*round)))
			}
		}
		if fields >= 6 {
			v.Set(ar.NewNull())
		}
		return v
	}, nil)
	return append(make([]byte, IstanbulExtraVanity), body...)
}

func assertCanonicalExtraElementCount(t *testing.T, header *types.Header, expected int) {
	t.Helper()
	var count int
	err := types.UnmarshalRlp(func(_ *fastrlp.Parser, v *fastrlp.Value) error {
		elems, err := v.GetElems()
		if err != nil {
			return err
		}
		count = len(elems)
		return nil
	}, header.ExtraData[IstanbulExtraVanity:])
	require.NoError(t, err)
	require.Equal(t, expected, count)
}

func assertBLSExtra(t *testing.T, s *SignerImpl, header *types.Header, expectedValidators validators.Validators, proposer []byte, committed, parent *AggregatedSeal, round *uint64) {
	t.Helper()
	require.NoError(t, checkBLSExtra(s, header, expectedValidators, proposer, committed, parent, round))
}

func checkBLSExtra(s *SignerImpl, header *types.Header, expectedValidators validators.Validators, proposer []byte, committed, parent *AggregatedSeal, round *uint64) error {
	extra, err := s.GetIBFTExtra(header)
	if err != nil {
		return fmt.Errorf("decode updated extra: %w", err)
	}
	if !bytes.Equal(proposer, extra.ProposerSeal) {
		return fmt.Errorf("proposer seal differs")
	}
	actualCommitted := extra.CommittedSeals.(*AggregatedSeal)
	if actualCommitted.Bitmap.Cmp(committed.Bitmap) != 0 || !bytes.Equal(actualCommitted.Signature, committed.Signature) {
		return fmt.Errorf("committed seal differs")
	}
	if parent == nil {
		if extra.ParentCommittedSeals != nil && extra.ParentCommittedSeals.Num() != 0 {
			return fmt.Errorf("parent committed seal should be semantically empty")
		}
	} else {
		actualParent := extra.ParentCommittedSeals.(*AggregatedSeal)
		if actualParent.Bitmap.Cmp(parent.Bitmap) != 0 || !bytes.Equal(actualParent.Signature, parent.Signature) {
			return fmt.Errorf("parent committed seal differs")
		}
	}
	if (round == nil) != (extra.RoundNumber == nil) || round != nil && *round != *extra.RoundNumber {
		return fmt.Errorf("round number differs")
	}
	if expectedValidators.Len() != extra.Validators.Len() {
		return fmt.Errorf("validator count differs")
	}
	for i := 0; i < expectedValidators.Len(); i++ {
		expected := expectedValidators.At(uint64(i)).(*validators.BLSValidator)
		actual := extra.Validators.At(uint64(i)).(*validators.BLSValidator)
		if expected.Address != actual.Address {
			return fmt.Errorf("validator %d address differs", i)
		}
		if !bytes.Equal(expected.BLSPublicKey, actual.BLSPublicKey) {
			return fmt.Errorf("validator %d BLS public key differs", i)
		}
	}
	return nil
}

func parentFor(present bool, parent *AggregatedSeal) *AggregatedSeal {
	if !present {
		return nil
	}
	return parent
}

func uint64Ptr(value uint64) *uint64 { return &value }

func TestAggregatedSealNumIsNilSafe(t *testing.T) {
	var nilSeal *AggregatedSeal
	require.Equal(t, 0, nilSeal.Num())
	require.Equal(t, 0, (&AggregatedSeal{}).Num())
	require.Equal(t, 0, (&AggregatedSeal{Bitmap: big.NewInt(0)}).Num())
	require.Equal(t, 3, (&AggregatedSeal{Bitmap: big.NewInt(5)}).Num())
}

func TestThreeFieldBLSExtraAtBlockTwoCanonicalizesSafely(t *testing.T) {
	vals := testBLSValidators(5, 73)
	oldProposer := []byte("old-proposer")
	newProposer := []byte("new-proposer")
	oldCommitted := testAggregatedSeal(73, "old", 3)
	newCommitted := testAggregatedSeal(73, "new", 7)
	newHeader := func() *types.Header {
		return &types.Header{Number: 2, ExtraData: legacyExtraData(vals, oldProposer, oldCommitted, &AggregatedSeal{}, nil, 3)}
	}
	s := testBLSExtraSigner(newProposer, newCommitted)
	header := newHeader()

	assertBLSExtra(t, s, header, vals, oldProposer, oldCommitted, nil, nil)
	parent, err := s.GetParentCommittedSeals(header)
	require.NoError(t, err)
	require.NotNil(t, parent)
	require.Equal(t, 0, parent.Num())
	_, err = s.CalculateHeaderHash(header)
	require.NoError(t, err)
	require.NoError(t, s.VerifyParentCommittedSeals(types.ZeroHash, header, vals, 1, false))
	require.ErrorIs(t, s.VerifyParentCommittedSeals(types.ZeroHash, header, vals, 1, true), ErrEmptyParentCommittedSeals)

	_, err = s.WriteProposerSeal(header)
	require.NoError(t, err)
	assertCanonicalExtraElementCount(t, header, 5)
	assertBLSExtra(t, s, header, vals, newProposer, oldCommitted, nil, nil)

	header = newHeader()
	round := uint64(11)
	_, err = s.WriteCommittedSeals(header, round, map[types.Address][]byte{vals.At(0).Addr(): []byte("seal")})
	require.NoError(t, err)
	assertCanonicalExtraElementCount(t, header, 5)
	assertBLSExtra(t, s, header, vals, oldProposer, newCommitted, nil, &round)
}

func TestCanonicalFiveFieldBLSExtraByteCompatibility(t *testing.T) {
	vals := testBLSValidators(5, 91)
	oldProposer := []byte("old-proposer")
	newProposer := []byte("new-proposer")
	oldCommitted := testAggregatedSeal(91, "old", 3)
	newCommitted := testAggregatedSeal(91, "new", 7)
	parent := testAggregatedSeal(91, "parent", 31)
	round := uint64(9)
	vanity := bytes.Repeat([]byte{0xab}, IstanbulExtraVanity)
	header := &types.Header{Number: 2, ExtraData: append(append([]byte(nil), vanity...), (&IstanbulExtra{Validators: vals, ProposerSeal: oldProposer, CommittedSeals: oldCommitted, ParentCommittedSeals: parent, RoundNumber: &round}).MarshalRLPTo(nil)...)}
	s := testBLSExtraSigner(newProposer, newCommitted)

	_, err := s.WriteProposerSeal(header)
	require.NoError(t, err)
	require.Equal(t, vanity, header.ExtraData[:IstanbulExtraVanity])
	require.Equal(t, append(append([]byte(nil), vanity...), (&IstanbulExtra{Validators: vals, ProposerSeal: newProposer, CommittedSeals: oldCommitted, ParentCommittedSeals: parent, RoundNumber: &round}).MarshalRLPTo(nil)...), header.ExtraData)

	newRound := uint64(10)
	_, err = s.WriteCommittedSeals(header, newRound, map[types.Address][]byte{vals.At(0).Addr(): []byte("seal")})
	require.NoError(t, err)
	require.Equal(t, vanity, header.ExtraData[:IstanbulExtraVanity])
	require.Equal(t, append(append([]byte(nil), vanity...), (&IstanbulExtra{Validators: vals, ProposerSeal: newProposer, CommittedSeals: newCommitted, ParentCommittedSeals: parent, RoundNumber: &newRound}).MarshalRLPTo(nil)...), header.ExtraData)
}

func TestMixedCurrentAndParentKeyManagersPreserveParentSeals(t *testing.T) {
	for _, test := range []struct {
		name        string
		current     validators.Validators
		currentOld  Seals
		currentNew  Seals
		parent      Seals
		currentType validators.ValidatorType
		parentType  validators.ValidatorType
	}{
		{"BLS current ECDSA parent", testBLSValidators(5, 101), testAggregatedSeal(101, "old", 3), testAggregatedSeal(101, "new", 7), &SerializedSeal{[]byte("parent-ecdsa")}, validators.BLSValidatorType, validators.ECDSAValidatorType},
		{"ECDSA current BLS parent", validators.NewECDSAValidatorSet(validators.NewECDSAValidator(types.StringToAddress("1"))), &SerializedSeal{[]byte("old")}, &SerializedSeal{[]byte("new")}, testAggregatedSeal(102, "parent", 7), validators.ECDSAValidatorType, validators.BLSValidatorType},
	} {
		t.Run(test.name, func(t *testing.T) {
			proposer := []byte("proposer")
			currentKM := mixedTestKeyManager(test.currentType, proposer, test.currentNew)
			parentKM := mixedTestKeyManager(test.parentType, nil, nil)
			s := NewSigner(currentKM, parentKM)
			round := uint64(4)
			header := &types.Header{Number: 2, ExtraData: getTestExtraBytes(test.current, []byte("old-proposer"), test.currentOld, test.parent, &round)}
			parentBytes := types.MarshalRLPTo(test.parent.MarshalRLPWith, nil)

			_, err := s.WriteProposerSeal(header)
			require.NoError(t, err)
			assertParentSealBytes(t, s, header, parentBytes)
			newRound := uint64(5)
			_, err = s.WriteCommittedSeals(header, newRound, map[types.Address][]byte{test.current.At(0).Addr(): []byte("seal")})
			require.NoError(t, err)
			assertParentSealBytes(t, s, header, parentBytes)
			assertCanonicalExtraElementCount(t, header, 5)
		})
	}
}

func mixedTestKeyManager(kind validators.ValidatorType, proposer []byte, committed Seals) *MockKeyManager {
	return &MockKeyManager{
		TypeFunc: func() validators.ValidatorType { return kind },
		NewEmptyValidatorsFunc: func() validators.Validators {
			if kind == validators.BLSValidatorType {
				return validators.NewBLSValidatorSet()
			}
			return validators.NewECDSAValidatorSet()
		},
		NewEmptyCommittedSealsFunc: func() Seals {
			if kind == validators.BLSValidatorType {
				return &AggregatedSeal{}
			}
			return &SerializedSeal{}
		},
		SignProposerSealFunc:       func([]byte) ([]byte, error) { return append([]byte(nil), proposer...), nil },
		GenerateCommittedSealsFunc: func(map[types.Address][]byte, validators.Validators) (Seals, error) { return committed, nil },
	}
}

func assertParentSealBytes(t *testing.T, s *SignerImpl, header *types.Header, expected []byte) {
	t.Helper()
	parent, err := s.GetParentCommittedSeals(header)
	require.NoError(t, err)
	ar := fastrlp.DefaultArenaPool.Get()
	actual := parent.MarshalRLPWith(ar).MarshalTo(nil)
	fastrlp.DefaultArenaPool.Put(ar)
	require.Equal(t, expected, actual)
}

func TestParentKeyManagerFallbackForEmptyParentSeal(t *testing.T) {
	vals := testBLSValidators(5, 131)
	usedFallback := false
	km := testBLSExtraSigner([]byte("proposer"), &AggregatedSeal{}).keyManager.(*MockKeyManager)
	km.NewEmptyCommittedSealsFunc = func() Seals {
		usedFallback = true
		return &AggregatedSeal{}
	}
	s := NewSigner(km, nil)
	header := &types.Header{Number: 2, ExtraData: legacyExtraData(vals, []byte("proposer"), &AggregatedSeal{}, &AggregatedSeal{}, nil, 3)}
	parent, err := s.GetParentCommittedSeals(header)
	require.NoError(t, err)
	require.True(t, usedFallback)
	require.Equal(t, 0, parent.Num())
	require.NoError(t, s.VerifyParentCommittedSeals(types.ZeroHash, header, vals, 1, false))
	require.ErrorIs(t, s.VerifyParentCommittedSeals(types.ZeroHash, header, vals, 1, true), ErrEmptyParentCommittedSeals)
}

func TestVerifyParentCommittedSealsUsesParentKeyManager(t *testing.T) {
	for _, test := range []struct {
		name        string
		current     validators.Validators
		parent      validators.Validators
		currentType validators.ValidatorType
		parentType  validators.ValidatorType
		currentSeal Seals
		parentSeal  Seals
	}{
		{"BLS current ECDSA parent", testBLSValidators(5, 141), validators.NewECDSAValidatorSet(validators.NewECDSAValidator(types.StringToAddress("2"))), validators.BLSValidatorType, validators.ECDSAValidatorType, testAggregatedSeal(141, "current", 1), &SerializedSeal{[]byte("parent")}},
		{"ECDSA current BLS parent", validators.NewECDSAValidatorSet(validators.NewECDSAValidator(types.StringToAddress("3"))), testBLSValidators(5, 151), validators.ECDSAValidatorType, validators.BLSValidatorType, &SerializedSeal{[]byte("current")}, testAggregatedSeal(151, "parent", 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			currentKM := mixedTestKeyManager(test.currentType, nil, test.currentSeal)
			currentKM.VerifyCommittedSealsFunc = func(Seals, []byte, validators.Validators) (int, error) {
				t.Fatal("current manager verified parent seal")
				return 0, nil
			}
			called := 0
			parentKM := mixedTestKeyManager(test.parentType, nil, nil)
			parentKM.VerifyCommittedSealsFunc = func(seal Seals, digest []byte, vals validators.Validators) (int, error) {
				called++
				require.Equal(t, test.parent.Type(), vals.Type())
				require.IsType(t, test.parentSeal, seal)
				zeroHash := types.ZeroHash
				require.Equal(t, crypto.Keccak256(wrapCommitHash(zeroHash[:])), digest)
				return 1, nil
			}
			s := NewSigner(currentKM, parentKM)
			header := &types.Header{Number: 2, ExtraData: getTestExtraBytes(test.current, nil, test.currentSeal, test.parentSeal, nil)}
			require.NoError(t, s.VerifyParentCommittedSeals(types.ZeroHash, header, test.parent, 1, true))
			require.Equal(t, 1, called)
		})
	}
}
