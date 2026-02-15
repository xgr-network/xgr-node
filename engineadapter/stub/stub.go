package stub

import "github.com/hashicorp/go-hclog"

func LogEnabled(logger hclog.Logger) {
	if logger != nil {
		logger.Info("engine adapter initialized in stub mode")
	}
}
