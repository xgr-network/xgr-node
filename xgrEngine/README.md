# xgrEngine (stub module)

This directory exists so that `xgrchain` can be built **standalone** even when the
private `xgrEngine` repository is not present.

It provides minimal stub packages for import resolution.

Internal builds may replace `github.com/xgr-network/xgrEngine` with the real engine repo
using a different `replace` directive.
