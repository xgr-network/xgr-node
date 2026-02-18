# xgrEngine (stub module)

This directory exists so that `xgrchain` can be built **standalone** even when the
private `xgrEngine` repository is not present.

It provides minimal stub packages for import resolution.

## Important: embedded builds

The stub package under `jsonrpc/xgr` is compiled only when **not** using
`-tags engine_embedded`.

This means:
- default / stub builds work standalone with this local module
- embedded builds require the real private `xgrEngine` module (for example via a
  different `replace` directive or a workspace override)
