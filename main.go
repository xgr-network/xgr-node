package main

import (
	_ "embed"
	"github.com/xgr-network/xgr-node/command/root"
	"github.com/xgr-network/xgr-node/licenses"
)

var (
	//go:embed LICENSE
	license string
)

func main() {
	licenses.SetLicense(license)
	root.NewRootCommand().Execute()
}
