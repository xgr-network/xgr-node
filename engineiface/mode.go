package engineiface

import "fmt"

const (
	ModeStub     = "stub"
	ModeEmbedded = "embedded"
)

func ValidateMode(mode string) error {
	switch mode {
	case "", ModeStub, ModeEmbedded:
		return nil
	default:
		return fmt.Errorf("invalid engine.mode %q (allowed: %s|%s)", mode, ModeStub, ModeEmbedded)
	}
}
