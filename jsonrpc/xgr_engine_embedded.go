//go:build engine_embedded

package jsonrpc

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/go-hclog"
	xgrsvc "github.com/xgr-network/xgr-node/jsonrpc/xgr"

	"github.com/xgr-network/xgrEngine/manager"
	"github.com/xgr-network/xgrEngine/xdala"
)

func newXGREndpointEmbedded(logger hclog.Logger, ethRPCURL, engineEOA string, enginePub33 []byte) (*xgrsvc.XGR, error) {
	if logger == nil {
		logger = hclog.L()
	}
	ctx := context.Background()

	// 1) SessionStore sofort (braucht kein JSON-RPC)
	store, err := xdala.NewPGSessionStore(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("init session store: %w", err)
	}

	// 2) Endpoint sofort mit Sessions (damit enqueue/wakeup etc. nicht mehr nil sind)
	ep := xgrsvc.New(xgrsvc.Config{
		Logger:      logger.Named("xgr"),
		EthRPCURL:   ethRPCURL,
		EngineEOA:   engineEOA,
		EnginePub33: enginePub33,
		Sessions:    store,
	})

	// 3) TxLayer + Background NACHLAUFEND starten (sonst Boot-Deadlock auf 127.0.0.1:8545)
	go func() {
		log := logger.Named("engine.boot")
		var lastErr error

		// kurze Retry-Phase reicht, weil JSON-RPC gleich hochkommt
		for i := 0; i < 120; i++ { // 120 * 250ms = 30s
			txl, err := xdala.NewRpcTxLayerFromEnv(ctx, logger.Named("xdala.tx"))
			if err == nil {
				eng := &xdala.Engine{
					Logger:    logger.Named("engine"),
					EthRPCURL: ethRPCURL,
					EngineEOA: engineEOA,
					Sessions:  store,
					Tx:        txl,
				}
				manager.StartBackground(ctx, logger, store, eng)
				log.Info("engine background started")
				return
			}

			lastErr = err
			// nicht spammen
			if i == 0 || i == 19 || i == 59 || i == 119 {
				log.Warn("tx layer not ready yet", "attempt", i+1, "err", err)
			}
			time.Sleep(250 * time.Millisecond)
		}
		log.Error("engine disabled: tx layer init failed", "err", lastErr)
	}()

	return ep, nil
}
