package jsonrpc

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThrottling(t *testing.T) {
	t.Parallel()

	th := NewThrottling(5, time.Millisecond*50)

	acquiredCh := make(chan struct{}, 10)
	releaseCh := make(chan struct{}, 10)
	finishedCh := make(chan struct{}, 10)
	waitTimeout := 5 * time.Second

	wg := sync.WaitGroup{}

	startBlockingRequest := func(value int) {
		wg.Add(1)

		go func() {
			defer wg.Done()

			res, err := th.AttemptRequest(context.Background(), func() (interface{}, error) {
				acquiredCh <- struct{}{}
				<-releaseCh

				return value, nil
			})

			require.NoError(t, err)
			assert.Equal(t, value, res.(int)) //nolint
			finishedCh <- struct{}{}
		}()
	}

	for i := 0; i < 5; i++ {
		startBlockingRequest(100)
	}

	for i := 0; i < 5; i++ {
		select {
		case <-acquiredCh:
		case <-time.After(waitTimeout):
			t.Fatal("timed out waiting for request acquisition")
		}
	}

	res, err := th.AttemptRequest(context.Background(), func() (interface{}, error) {
		return 1, nil
	})

	require.ErrorIs(t, err, errRequestLimitExceeded)
	assert.Nil(t, res)

	releaseCh <- struct{}{}

	select {
	case <-finishedCh:
	case <-time.After(waitTimeout):
		t.Fatal("timed out waiting for request release")
	}

	startBlockingRequest(10)

	select {
	case <-acquiredCh:
	case <-time.After(waitTimeout):
		t.Fatal("timed out waiting for replacement request acquisition")
	}

	res, err = th.AttemptRequest(context.Background(), func() (interface{}, error) {
		return 2, nil
	})

	require.ErrorIs(t, err, errRequestLimitExceeded)
	assert.Nil(t, res)

	for i := 0; i < 5; i++ {
		releaseCh <- struct{}{}
	}

	wg.Wait()

	res, err = th.AttemptRequest(context.Background(), func() (interface{}, error) {
		return 3, nil
	})

	require.NoError(t, err)
	assert.Equal(t, 3, res.(int)) //nolint
}
